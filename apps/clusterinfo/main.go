// clusterinfo — every 500ms, polls the Kubernetes API for ONE randomly-chosen cluster
// aspect and publishes it to the Kafka `cluster-info` topic, keyed by aspect (nodes, pods,
// namespaces, restarts, capacity, deployments, services, pvcs, node/pod metrics, and the cilium bgp/endpoints/
// identities/nodes/lb-pools/policies views). The topic is log-compacted, so the latest value
// per aspect is retained. wsfeed consumes this and shows it live. Uses the in-cluster
// ServiceAccount (read-only RBAC in deploy.yaml).
package main

import (
	"context"
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	otelresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// initTracer wires an OTel TracerProvider; exporter/endpoint come from standard OTEL_* env
// (autoexport). Returns Shutdown so batched spans flush on exit.
func initTracer(ctx context.Context) (func(context.Context) error, error) {
	exp, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, err
	}
	res, err := otelresource.New(ctx,
		otelresource.WithAttributes(attribute.String("service.name", "clusterinfo")),
		otelresource.WithFromEnv(),
		otelresource.WithTelemetrySDK(),
		otelresource.WithProcess(),
	)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	return tp.Shutdown, nil
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func main() {
	broker := env("KAFKA_BROKER", "main-kafka-bootstrap.kafka.svc:9094")
	topic := env("TOPIC_CLUSTER", "cluster-info")
	interval := 500 * time.Millisecond

	cfg, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("in-cluster config: %v", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("clientset: %v", err)
	}
	// Dynamic client for the Cilium (cilium.io) CRDs — read as unstructured, so clusterinfo
	// carries no Cilium Go dependency. Package-global; the cilium-* aspects use it directly.
	dyn, err = dynamic.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("dynamic client: %v", err)
	}

	w := &kafka.Writer{
		Addr:         kafka.TCP(broker),
		Topic:        topic,
		Balancer:     &kafka.Hash{},    // key (aspect) -> stable partition
		RequiredAcks: kafka.RequireAll, // struct-literal default is RequireNone (fire-and-forget)
	}
	defer w.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdown, err := initTracer(ctx)
	if err != nil {
		log.Fatalf("init tracer: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	// Liveness: /healthz reports whether the poll loop is still ticking (updated at the top of
	// poll, before Kafka) — so a broker outage doesn't crashloop the pod, but a wedged loop does.
	lastTick.Store(time.Now().Unix())
	go serveHealth(env("ADDR", ":8080"))

	log.Printf("clusterinfo: broker=%s topic=%s interval=%s", broker, topic, interval)
	t := time.NewTicker(interval)
	defer t.Stop()
	poll(ctx, cs, w) // once at start, then on each tick
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			poll(ctx, cs, w)
		}
	}
}

// dyn is the dynamic client used by the cilium-* aspects (set in main).
var dyn dynamic.Interface

// lastTick is the unix time of the most recent poll iteration; /healthz uses it for liveness.
var lastTick atomic.Int64

// serveHealth exposes /healthz: 200 while the poll loop is ticking, 503 if it has stalled.
func serveHealth(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if time.Now().Unix()-lastTick.Load() > 30 {
			http.Error(w, "poll loop stalled", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Printf("health server: %v", err)
	}
}

// aspectFn polls one cluster aspect and returns its Kafka message (keyed by aspect name).
type aspectFn func(context.Context, *kubernetes.Clientset) (kafka.Message, bool)

// aspects is the set of things clusterinfo publishes each tick. Add a function here to
// publish a new aspect — the topic is keyed + compacted, so it's one message per aspect.
var aspects = []aspectFn{
	aspectNodes,
	aspectPods,
	aspectNamespaces,
	aspectRestarts,
	aspectCapacity,
	aspectDeployments,
	aspectServices,
	aspectPVCs,
	aspectCiliumBGP,
	aspectCiliumEndpoints,
	aspectCiliumIdentities,
	aspectCiliumNodes,
	aspectCiliumLBPools,
	aspectCiliumPolicies,
	aspectNodeMetrics,
	aspectPodMetrics,
}

// poll publishes ONE randomly-chosen aspect per tick (not all of them), spreading the API
// load and staggering updates across aspects.
func poll(ctx context.Context, cs *kubernetes.Clientset, w *kafka.Writer) {
	ctx, span := otel.Tracer("clusterinfo").Start(ctx, "poll")
	defer span.End()

	lastTick.Store(time.Now().Unix()) // liveness heartbeat (before Kafka, so broker outages don't fail /healthz)
	a := aspects[rand.Intn(len(aspects))]
	// Bound the aspect's API call so a slow/hung kube-apiserver List can't wedge the poll loop
	// (a stalled loop stops advancing lastTick and trips the liveness probe into a restart).
	actx, acancel := context.WithTimeout(ctx, 5*time.Second)
	defer acancel()
	m, ok := a(actx, cs)
	span.SetAttributes(attribute.Bool("published", ok))
	if !ok {
		return
	}
	span.SetAttributes(attribute.String("aspect", string(m.Key)))
	wctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if err := w.WriteMessages(wctx, m); err != nil {
		span.RecordError(err)
		log.Printf("kafka write: %v", err)
	}
}

func msg(aspect string, v any) (kafka.Message, bool) {
	b, err := json.Marshal(v)
	if err != nil {
		return kafka.Message{}, false
	}
	return kafka.Message{Key: []byte(aspect), Value: b, Time: time.Now()}, true
}

func aspectNodes(ctx context.Context, cs *kubernetes.Clientset) (kafka.Message, bool) {
	nl, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("list nodes: %v", err)
		return kafka.Message{}, false
	}
	ready := 0
	names := []string{}
	for _, n := range nl.Items {
		names = append(names, n.Name)
		for _, c := range n.Status.Conditions {
			if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
				ready++
			}
		}
	}
	sort.Strings(names)
	return msg("nodes", map[string]any{"total": len(nl.Items), "ready": ready, "names": names})
}

func aspectPods(ctx context.Context, cs *kubernetes.Clientset) (kafka.Message, bool) {
	pl, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("list pods: %v", err)
		return kafka.Message{}, false
	}
	byPhase := map[string]int{}
	for _, p := range pl.Items {
		byPhase[string(p.Status.Phase)]++
	}
	return msg("pods", map[string]any{
		"total":     len(pl.Items),
		"running":   byPhase["Running"],
		"pending":   byPhase["Pending"],
		"succeeded": byPhase["Succeeded"],
		"failed":    byPhase["Failed"],
	})
}

func aspectNamespaces(ctx context.Context, cs *kubernetes.Clientset) (kafka.Message, bool) {
	nl, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("list namespaces: %v", err)
		return kafka.Message{}, false
	}
	names := make([]string, 0, len(nl.Items))
	for _, n := range nl.Items {
		names = append(names, n.Name)
	}
	sort.Strings(names)
	return msg("namespaces", map[string]any{"count": len(nl.Items), "names": names})
}

// restarts — total container restarts across all pods + the top offenders. Free: derived
// from the pod list (no extra RBAC beyond pods).
func aspectRestarts(ctx context.Context, cs *kubernetes.Clientset) (kafka.Message, bool) {
	pl, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("list pods (restarts): %v", err)
		return kafka.Message{}, false
	}
	type pr struct {
		Pod      string `json:"pod"`
		Restarts int32  `json:"restarts"`
	}
	var total int64
	top := []pr{}
	for _, p := range pl.Items {
		var r int32
		for _, c := range p.Status.ContainerStatuses {
			r += c.RestartCount
		}
		total += int64(r)
		if r > 0 {
			top = append(top, pr{p.Namespace + "/" + p.Name, r})
		}
	}
	sort.Slice(top, func(i, j int) bool { return top[i].Restarts > top[j].Restarts })
	if len(top) > 5 {
		top = top[:5]
	}
	return msg("restarts", map[string]any{"total": total, "top": top})
}

// capacity — total + allocatable CPU (cores) and memory (GiB) across nodes. Free: from the
// node list.
func aspectCapacity(ctx context.Context, cs *kubernetes.Clientset) (kafka.Message, bool) {
	nl, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("list nodes (capacity): %v", err)
		return kafka.Message{}, false
	}
	var cpuM, memB, aCpuM, aMemB int64
	for _, n := range nl.Items {
		cpuM += n.Status.Capacity.Cpu().MilliValue()
		memB += n.Status.Capacity.Memory().Value()
		aCpuM += n.Status.Allocatable.Cpu().MilliValue()
		aMemB += n.Status.Allocatable.Memory().Value()
	}
	round := func(f float64) float64 { return float64(int64(f*100)) / 100 }
	return msg("capacity", map[string]any{
		"cpu_cores":        round(float64(cpuM) / 1000),
		"memory_gib":       round(float64(memB) / (1 << 30)),
		"alloc_cpu_cores":  round(float64(aCpuM) / 1000),
		"alloc_memory_gib": round(float64(aMemB) / (1 << 30)),
	})
}

// deployments — total/ready and any not-fully-available (surfaces rollouts live).
// Needs RBAC: apps/deployments get,list.
func aspectDeployments(ctx context.Context, cs *kubernetes.Clientset) (kafka.Message, bool) {
	dl, err := cs.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("list deployments: %v", err)
		return kafka.Message{}, false
	}
	ready := 0
	notReady := []string{}
	for _, d := range dl.Items {
		want := int32(1)
		if d.Spec.Replicas != nil {
			want = *d.Spec.Replicas
		}
		if d.Status.ReadyReplicas >= want {
			ready++
		} else {
			notReady = append(notReady, d.Namespace+"/"+d.Name)
		}
	}
	sort.Strings(notReady)
	return msg("deployments", map[string]any{"total": len(dl.Items), "ready": ready, "not_ready": notReady})
}

// services — count by type + the LoadBalancer VIPs (the live feed shows its own edge VIP).
// Needs RBAC: services get,list.
func aspectServices(ctx context.Context, cs *kubernetes.Clientset) (kafka.Message, bool) {
	sl, err := cs.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("list services: %v", err)
		return kafka.Message{}, false
	}
	byType := map[string]int{}
	lbs := []map[string]string{}
	for _, s := range sl.Items {
		byType[string(s.Spec.Type)]++
		if s.Spec.Type == corev1.ServiceTypeLoadBalancer {
			for _, ing := range s.Status.LoadBalancer.Ingress {
				if ing.IP != "" {
					lbs = append(lbs, map[string]string{"svc": s.Namespace + "/" + s.Name, "ip": ing.IP})
				}
			}
		}
	}
	sort.Slice(lbs, func(i, j int) bool { return lbs[i]["ip"] < lbs[j]["ip"] })
	return msg("services", map[string]any{"total": len(sl.Items), "by_type": byType, "loadbalancers": lbs})
}

// pvcs — bound/pending counts + total requested GiB (ties to TopoLVM). Needs RBAC:
// persistentvolumeclaims get,list.
func aspectPVCs(ctx context.Context, cs *kubernetes.Clientset) (kafka.Message, bool) {
	pl, err := cs.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("list pvcs: %v", err)
		return kafka.Message{}, false
	}
	byPhase := map[string]int{}
	var gib float64
	for _, p := range pl.Items {
		byPhase[string(p.Status.Phase)]++
		if q, ok := p.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			gib += float64(q.Value()) / (1 << 30)
		}
	}
	return msg("pvcs", map[string]any{
		"total":         len(pl.Items),
		"bound":         byPhase["Bound"],
		"pending":       byPhase["Pending"],
		"requested_gib": float64(int64(gib*100)) / 100,
	})
}

// cgvr builds a cilium.io/v2 GroupVersionResource for the dynamic client.
func cgvr(resource string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "cilium.io", Version: "v2", Resource: resource}
}

// mgvr is the GroupVersionResource for the resource Metrics API (metrics.k8s.io/v1beta1),
// served by metrics-server (see tf/k8s/helm_metrics_server.tf). Read via the dynamic client.
func mgvr(res string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: res}
}

func asString(v any) string { s, _ := v.(string); return s }

// quantityMilli parses a k8s quantity ("123m", "1", "1500m") to millicores.
func quantityMilli(s string) int64 {
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return 0
	}
	return q.MilliValue()
}

// quantityMiB parses a k8s quantity ("456789Ki", "512Mi", "1Gi") to MiB.
func quantityMiB(s string) int64 {
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return 0
	}
	return q.Value() / (1 << 20)
}

// aspectNodeMetrics — per-node CPU (millicores) + memory (MiB) usage from the Metrics API.
// Needs metrics-server; RBAC: metrics.k8s.io nodes get,list.
func aspectNodeMetrics(ctx context.Context, _ *kubernetes.Clientset) (kafka.Message, bool) {
	l, err := dyn.Resource(mgvr("nodes")).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("list nodemetrics: %v", err)
		return kafka.Message{}, false
	}
	type nm struct {
		Name   string `json:"name"`
		CPUm   int64  `json:"cpu_m"`
		MemMiB int64  `json:"mem_mib"`
	}
	nodes := make([]nm, 0, len(l.Items))
	var totCPU, totMem int64
	for _, it := range l.Items {
		usage, _, _ := unstructured.NestedStringMap(it.Object, "usage")
		cpu, mem := quantityMilli(usage["cpu"]), quantityMiB(usage["memory"])
		totCPU += cpu
		totMem += mem
		nodes = append(nodes, nm{it.GetName(), cpu, mem})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	return msg("node-metrics", map[string]any{"nodes": nodes, "total_cpu_m": totCPU, "total_mem_mib": totMem})
}

// aspectPodMetrics — top pods by CPU usage across all namespaces, from the Metrics API.
// RBAC: metrics.k8s.io pods get,list.
func aspectPodMetrics(ctx context.Context, _ *kubernetes.Clientset) (kafka.Message, bool) {
	l, err := dyn.Resource(mgvr("pods")).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("list podmetrics: %v", err)
		return kafka.Message{}, false
	}
	type pm struct {
		Pod    string `json:"pod"`
		CPUm   int64  `json:"cpu_m"`
		MemMiB int64  `json:"mem_mib"`
	}
	pods := make([]pm, 0, len(l.Items))
	for _, it := range l.Items {
		containers, _, _ := unstructured.NestedSlice(it.Object, "containers")
		var cpu, mem int64
		for _, c := range containers {
			cm, ok := c.(map[string]any)
			if !ok {
				continue
			}
			u, ok := cm["usage"].(map[string]any)
			if !ok {
				continue
			}
			cpu += quantityMilli(asString(u["cpu"]))
			mem += quantityMiB(asString(u["memory"]))
		}
		pods = append(pods, pm{it.GetNamespace() + "/" + it.GetName(), cpu, mem})
	}
	sort.Slice(pods, func(i, j int) bool { return pods[i].CPUm > pods[j].CPUm })
	if len(pods) > 8 {
		pods = pods[:8]
	}
	return msg("pod-metrics", map[string]any{"top_cpu": pods})
}

// cilium-bgp — per-node BGP peering state from CiliumBGPNodeConfig: how many sessions are
// established (the BGP that drives the LB VIPs), plus any that aren't.
func aspectCiliumBGP(ctx context.Context, _ *kubernetes.Clientset) (kafka.Message, bool) {
	l, err := dyn.Resource(cgvr("ciliumbgpnodeconfigs")).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("list ciliumbgpnodeconfigs: %v", err)
		return kafka.Message{}, false
	}
	total, established := 0, 0
	down := []string{}
	for _, it := range l.Items {
		node := it.GetName()
		instances, _, _ := unstructured.NestedSlice(it.Object, "status", "bgpInstances")
		for _, inst := range instances {
			im, ok := inst.(map[string]any)
			if !ok {
				continue
			}
			peers, _ := im["peers"].([]any)
			for _, pr := range peers {
				pm, ok := pr.(map[string]any)
				if !ok {
					continue
				}
				total++
				state, _ := pm["peeringState"].(string)
				if state == "established" {
					established++
				} else {
					addr, _ := pm["peerAddress"].(string)
					down = append(down, node+"->"+addr+"("+state+")")
				}
			}
		}
	}
	sort.Strings(down)
	return msg("cilium-bgp", map[string]any{"peers": total, "established": established, "down": down})
}

// cilium-endpoints — count of CiliumEndpoints (≈ networked pods).
func aspectCiliumEndpoints(ctx context.Context, _ *kubernetes.Clientset) (kafka.Message, bool) {
	l, err := dyn.Resource(cgvr("ciliumendpoints")).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("list ciliumendpoints: %v", err)
		return kafka.Message{}, false
	}
	return msg("cilium-endpoints", map[string]any{"count": len(l.Items)})
}

// cilium-identities — count of Cilium security identities.
func aspectCiliumIdentities(ctx context.Context, _ *kubernetes.Clientset) (kafka.Message, bool) {
	l, err := dyn.Resource(cgvr("ciliumidentities")).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("list ciliumidentities: %v", err)
		return kafka.Message{}, false
	}
	return msg("cilium-identities", map[string]any{"count": len(l.Items)})
}

// cilium-nodes — count of CiliumNodes + their names.
func aspectCiliumNodes(ctx context.Context, _ *kubernetes.Clientset) (kafka.Message, bool) {
	l, err := dyn.Resource(cgvr("ciliumnodes")).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("list ciliumnodes: %v", err)
		return kafka.Message{}, false
	}
	names := make([]string, 0, len(l.Items))
	for _, it := range l.Items {
		names = append(names, it.GetName())
	}
	sort.Strings(names)
	return msg("cilium-nodes", map[string]any{"count": len(l.Items), "names": names})
}

// cilium-lb-pools — the LoadBalancer IP pools and their ranges.
func aspectCiliumLBPools(ctx context.Context, _ *kubernetes.Clientset) (kafka.Message, bool) {
	l, err := dyn.Resource(cgvr("ciliumloadbalancerippools")).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("list ciliumloadbalancerippools: %v", err)
		return kafka.Message{}, false
	}
	type pool struct {
		Name     string   `json:"name"`
		Disabled bool     `json:"disabled"`
		Ranges   []string `json:"ranges"`
	}
	pools := []pool{}
	for _, it := range l.Items {
		p := pool{Name: it.GetName()}
		p.Disabled, _, _ = unstructured.NestedBool(it.Object, "spec", "disabled")
		blocks, _, _ := unstructured.NestedSlice(it.Object, "spec", "blocks")
		for _, b := range blocks {
			bm, ok := b.(map[string]any)
			if !ok {
				continue
			}
			if cidr, _ := bm["cidr"].(string); cidr != "" {
				p.Ranges = append(p.Ranges, cidr)
			} else if start, _ := bm["start"].(string); start != "" {
				if stop, _ := bm["stop"].(string); stop != "" && stop != start {
					p.Ranges = append(p.Ranges, start+"-"+stop)
				} else {
					p.Ranges = append(p.Ranges, start)
				}
			}
		}
		pools = append(pools, p)
	}
	return msg("cilium-lb-pools", map[string]any{"count": len(l.Items), "pools": pools})
}

// cilium-policies — CiliumNetworkPolicy + CiliumClusterwideNetworkPolicy counts.
func aspectCiliumPolicies(ctx context.Context, _ *kubernetes.Clientset) (kafka.Message, bool) {
	cnp, e1 := dyn.Resource(cgvr("ciliumnetworkpolicies")).List(ctx, metav1.ListOptions{})
	ccnp, e2 := dyn.Resource(cgvr("ciliumclusterwidenetworkpolicies")).List(ctx, metav1.ListOptions{})
	if e1 != nil || e2 != nil {
		log.Printf("list cilium policies: %v / %v", e1, e2)
		return kafka.Message{}, false
	}
	return msg("cilium-policies", map[string]any{"namespaced": len(cnp.Items), "clusterwide": len(ccnp.Items)})
}
