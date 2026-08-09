package main

import (
	"math"
	"testing"
)

// stubPrices is a static PriceSource for estimator tests.
type stubPrices struct {
	instance map[string]float64
	ebs      map[string]float64
	ipv4     float64
}

func (s stubPrices) InstanceHourly(t string) (float64, bool) { v, ok := s.instance[t]; return v, ok }
func (s stubPrices) EBSGBMonth(t string) (float64, bool)     { v, ok := s.ebs[t]; return v, ok }
func (s stubPrices) PublicIPv4Hourly() float64               { return s.ipv4 }

func testPrices() stubPrices {
	return stubPrices{
		instance: map[string]float64{"t4g.medium": 0.0336, "t4g.micro": 0.0084},
		ebs:      map[string]float64{"gp3": 0.088}, // $/GB-month (eu-west-1)
		ipv4:     0.005,
	}
}

// find returns the first sample matching component + instanceID.
func find(samples []Sample, component, instanceID string) (Sample, bool) {
	for _, s := range samples {
		if s.Attrs["cost.component"] == component && s.Attrs["instance_id"] == instanceID {
			return s, true
		}
	}
	return Sample{}, false
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestComputeDailyAllComponents(t *testing.T) {
	inv := []ComputeResource{{
		InstanceID:   "i-worker1",
		Name:         "demo-worker-01",
		InstanceType: "t4g.medium",
		Kind:         "worker",
		State:        "running",
		Running:      true,
		HasPublicIP:  true,
		Volumes: []StorageResource{
			{VolumeID: "vol-root", VolumeType: "gp3", SizeGiB: 32},
			{VolumeID: "vol-data", VolumeType: "gp3", SizeGiB: 20},
		},
	}}

	got := NewEstimator(testPrices()).Compute(inv)

	// every sample is the daily metric.
	for _, s := range got {
		if s.Metric != metricDaily {
			t.Fatalf("sample metric = %q, want %q", s.Metric, metricDaily)
		}
	}

	// compute daily = hourly * 24
	c, ok := find(got, componentCompute, "i-worker1")
	if !ok || !approx(c.Value, 0.0336*hoursPerDay) {
		t.Fatalf("compute daily = %v (found=%v), want %v", c.Value, ok, 0.0336*hoursPerDay)
	}
	if c.Attrs["instance_type"] != "t4g.medium" || c.Attrs["kind"] != "worker" ||
		c.Attrs["name"] != "demo-worker-01" || c.Attrs["state"] != "running" {
		t.Fatalf("compute attrs missing descriptors: %+v", c.Attrs)
	}

	// storage: two volumes; verify the 32GiB root's daily = $/GB-mo * size / 730 * 24, incl. descriptors.
	rootDaily := 0.088 * 32 / hoursPerMonth * hoursPerDay
	var storCount int
	var foundRoot bool
	for _, s := range got {
		if s.Attrs["cost.component"] != componentStorage {
			continue
		}
		storCount++
		if s.Attrs["volume_id"] == "vol-root" {
			foundRoot = true
			if !approx(s.Value, rootDaily) {
				t.Fatalf("root storage daily = %v, want %v", s.Value, rootDaily)
			}
			if s.Attrs["volume_type"] != "gp3" || s.Attrs["size_gib"] != "32" {
				t.Fatalf("root storage attrs wrong: %+v", s.Attrs)
			}
		}
	}
	if storCount != 2 || !foundRoot {
		t.Fatalf("expected 2 storage samples incl root, got count=%d foundRoot=%v", storCount, foundRoot)
	}

	// public IPv4 daily = 0.005 * 24
	ip, ok := find(got, componentPublicIPv4, "i-worker1")
	if !ok || !approx(ip.Value, 0.005*hoursPerDay) {
		t.Fatalf("public_ipv4 daily = %v (found=%v), want %v", ip.Value, ok, 0.005*hoursPerDay)
	}
}

func TestComputeStoppedInstanceStillBillsStorageNotCompute(t *testing.T) {
	inv := []ComputeResource{{
		InstanceID:   "i-stopped",
		InstanceType: "t4g.medium",
		Kind:         "worker",
		State:        "stopped",
		Running:      false,
		HasPublicIP:  false, // auto-assigned IP is released when stopped
		Volumes:      []StorageResource{{VolumeID: "vol-root", VolumeType: "gp3", SizeGiB: 32}},
	}}

	got := NewEstimator(testPrices()).Compute(inv)

	// compute daily must be 0 for a stopped instance (not billing compute), but present.
	c, ok := find(got, componentCompute, "i-stopped")
	if !ok || c.Value != 0 {
		t.Fatalf("stopped compute daily = %v (found=%v), want 0", c.Value, ok)
	}
	// storage STILL bills while stopped.
	s, ok := find(got, componentStorage, "i-stopped")
	if !ok || !approx(s.Value, 0.088*32/hoursPerMonth*hoursPerDay) {
		t.Fatalf("stopped storage daily = %v (found=%v), want billed", s.Value, ok)
	}
	// no public IPv4 sample when the instance has no public IP.
	if _, ok := find(got, componentPublicIPv4, "i-stopped"); ok {
		t.Fatal("did not expect a public_ipv4 sample for an instance without a public IP")
	}
}

func TestComputeRunningButUnpricedInstanceSkipsCompute(t *testing.T) {
	inv := []ComputeResource{{
		InstanceID:   "i-exotic",
		InstanceType: "x9.enormous", // not in the price book
		Kind:         "worker",
		State:        "running",
		Running:      true,
	}}
	got := NewEstimator(testPrices()).Compute(inv)
	if _, ok := find(got, componentCompute, "i-exotic"); ok {
		t.Fatal("running but unpriced instance must not emit a compute estimate")
	}
}

func TestComputeUnknownKindAndNameNormalized(t *testing.T) {
	inv := []ComputeResource{{
		InstanceID:   "i-bare",
		InstanceType: "t4g.micro",
		State:        "running",
		Running:      true,
	}}
	got := NewEstimator(testPrices()).Compute(inv)
	c, ok := find(got, componentCompute, "i-bare")
	if !ok {
		t.Fatal("expected a compute sample")
	}
	if c.Attrs["kind"] != "unknown" || c.Attrs["name"] != "unknown" {
		t.Fatalf("empty kind/name must normalize to 'unknown', got %+v", c.Attrs)
	}
}
