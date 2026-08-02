// wsfeed — consumes the Kafka feeds and streams the latest value per subject to browsers
// over a WebSocket at /ws, in the emf-compatible envelope {subject, topic, ts, data}.
//
// Two feeds:
//   - cluster-info : keyed by aspect (nodes/pods/…), latest value per aspect. Subject
//     "cluster/<aspect>". The topic is log-compacted, so reading from the start yields the
//     current value per aspect.
//   - visit-counts : compacted {count,last} per URL, produced by the single visitcounter
//     aggregator (subject "visit/<url>"). Relayed latest-wins; every wsfeed replica sees
//     identical counts because the aggregation is centralized in visitcounter, not per-replica.
//
// Each replica reads every partition (topics are single-partition) with no consumer group,
// so every wsfeed holds the full state independently and can serve any client — no
// cross-replica coordination.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// initTracer wires an OTel TracerProvider; exporter/endpoint come from standard OTEL_* env
// (autoexport). Returns Shutdown so batched spans flush on exit.
func initTracer(ctx context.Context) (func(context.Context) error, error) {
	exp, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, err
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(attribute.String("service.name", "wsfeed")),
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

// envelope is what the browser (static/app.js) expects on /ws.
type envelope struct {
	Subject string          `json:"subject"`
	Topic   string          `json:"topic"`
	TS      int64           `json:"ts"` // epoch seconds
	Data    json.RawMessage `json:"data"`
}

// hub keeps the latest envelope per subject and fans live updates out to clients.
type hub struct {
	mu      sync.RWMutex
	state   map[string]envelope
	clients map[chan envelope]struct{}
}

func newHub() *hub {
	return &hub{state: map[string]envelope{}, clients: map[chan envelope]struct{}{}}
}

// publish records the latest value for a subject and fans it out to all clients.
func (h *hub) publish(e envelope) {
	h.mu.Lock()
	h.state[e.Subject] = e
	for ch := range h.clients {
		select {
		case ch <- e:
		default: // drop for a slow client rather than block the consumer
		}
	}
	h.mu.Unlock()
}

// snapshot returns the current per-subject state (the seed for a new client).
func (h *hub) snapshot() []envelope {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]envelope, 0, len(h.state))
	for _, e := range h.state {
		out = append(out, e)
	}
	return out
}

func (h *hub) register() chan envelope {
	ch := make(chan envelope, 256)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *hub) unregister(ch chan envelope) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
	close(ch)
}

func nowSec() int64 { return time.Now().Unix() }

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func newReader(broker, topic string) *kafka.Reader {
	return kafka.NewReader(kafka.ReaderConfig{
		Brokers:     []string{broker},
		Topic:       topic,
		Partition:   0, // topics are single-partition; read it all, no consumer group
		StartOffset: kafka.FirstOffset,
		MinBytes:    1,
		MaxBytes:    10e6,
		MaxWait:     500 * time.Millisecond,
	})
}

// consumeClusterInfo: each message's key is the aspect, value is JSON — latest wins.
func consumeClusterInfo(ctx context.Context, r *kafka.Reader, h *hub) {
	for {
		m, err := r.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("cluster-info read: %v", err)
			time.Sleep(time.Second)
			continue
		}
		aspect := string(m.Key)
		if aspect == "" {
			aspect = "unknown"
		}
		ts := m.Time.Unix()
		if ts <= 0 {
			ts = nowSec()
		}
		h.publish(envelope{
			Subject: "cluster/" + aspect,
			Topic:   "cluster/" + aspect,
			TS:      ts,
			Data:    json.RawMessage(m.Value),
		})
	}
}

// consumeVisitCounts: each message's key is the URL, value is the {count,last} JSON produced
// by the visitcounter aggregator (compacted topic) — latest wins. Relayed as "visit/<url>".
func consumeVisitCounts(ctx context.Context, r *kafka.Reader, h *hub) {
	for {
		m, err := r.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("visit-counts read: %v", err)
			time.Sleep(time.Second)
			continue
		}
		url := string(m.Key)
		if url == "" {
			continue
		}
		ts := m.Time.Unix()
		if ts <= 0 {
			ts = nowSec()
		}
		subj := "visit/" + strings.TrimPrefix(url, "/")
		h.publish(envelope{
			Subject: subj,
			Topic:   subj,
			TS:      ts,
			Data:    json.RawMessage(m.Value),
		})
	}
}

func (h *hub) serveWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Restrict browser origins to the public front-door hosts (ALLOWED_ORIGINS). Same-origin
		// and non-browser clients (no Origin header, e.g. curl) are still accepted.
		OriginPatterns: allowedOrigins,
		// permessage-deflate for clients that offer it (browsers). The feed is repetitive JSON
		// (cluster-info + the full-state seed), so context-takeover reuses the sliding window
		// across messages for a better ratio (~32 KiB window per connection).
		CompressionMode: websocket.CompressionContextTakeover,
	})
	if err != nil {
		return
	}
	defer c.CloseNow()

	// CloseRead drains inbound frames (handling ping/close) and returns a context that is
	// cancelled when the client disconnects — without it a write-only server never notices a
	// gone client, leaking the goroutine, channel, and hub entry until the next write fails.
	ctx := c.CloseRead(r.Context())
	ch := h.register()
	defer h.unregister(ch)

	tracer := otel.Tracer("wsfeed")

	// messagesSent counts every envelope written to this client (seed + live). Emitted as a
	// short "ws.disconnect" span when the connection ends, alongside the session duration.
	var messagesSent int64
	connectedAt := time.Now()
	defer func() {
		_, span := tracer.Start(context.Background(), "ws.disconnect")
		span.SetAttributes(
			attribute.Int64("session_duration_ms", time.Since(connectedAt).Milliseconds()),
			attribute.Int64("messages_sent", messagesSent),
		)
		span.End()
	}()

	// Seed: current value per subject, then live updates.
	seed := h.snapshot()
	for _, e := range seed {
		if err := writeJSON(ctx, c, e); err != nil {
			return
		}
		messagesSent++
	}

	// Short-lived span marking a successful connect + seed (start then End immediately).
	_, connectSpan := tracer.Start(context.Background(), "ws.connect")
	connectSpan.SetAttributes(attribute.Int("seed_envelopes", len(seed)))
	connectSpan.End()

	for {
		select {
		case <-ctx.Done():
			return
		case e := <-ch:
			if err := writeJSON(ctx, c, e); err != nil {
				return
			}
			messagesSent++
		}
	}
}

func writeJSON(ctx context.Context, c *websocket.Conn, e envelope) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.Write(wctx, websocket.MessageText, b)
}

// allowedOrigins are the browser Origins accepted for /ws (set in main from ALLOWED_ORIGINS).
var allowedOrigins []string

func main() {
	broker := env("KAFKA_BROKER", "main-kafka-bootstrap.kafka.svc:9094")
	addr := env("ADDR", ":8080")
	allowedOrigins = strings.Split(env("ALLOWED_ORIGINS", "pok.somegroup.net,demo.somegroup.net,dev.somegroup.net"), ",")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdown, err := initTracer(ctx)
	if err != nil {
		log.Fatalf("init tracer: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	h := newHub()
	rCluster := newReader(broker, env("TOPIC_CLUSTER", "cluster-info"))
	rVisits := newReader(broker, env("TOPIC_COUNTS", "visit-counts"))
	defer rCluster.Close()
	defer rVisits.Close()
	go consumeClusterInfo(ctx, rCluster, h)
	go consumeVisitCounts(ctx, rVisits, h)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/ws", h.serveWS)

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sc, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sc)
	}()
	log.Printf("wsfeed: broker=%s listening %s", broker, addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
