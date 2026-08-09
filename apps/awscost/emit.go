package main

import (
	"context"
	"log"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Emitter records a gauge value with string attributes. It is an interface so the poller can be
// tested with a recorder instead of a real MeterProvider.
type Emitter interface {
	Gauge(ctx context.Context, name string, value float64, attrs map[string]string)
}

// otelEmitter adapts an OTel Meter to Emitter, caching one Float64Gauge per metric name (the
// SDK expects an instrument to be created once and reused).
type otelEmitter struct {
	meter  metric.Meter
	mu     sync.Mutex
	gauges map[string]metric.Float64Gauge
}

// newOTelEmitter returns an Emitter backed by meter.
func newOTelEmitter(meter metric.Meter) *otelEmitter {
	return &otelEmitter{meter: meter, gauges: map[string]metric.Float64Gauge{}}
}

// Gauge records value on the named gauge with the given attributes.
func (e *otelEmitter) Gauge(ctx context.Context, name string, value float64, attrs map[string]string) {
	g := e.gaugeFor(name)
	if g == nil {
		return
	}
	g.Record(ctx, value, metric.WithAttributes(keyValues(attrs)...))
}

// gaugeFor returns a cached gauge for name, creating it on first use.
func (e *otelEmitter) gaugeFor(name string) metric.Float64Gauge {
	e.mu.Lock()
	defer e.mu.Unlock()
	if g, ok := e.gauges[name]; ok {
		return g
	}
	g, err := e.meter.Float64Gauge(name)
	if err != nil {
		log.Printf("emit: create gauge %q: %v", name, err)
		return nil
	}
	e.gauges[name] = g
	return g
}

// keyValues converts a string attribute map to OTel key-values.
func keyValues(attrs map[string]string) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(attrs))
	for k, v := range attrs {
		out = append(out, attribute.String(k, v))
	}
	return out
}
