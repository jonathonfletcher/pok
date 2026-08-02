// static — a pure Go file server for the cluster-live-feed frontend.
//
// It serves the embedded web/ tree (index.html at /, assets under /static/…). It is
// deliberately stateless and dependency-free: the live data comes from the wsfeed service
// over /ws (reverse-proxied to the same origin by the gateway), and per-URL visit events
// come from the helloworld app — neither is this app's concern.
//
// OpenTelemetry: incoming HTTP requests get a server span (via otelhttp) so the / and
// /static/… paths are visible in Honeycomb. /healthz is filtered out to avoid probe noise.
package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

//go:embed web
var webFS embed.FS

// initTracer wires an OTel TracerProvider; exporter/endpoint come from standard OTEL_* env
// (autoexport). Returns Shutdown so batched spans flush on exit.
func initTracer(ctx context.Context) (func(context.Context) error, error) {
	exp, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, err
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(attribute.String("service.name", "static")),
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

func main() {
	ctx := context.Background()
	shutdown, err := initTracer(ctx)
	if err != nil {
		log.Fatalf("init tracer: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	root, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("embed sub: %v", err)
	}
	files := http.FileServer(http.FS(root))

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.Handle("/", files) // "/" -> web/index.html, "/static/…" -> web/static/…

	// Wrap the mux so every request produces a server span. /healthz is filtered
	// out to avoid probe noise in traces.
	handler := otelhttp.NewHandler(logRequests(mux), "http.server",
		otelhttp.WithFilter(func(r *http.Request) bool { return r.URL.Path != "/healthz" }),
	)

	addr := ":8080"
	if v := os.Getenv("ADDR"); v != "" {
		addr = v
	}
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("static: serving embedded web/ on %s", addr)
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

// logRequests is minimal request logging (stdout) — no Kafka here; url-visit events are
// the helloworld app's job.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
