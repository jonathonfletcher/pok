// visitcounter — consumes the `url-visits` event log and sums a per-URL count over the topic's
// retained window (url-visits is delete/24h, so the count is rebuilt from the log on restart, not a
// durable cumulative total), publishing the latest {count,last} per URL to the log-COMPACTED `visit-counts` topic (keyed
// by URL). Single replica: one authoritative counter. Every wsfeed replica then relays
// visit-counts identically, so browsers see consistent totals regardless of which wsfeed they
// hit (the aggregation lives here, not per-wsfeed). /healthz reflects the consume loop.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// maxVisitURLs bounds distinct tracked URLs. /hw/* is publicly routable, so an attacker could
// otherwise mint unlimited paths and grow the counts map + visit-counts topic without limit.
const maxVisitURLs = 512

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// initTracer wires an OTel TracerProvider; exporter/endpoint come from standard OTEL_* env
// (autoexport). Returns Shutdown so batched spans flush on exit.
func initTracer(ctx context.Context) (func(context.Context) error, error) {
	exp, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, err
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(attribute.String("service.name", "visitcounter")),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithProcess(),
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

// serveHealth exposes /healthz: 200 (process liveness). Deliberately NOT tied to message
// freshness — url-visits is event-driven and can be quiet for long stretches, which would
// otherwise fail the liveness probe on a perfectly healthy pod (unlike clusterinfo, which
// produces every tick).
func serveHealth(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Printf("health server: %v", err)
	}
}

func main() {
	broker := env("KAFKA_BROKER", "main-kafka-bootstrap.kafka.svc:9094")
	inTopic := env("TOPIC_VISITS", "url-visits")
	outTopic := env("TOPIC_COUNTS", "visit-counts")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdown, err := initTracer(ctx)
	if err != nil {
		log.Fatalf("init tracer: %v", err)
	}
	// Flush batched spans on exit (ctx is canceled by SIGTERM, so use a fresh context).
	defer func() { _ = shutdown(context.Background()) }()
	tracer := otel.Tracer("visitcounter")

	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:     []string{broker},
		Topic:       inTopic,
		Partition:   0, // single-partition; read it all, no consumer group
		StartOffset: kafka.FirstOffset,
		MinBytes:    1,
		MaxBytes:    10e6,
		MaxWait:     500 * time.Millisecond,
	})
	defer r.Close()
	w := &kafka.Writer{
		Addr:         kafka.TCP(broker),
		Topic:        outTopic,
		Balancer:     &kafka.Hash{},    // key (URL) -> stable partition
		RequiredAcks: kafka.RequireAll, // struct-literal default is RequireNone (fire-and-forget)
	}
	defer w.Close()

	go serveHealth(env("ADDR", ":8080"))

	log.Printf("visitcounter: broker=%s in=%s out=%s", broker, inTopic, outTopic)
	counts := map[string]int64{}
	for {
		m, err := r.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("url-visits read: %v", err)
			time.Sleep(time.Second)
			continue
		}
		url := string(m.Key)
		if url == "" {
			var ev struct {
				URL string `json:"url"`
			}
			_ = json.Unmarshal(m.Value, &ev)
			url = ev.URL
		}
		if url == "" {
			continue
		}
		if _, known := counts[url]; !known && len(counts) >= maxVisitURLs {
			continue // cap distinct URLs to bound memory + topic (public front door)
		}
		counts[url]++
		// One short span per aggregated visit: covers counting through the publish.
		sctx, span := tracer.Start(ctx, "visit.aggregate")
		span.SetAttributes(attribute.String("url", url), attribute.Int64("count", counts[url]))
		ts := m.Time.Unix()
		if ts <= 0 {
			ts = time.Now().Unix()
		}
		data, _ := json.Marshal(map[string]any{"count": counts[url], "last": ts})
		wctx, cancel := context.WithTimeout(sctx, 4*time.Second)
		if err := w.WriteMessages(wctx, kafka.Message{Key: []byte(url), Value: data, Time: time.Now()}); err != nil {
			span.RecordError(err)
			log.Printf("visit-counts write: %v", err)
		}
		cancel()
		span.End()
	}
}
