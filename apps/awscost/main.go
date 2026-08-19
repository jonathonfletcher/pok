// awscost — a singleton AWS cost poller. It publishes two disjoint families of OTLP metrics for
// EVERY EC2 instance in the account/region (k8s node or not, running or stopped):
//
//	ESTIMATED (cost.type=estimated) — the current run-rate projected over 24h, computed locally
//	  every ESTIMATE_INTERVAL from the AWS Pricing API and a live EC2 inventory. Per instance and
//	  cost component (compute / storage / public_ipv4): aws.cost.estimated.daily.usd.
//	ACTUAL   (cost.type=actual)    — ground-truth daily UnblendedCost from Cost Explorer every
//	  ACTUAL_INTERVAL: aws.cost.actual.daily.usd, grouped by service + instance_type.
//
// Both families use the same unit (USD/day) so estimated and actual compare directly.
//
// Credentials come from the instance profile via IMDS (no long-lived keys). Metrics egress via
// OTLP to the node-local otel-agent (standard OTEL_* env), which forwards to Honeycomb.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/pricing"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	otelresource "go.opentelemetry.io/otel/sdk/resource"
)

// lastTick is the unix time of the most recent estimate iteration; /healthz uses it for liveness.
var lastTick atomic.Int64

func main() {
	region := envString("AWS_REGION", "eu-west-1")
	// Pricing and Cost Explorer are global services fronted from us-east-1.
	globalRegion := envString("AWS_GLOBAL_REGION", "us-east-1")
	estInterval := envDuration("ESTIMATE_INTERVAL", 1*time.Hour)
	actInterval := envDuration("ACTUAL_INTERVAL", 24*time.Hour)
	actualDays := envInt("ACTUAL_DAYS", 2)
	ipv4Hourly := envFloat("PUBLIC_IPV4_HOURLY_USD", 0.005) // flat AWS rate since 2024-02-01

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdown, err := initMeter(ctx)
	if err != nil {
		log.Fatalf("init meter: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		log.Fatalf("aws config: %v", err)
	}

	// Preflight the credentials once so an IAM/IMDS problem is reported clearly at startup,
	// not only per-cycle. Non-fatal: the loops keep running and report credentials_ok to
	// Honeycomb, so the pod recovers on its own once the instance profile/IMDS is fixed.
	if _, err := awsCfg.Credentials.Retrieve(ctx); err != nil {
		log.Printf("startup: %s: %v", credHint, err)
	} else {
		log.Printf("startup: AWS credentials OK (IMDS instance profile)")
	}

	book := NewPriceBook(ipv4Hourly)
	poller := &Poller{
		ec2:        ec2.NewFromConfig(awsCfg),
		pricer:     NewPricer(pricing.NewFromConfig(awsCfg, func(o *pricing.Options) { o.Region = globalRegion }), region, book),
		ce:         costexplorer.NewFromConfig(awsCfg, func(o *costexplorer.Options) { o.Region = globalRegion }),
		est:        NewEstimator(book),
		emit:       newOTelEmitter(otel.Meter("awscost")),
		creds:      awsCfg.Credentials,
		actualDays: actualDays,
	}

	lastTick.Store(time.Now().Unix())
	// Liveness trips only if the estimate loop stops ticking for well over one interval.
	go serveHealth(envString("ADDR", ":8080"), 2*estInterval)

	log.Printf("awscost: region=%s estimate=%s actual=%s actual_days=%d", region, estInterval, actInterval, actualDays)
	runLoops(ctx, poller, estInterval, actInterval)
}

// runLoops runs the estimate and actual cycles until ctx is cancelled. Both fire once at start.
func runLoops(ctx context.Context, p *Poller, estInterval, actInterval time.Duration) {
	// Bound each cycle so a hung AWS call can't stall the loop (which would eventually trip the
	// liveness probe) — a fresh timeout per tick, well under either interval.
	const cycleTimeout = 2 * time.Minute
	estimate := func() {
		lastTick.Store(time.Now().Unix()) // heartbeat before the work, so an error still counts as "alive"
		cctx, cancel := context.WithTimeout(ctx, cycleTimeout)
		defer cancel()
		if err := p.EmitEstimated(cctx); err != nil {
			log.Printf("estimate: %v", err)
		}
	}
	actual := func() {
		cctx, cancel := context.WithTimeout(ctx, cycleTimeout)
		defer cancel()
		if err := p.EmitActual(cctx, time.Now()); err != nil {
			log.Printf("actual: %v", err)
		}
	}

	estT := time.NewTicker(estInterval)
	defer estT.Stop()
	actT := time.NewTicker(actInterval)
	defer actT.Stop()

	estimate()
	actual()
	for {
		select {
		case <-ctx.Done():
			return
		case <-estT.C:
			estimate()
		case <-actT.C:
			actual()
		}
	}
}

// initMeter wires an OTel MeterProvider; exporter/endpoint come from standard OTEL_* env
// (autoexport). Returns Shutdown so buffered metrics flush on exit.
func initMeter(ctx context.Context) (func(context.Context) error, error) {
	reader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return nil, err
	}
	res, err := otelresource.New(ctx,
		otelresource.WithAttributes(attribute.String("service.name", "awscost")),
		otelresource.WithFromEnv(),
		otelresource.WithTelemetrySDK(),
		otelresource.WithProcess(),
	)
	if err != nil {
		return nil, err
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithResource(res))
	otel.SetMeterProvider(mp)
	return mp.Shutdown, nil
}

// serveHealth exposes /healthz: 200 while the estimate loop is ticking, 503 if it has been
// silent for longer than stallAfter (scaled to the estimate interval, so a long interval doesn't
// cause false restarts).
func serveHealth(addr string, stallAfter time.Duration) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if time.Since(time.Unix(lastTick.Load(), 0)) > stallAfter {
			http.Error(w, "estimate loop stalled", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Printf("health server: %v", err)
	}
}

func envString(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envFloat(name string, def float64) float64 {
	if v := os.Getenv(name); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envDuration(name string, def time.Duration) time.Duration {
	if v := os.Getenv(name); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
