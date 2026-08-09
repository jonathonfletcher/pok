package main

import "strconv"

// hoursPerMonth converts a $/GB-month EBS rate to hourly; hoursPerDay scales an hourly rate to a
// full day so the estimated metric shares the ACTUAL metric's daily unit (aws.cost.*.daily.usd),
// making the two directly comparable.
const (
	hoursPerMonth = 730.0
	hoursPerDay   = 24.0
)

// cost.component attribute values.
const (
	componentCompute    = "compute"
	componentStorage    = "storage"
	componentPublicIPv4 = "public_ipv4"
)

// metricDaily is the sample's metric suffix; estimatedMetric() expands it to the full metric name
// aws.cost.estimated.daily.usd.
const metricDaily = "daily"

// StorageResource is one EBS volume attached to an instance.
type StorageResource struct {
	VolumeID   string
	VolumeType string // gp3, gp2, io2, ...
	SizeGiB    int64
}

// ComputeResource is one EC2 instance (k8s node or not) plus the storage and public IP it is
// billed for. State is carried so a stopped instance still surfaces its (EBS/EIP) cost.
type ComputeResource struct {
	InstanceID   string
	Name         string // the Name tag ("demo-worker-01"), for readable dashboards
	InstanceType string // t4g.medium, ...
	Kind         string // control-plane|worker|border (Kind tag; "" -> "unknown")
	State        string // running|stopped|...
	Running      bool
	HasPublicIP  bool
	Volumes      []StorageResource
}

// Sample is one metric point the estimator wants emitted.
type Sample struct {
	Metric string  // metricDaily
	Value  float64 // USD per day
	Attrs  map[string]string
}

// PriceSource yields the rates the estimator needs. An interface so the estimator runs against
// the live AWS Pricing API in production and a static book in tests.
type PriceSource interface {
	InstanceHourly(instanceType string) (float64, bool)
	EBSGBMonth(volumeType string) (float64, bool)
	PublicIPv4Hourly() float64
}

// Estimator turns a live inventory + a price source into estimated DAILY cost samples
// (aws.cost.estimated.daily.usd), one per instance and cost component (compute / storage /
// public_ipv4). The daily figure — the current run-rate projected over 24h — shares the unit of
// the ACTUAL daily cost from Cost Explorer, so the two compare directly. It performs no I/O;
// Compute is a pure, deterministic function of its inputs, which is what the tests exercise.
type Estimator struct {
	prices PriceSource
}

// NewEstimator returns an Estimator backed by prices.
func NewEstimator(prices PriceSource) *Estimator {
	return &Estimator{prices: prices}
}

// Compute returns the estimated daily-cost samples for the given inventory.
func (e *Estimator) Compute(instances []ComputeResource) []Sample {
	var out []Sample
	for _, in := range instances {
		base := map[string]string{
			"instance_id": in.InstanceID,
			"name":        orUnknown(in.Name),
			"kind":        orUnknown(in.Kind),
			"state":       orUnknown(in.State),
		}
		out = append(out, e.compute(in, base)...)
		out = append(out, e.storage(in, base)...)
		out = append(out, e.publicIPv4(in, base)...)
	}
	return out
}

// compute emits the instance's daily compute cost. A stopped instance emits 0 (it exists but
// isn't billing for compute); a running instance is skipped only if its rate is unknown.
func (e *Estimator) compute(in ComputeResource, base map[string]string) []Sample {
	hourly := 0.0
	if in.Running {
		rate, ok := e.prices.InstanceHourly(in.InstanceType)
		if !ok {
			return nil // running but unpriced — can't estimate, so don't guess
		}
		hourly = rate
	}
	return []Sample{daily(hourly, mergeAttrs(base, map[string]string{
		"cost.component": componentCompute,
		"instance_type":  in.InstanceType,
	}))}
}

// storage emits one daily-cost sample per attached EBS volume. EBS bills regardless of instance
// state, so this is emitted whether the instance is running or stopped.
func (e *Estimator) storage(in ComputeResource, base map[string]string) []Sample {
	var out []Sample
	for _, v := range in.Volumes {
		gbMonth, ok := e.prices.EBSGBMonth(v.VolumeType)
		if !ok {
			continue
		}
		hourly := gbMonth * float64(v.SizeGiB) / hoursPerMonth
		out = append(out, daily(hourly, mergeAttrs(base, map[string]string{
			"cost.component": componentStorage,
			"volume_id":      v.VolumeID,
			"volume_type":    v.VolumeType,
			"size_gib":       strconv.FormatInt(v.SizeGiB, 10),
		})))
	}
	return out
}

// publicIPv4 emits the instance's daily public-IPv4 cost when it carries a public address.
func (e *Estimator) publicIPv4(in ComputeResource, base map[string]string) []Sample {
	if !in.HasPublicIP {
		return nil
	}
	return []Sample{daily(e.prices.PublicIPv4Hourly(), mergeAttrs(base, map[string]string{
		"cost.component": componentPublicIPv4,
	}))}
}

// daily scales an hourly rate to a full day and packages it as a daily-metric sample.
func daily(hourly float64, attrs map[string]string) Sample {
	return Sample{Metric: metricDaily, Value: hourly * hoursPerDay, Attrs: attrs}
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// mergeAttrs returns base overlaid with extra (neither input is mutated).
func mergeAttrs(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}
