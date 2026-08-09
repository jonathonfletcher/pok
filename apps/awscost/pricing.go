package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
)

// PriceBook is a concurrency-safe in-memory cache of rates. It implements PriceSource, so the
// estimator reads from it directly while the Pricer fills it lazily from the AWS Pricing API.
type PriceBook struct {
	mu         sync.RWMutex
	instance   map[string]float64 // instanceType -> USD/hr
	ebs        map[string]float64 // volumeType   -> USD/GB-month
	ipv4Hourly float64            // flat USD/hr per public IPv4 address
}

// NewPriceBook returns an empty book with the given flat public-IPv4 hourly rate.
func NewPriceBook(ipv4Hourly float64) *PriceBook {
	return &PriceBook{
		instance:   map[string]float64{},
		ebs:        map[string]float64{},
		ipv4Hourly: ipv4Hourly,
	}
}

// InstanceHourly returns the cached hourly rate for an instance type.
func (b *PriceBook) InstanceHourly(t string) (float64, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	v, ok := b.instance[t]
	return v, ok
}

// EBSGBMonth returns the cached $/GB-month rate for a volume type.
func (b *PriceBook) EBSGBMonth(t string) (float64, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	v, ok := b.ebs[t]
	return v, ok
}

// PublicIPv4Hourly returns the flat per-address hourly rate.
func (b *PriceBook) PublicIPv4Hourly() float64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.ipv4Hourly
}

// SetInstanceHourly records an instance-type rate.
func (b *PriceBook) SetInstanceHourly(t string, v float64) {
	b.mu.Lock()
	b.instance[t] = v
	b.mu.Unlock()
}

// SetEBSGBMonth records a volume-type rate.
func (b *PriceBook) SetEBSGBMonth(t string, v float64) {
	b.mu.Lock()
	b.ebs[t] = v
	b.mu.Unlock()
}

// HasInstance reports whether an instance-type rate is cached.
func (b *PriceBook) HasInstance(t string) bool {
	_, ok := b.InstanceHourly(t)
	return ok
}

// HasEBS reports whether a volume-type rate is cached.
func (b *PriceBook) HasEBS(t string) bool {
	_, ok := b.EBSGBMonth(t)
	return ok
}

// parseOnDemandUSD extracts the single on-demand USD unit price from one AWS Pricing API
// PriceList document. GetProducts returns PriceList as a slice of JSON strings, each shaped:
//
//	{"terms":{"OnDemand":{"<offer>":{"priceDimensions":{"<dim>":{"pricePerUnit":{"USD":"0.0432"}}}}}}}
//
// A well-filtered query yields exactly one offer with one dimension, so the first non-empty USD
// value is the answer.
func parseOnDemandUSD(doc string) (float64, error) {
	var p struct {
		Terms struct {
			OnDemand map[string]struct {
				PriceDimensions map[string]struct {
					PricePerUnit struct {
						USD string `json:"USD"`
					} `json:"pricePerUnit"`
				} `json:"priceDimensions"`
			} `json:"OnDemand"`
		} `json:"terms"`
	}
	if err := json.Unmarshal([]byte(doc), &p); err != nil {
		return 0, fmt.Errorf("pricing: decode document: %w", err)
	}
	for _, offer := range p.Terms.OnDemand {
		for _, dim := range offer.PriceDimensions {
			if dim.PricePerUnit.USD == "" {
				continue
			}
			f, err := strconv.ParseFloat(dim.PricePerUnit.USD, 64)
			if err != nil {
				return 0, fmt.Errorf("pricing: parse USD %q: %w", dim.PricePerUnit.USD, err)
			}
			return f, nil
		}
	}
	return 0, fmt.Errorf("pricing: no on-demand USD price in document")
}
