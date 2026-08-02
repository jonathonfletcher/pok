package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// initTracer wires up an OTel TracerProvider. The exporter is selected entirely
// by standard env vars via autoexport:
//   - default (OTEL_TRACES_EXPORTER unset or "otlp"): OTLP to OTEL_EXPORTER_OTLP_ENDPOINT
//     (in-cluster: the otel-agent ClusterIP Service, internalTrafficPolicy: Local -> node-local agent)
//   - "console": pretty-prints spans to stdout (used for local verification)
//
// Returns the provider's Shutdown so batched spans flush on exit.
func initTracer(ctx context.Context) (func(context.Context) error, error) {
	exp, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		// Default identity; OTEL_SERVICE_NAME / OTEL_RESOURCE_ATTRIBUTES override these.
		resource.WithAttributes(attribute.String("service.name", "helloworld")),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithProcess(),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
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
	ctx := context.Background()

	shutdown, err := initTracer(ctx)
	if err != nil {
		log.Fatalf("init tracer: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	tracer := otel.Tracer("helloworld")

	// url-visit event log -> Kafka. Async/fire-and-forget so serving never blocks on Kafka;
	// keyed by URL path so wsfeed can aggregate a per-URL count. Errors are logged, not fatal.
	visitW := &kafka.Writer{
		Addr:     kafka.TCP(env("KAFKA_BROKER", "main-kafka-bootstrap.kafka.svc:9094")),
		Topic:    env("TOPIC_VISITS", "url-visits"),
		Balancer: &kafka.Hash{},
		Async:    true,
		Completion: func(_ []kafka.Message, err error) {
			if err != nil {
				log.Printf("kafka visit write: %v", err)
			}
		},
	}
	defer visitW.Close()
	publishVisit := func(ctx context.Context, path string) {
		ev, _ := json.Marshal(map[string]any{"url": path, "ts": time.Now().Unix()})
		_ = visitW.WriteMessages(ctx, kafka.Message{Key: []byte(path), Value: ev})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Child span of the otelhttp server span (via r.Context()).
		_, span := tracer.Start(r.Context(), "say-hello")
		defer span.End()

		// Log this URL visit to Kafka (the "event log" feed). Non-blocking.
		publishVisit(r.Context(), r.URL.Path)

		host, _ := os.Hostname()
		span.SetAttributes(attribute.String("host.name", host))
		fmt.Fprintf(w, "hello world via %s — you visited %s\n", host, r.URL.Path)
	})

	// Wrap the mux so every request produces a server span. /healthz is filtered
	// out to avoid probe noise in traces.
	handler := otelhttp.NewHandler(mux, "http.server",
		otelhttp.WithFilter(func(r *http.Request) bool { return r.URL.Path != "/healthz" }),
	)

	srv := &http.Server{Addr: ":8080", Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		log.Printf("listening on :8080")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	// Graceful shutdown so in-flight requests finish and batched spans flush
	// when Kubernetes sends SIGTERM.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Printf("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
}
