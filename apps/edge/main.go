// edge — the single external front door for the live-feed stack. A tiny reverse proxy so
// the browser gets one origin (WebSocket at /ws must be same-origin as the page):
//
//	/ws        -> wsfeed      (WebSocket; httputil handles the Upgrade)
//	/hw, /hw/* -> helloworld (each visit is logged to Kafka by helloworld)
//	/*         -> static      (the frontend: index.html + /static/…)
//
// stdlib reverse proxy (net/http/httputil) + OpenTelemetry: incoming HTTP requests get a
// server span (via otelhttp) and trace context is propagated to the upstreams, so the /hw and
// /static paths are visible in Honeycomb. /ws (a long-lived hijacked WebSocket) and /healthz
// are filtered out to avoid marathon spans / probe noise. Upstreams overridable by env. This
// is the app-layer stand-in for an ingress controller (none installed).
package main

import (
	"context"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
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
		resource.WithAttributes(attribute.String("service.name", "edge")),
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

// proxyTo builds a reverse proxy to raw. If trace, it propagates trace context to the upstream
// via otelhttp's transport (NOT for the WebSocket upstream — the Upgrade must pass through raw).
func proxyTo(raw string, trace bool) *httputil.ReverseProxy {
	u, err := url.Parse(raw)
	if err != nil {
		log.Fatalf("bad upstream %q: %v", raw, err)
	}
	p := httputil.NewSingleHostReverseProxy(u)
	if trace {
		p.Transport = otelhttp.NewTransport(http.DefaultTransport)
	}
	return p
}

func main() {
	ctx := context.Background()
	shutdown, err := initTracer(ctx)
	if err != nil {
		log.Fatalf("init tracer: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	ws := proxyTo(env("WS_UPSTREAM", "http://wsfeed.app-livefeed.svc.cluster.local:80"), false)
	hw := proxyTo(env("HW_UPSTREAM", "http://helloworld.app-helloworld.svc.cluster.local:80"), true)
	static := proxyTo(env("STATIC_UPSTREAM", "http://static.app-livefeed.svc.cluster.local:80"), true)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.Handle("/ws", ws)  // exact — the WebSocket endpoint
	mux.Handle("/hw", hw)  // /hw and…
	mux.Handle("/hw/", hw) // …/hw/* -> helloworld (logs the visit)
	mux.Handle("/", static)

	// Server span per request, except /ws (long-lived WebSocket) and /healthz (probe noise).
	handler := otelhttp.NewHandler(mux, "http.server",
		otelhttp.WithFilter(func(r *http.Request) bool {
			return r.URL.Path != "/healthz" && r.URL.Path != "/ws"
		}),
	)

	addr := env("ADDR", ":8080")
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("edge: /ws->wsfeed /hw->helloworld /*->static, listening %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
}
