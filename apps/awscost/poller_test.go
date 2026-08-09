package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
)

// --- fakes ---------------------------------------------------------------------------------

type fakeEC2 struct {
	instances     *ec2.DescribeInstancesOutput
	volumes       *ec2.DescribeVolumesOutput
	err           error
	describeCalls int
}

func (f *fakeEC2) DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	f.describeCalls++
	return f.instances, f.err
}

func (f *fakeEC2) DescribeVolumes(context.Context, *ec2.DescribeVolumesInput, ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	return f.volumes, f.err
}

type fakePricing struct{ calls int }

func (f *fakePricing) GetProducts(_ context.Context, in *pricing.GetProductsInput, _ ...func(*pricing.Options)) (*pricing.GetProductsOutput, error) {
	f.calls++
	for _, fl := range in.Filters {
		switch aws.ToString(fl.Field) {
		case "instanceType":
			return &pricing.GetProductsOutput{PriceList: []string{instanceDoc}}, nil
		case "volumeApiName":
			return &pricing.GetProductsOutput{PriceList: []string{ebsDoc}}, nil
		}
	}
	return &pricing.GetProductsOutput{}, nil
}

type fakeCE struct {
	out       *costexplorer.GetCostAndUsageOutput
	err       error
	lastInput *costexplorer.GetCostAndUsageInput
	calls     int
}

func (f *fakeCE) GetCostAndUsage(_ context.Context, in *costexplorer.GetCostAndUsageInput, _ ...func(*costexplorer.Options)) (*costexplorer.GetCostAndUsageOutput, error) {
	f.calls++
	f.lastInput = in
	return f.out, f.err
}

// fakeCreds implements CredentialsProbe: it succeeds unless err is set.
type fakeCreds struct{ err error }

func (f fakeCreds) Retrieve(context.Context) (aws.Credentials, error) {
	if f.err != nil {
		return aws.Credentials{}, f.err
	}
	return aws.Credentials{AccessKeyID: "AKIATEST", SecretAccessKey: "secret", Source: "fake"}, nil
}

type recordedGauge struct {
	Name  string
	Value float64
	Attrs map[string]string
}

type recordingEmitter struct{ calls []recordedGauge }

func (r *recordingEmitter) Gauge(_ context.Context, name string, v float64, attrs map[string]string) {
	r.calls = append(r.calls, recordedGauge{Name: name, Value: v, Attrs: attrs})
}

// findGauge returns the first recorded gauge with the given name matching pred.
func (r *recordingEmitter) findGauge(name string, pred func(map[string]string) bool) (recordedGauge, bool) {
	for _, c := range r.calls {
		if c.Name == name && (pred == nil || pred(c.Attrs)) {
			return c, true
		}
	}
	return recordedGauge{}, false
}

// --- helpers to build fake AWS output ------------------------------------------------------

func runningWorker(launch time.Time) ec2types.Instance {
	return ec2types.Instance{
		InstanceId:      aws.String("i-worker1"),
		InstanceType:    ec2types.InstanceType("t4g.medium"),
		State:           &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
		LaunchTime:      aws.Time(launch),
		PublicIpAddress: aws.String("1.2.3.4"),
		Tags: []ec2types.Tag{
			{Key: aws.String("Kind"), Value: aws.String("worker")},
			{Key: aws.String("Name"), Value: aws.String("demo-worker-01")},
		},
	}
}

func newTestPoller(ec2c EC2API, ce CostExplorerAPI, emit Emitter) *Poller {
	book := NewPriceBook(0.005)
	return &Poller{
		ec2:        ec2c,
		pricer:     NewPricer(&fakePricing{}, "eu-west-1", book),
		ce:         ce,
		est:        NewEstimator(book),
		emit:       emit,
		creds:      fakeCreds{}, // usable by default; tests override with fakeCreds{err: ...}
		actualDays: 2,
	}
}

// --- tests ---------------------------------------------------------------------------------

func TestInventoryJoinsVolumesTagsAndState(t *testing.T) {
	launch := time.Now().Add(-time.Hour)
	ec2c := &fakeEC2{
		instances: &ec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{
			{Instances: []ec2types.Instance{runningWorker(launch)}},
		}},
		volumes: &ec2.DescribeVolumesOutput{Volumes: []ec2types.Volume{
			{
				VolumeId:    aws.String("vol-root"),
				VolumeType:  ec2types.VolumeType("gp3"),
				Size:        aws.Int32(32),
				Attachments: []ec2types.VolumeAttachment{{InstanceId: aws.String("i-worker1")}},
			},
		}},
	}
	p := newTestPoller(ec2c, &fakeCE{}, &recordingEmitter{})

	inv, err := p.inventory(context.Background())
	if err != nil {
		t.Fatalf("inventory: %v", err)
	}
	if len(inv) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(inv))
	}
	in := inv[0]
	if in.InstanceID != "i-worker1" || in.InstanceType != "t4g.medium" || in.Kind != "worker" ||
		in.Name != "demo-worker-01" || in.State != "running" || !in.Running || !in.HasPublicIP {
		t.Fatalf("instance fields wrong: %+v", in)
	}
	if len(in.Volumes) != 1 || in.Volumes[0].VolumeID != "vol-root" || in.Volumes[0].SizeGiB != 32 {
		t.Fatalf("volumes not joined: %+v", in.Volumes)
	}
}

func TestEmitEstimatedEndToEnd(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	ec2c := &fakeEC2{
		instances: &ec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{
			{Instances: []ec2types.Instance{runningWorker(now.Add(-2 * time.Hour))}},
		}},
		volumes: &ec2.DescribeVolumesOutput{Volumes: []ec2types.Volume{
			{
				VolumeId:    aws.String("vol-root"),
				VolumeType:  ec2types.VolumeType("gp3"),
				Size:        aws.Int32(32),
				Attachments: []ec2types.VolumeAttachment{{InstanceId: aws.String("i-worker1")}},
			},
		}},
	}
	rec := &recordingEmitter{}
	p := newTestPoller(ec2c, &fakeCE{}, rec)

	if err := p.EmitEstimated(context.Background()); err != nil {
		t.Fatalf("EmitEstimated: %v", err)
	}

	// compute rate gauge, per instance, tagged estimated.
	g, ok := rec.findGauge("aws.cost.estimated.daily.usd", func(a map[string]string) bool {
		return a["cost.component"] == componentCompute && a["instance_id"] == "i-worker1"
	})
	if !ok {
		t.Fatal("no estimated compute rate gauge emitted")
	}
	if !approx(g.Value, 0.0336*hoursPerDay) {
		t.Fatalf("compute daily = %v, want %v", g.Value, 0.0336*hoursPerDay)
	}
	if g.Attrs["cost.type"] != "estimated" || g.Attrs["instance_type"] != "t4g.medium" || g.Attrs["kind"] != "worker" {
		t.Fatalf("compute rate attrs wrong: %+v", g.Attrs)
	}

	// storage rate gauge carries size + volume descriptors.
	if _, ok := rec.findGauge("aws.cost.estimated.daily.usd", func(a map[string]string) bool {
		return a["cost.component"] == componentStorage && a["size_gib"] == "32" && a["volume_type"] == "gp3"
	}); !ok {
		t.Fatal("no estimated storage rate gauge with descriptors emitted")
	}

	// public IPv4 gauge present.
	if _, ok := rec.findGauge("aws.cost.estimated.daily.usd", func(a map[string]string) bool {
		return a["cost.component"] == componentPublicIPv4
	}); !ok {
		t.Fatal("no estimated public_ipv4 rate gauge emitted")
	}

	// prices were actually fetched via the Pricing API (instance + ebs = 2 calls).
	if fp := p.pricer.api.(*fakePricing); fp.calls != 2 {
		t.Fatalf("pricing calls = %d, want 2", fp.calls)
	}

	// with usable credentials, the health gauge reports 1.
	if g, ok := rec.findGauge(metricCredentialsOK, nil); !ok || g.Value != 1 {
		t.Fatalf("credentials_ok = %v (found=%v), want 1", g.Value, ok)
	}
}

func TestEmitEstimatedSkipsAndReportsWhenNoCredentials(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	ec2c := &fakeEC2{
		instances: &ec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{
			{Instances: []ec2types.Instance{runningWorker(now.Add(-time.Hour))}},
		}},
	}
	rec := &recordingEmitter{}
	p := newTestPoller(ec2c, &fakeCE{}, rec)
	p.creds = fakeCreds{err: errors.New("no EC2 IMDS role found")}

	if err := p.EmitEstimated(context.Background()); err != nil {
		t.Fatalf("EmitEstimated should not return an error on missing creds, got %v", err)
	}
	// health gauge reports 0 (this reaches Honeycomb even without AWS creds).
	if g, ok := rec.findGauge(metricCredentialsOK, nil); !ok || g.Value != 0 {
		t.Fatalf("credentials_ok = %v (found=%v), want 0", g.Value, ok)
	}
	// no cost samples emitted, and the doomed EC2 call was never made.
	if _, ok := rec.findGauge("aws.cost.estimated.daily.usd", nil); ok {
		t.Fatal("must not emit cost metrics when credentials are unavailable")
	}
	if ec2c.describeCalls != 0 {
		t.Fatalf("EC2 DescribeInstances called %d times; must be skipped without creds", ec2c.describeCalls)
	}
}

func TestEmitActualSkipsWhenNoCredentials(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	ce := &fakeCE{out: &costexplorer.GetCostAndUsageOutput{}}
	p := newTestPoller(&fakeEC2{}, ce, &recordingEmitter{})
	p.creds = fakeCreds{err: errors.New("no EC2 IMDS role found")}

	if err := p.EmitActual(context.Background(), now); err != nil {
		t.Fatalf("EmitActual should not return an error on missing creds, got %v", err)
	}
	if ce.calls != 0 {
		t.Fatalf("Cost Explorer called %d times; the billable call must be skipped without creds", ce.calls)
	}
}

func TestEmitActualEndToEnd(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	ce := &fakeCE{out: &costexplorer.GetCostAndUsageOutput{
		ResultsByTime: []cetypes.ResultByTime{
			{
				TimePeriod: &cetypes.DateInterval{Start: aws.String("2026-08-08"), End: aws.String("2026-08-09")},
				Groups: []cetypes.Group{
					{
						Keys: []string{"Amazon Elastic Compute Cloud - Compute", "t4g.medium"},
						Metrics: map[string]cetypes.MetricValue{
							"UnblendedCost": {Amount: aws.String("1.23"), Unit: aws.String("USD")},
						},
					},
				},
			},
		},
	}}
	rec := &recordingEmitter{}
	p := newTestPoller(&fakeEC2{}, ce, rec)

	if err := p.EmitActual(context.Background(), now); err != nil {
		t.Fatalf("EmitActual: %v", err)
	}

	g, ok := rec.findGauge("aws.cost.actual.daily.usd", nil)
	if !ok {
		t.Fatal("no actual daily gauge emitted")
	}
	if g.Value != 1.23 {
		t.Fatalf("actual amount = %v, want 1.23", g.Value)
	}
	if g.Attrs["cost.type"] != "actual" || g.Attrs["service"] != "Amazon Elastic Compute Cloud - Compute" ||
		g.Attrs["instance_type"] != "t4g.medium" || g.Attrs["usage_date"] != "2026-08-08" {
		t.Fatalf("actual attrs wrong: %+v", g.Attrs)
	}

	// the query window is [now-2d, now) truncated to whole UTC days, DAILY granularity.
	in := ce.lastInput
	if in.Granularity != cetypes.GranularityDaily {
		t.Fatalf("granularity = %v, want DAILY", in.Granularity)
	}
	if aws.ToString(in.TimePeriod.Start) != "2026-08-07" || aws.ToString(in.TimePeriod.End) != "2026-08-09" {
		t.Fatalf("time period = %s..%s, want 2026-08-07..2026-08-09",
			aws.ToString(in.TimePeriod.Start), aws.ToString(in.TimePeriod.End))
	}
}

func TestCostRecords(t *testing.T) {
	out := &costexplorer.GetCostAndUsageOutput{ResultsByTime: []cetypes.ResultByTime{
		{
			TimePeriod: &cetypes.DateInterval{Start: aws.String("2026-08-08")},
			Groups: []cetypes.Group{
				{Keys: []string{"EC2", "t4g.micro"}, Metrics: map[string]cetypes.MetricValue{
					"UnblendedCost": {Amount: aws.String("0.20"), Unit: aws.String("USD")}}},
				{Keys: []string{"EBS", "NoInstanceType"}, Metrics: map[string]cetypes.MetricValue{
					"UnblendedCost": {Amount: aws.String("0.05"), Unit: aws.String("USD")}}},
			},
		},
	}}
	recs := costRecords(out, "UnblendedCost", []string{"service", "instance_type"})
	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}
	if recs[0].Date != "2026-08-08" || recs[0].Groups["service"] != "EC2" ||
		recs[0].Groups["instance_type"] != "t4g.micro" || recs[0].Amount != 0.20 {
		t.Fatalf("record 0 wrong: %+v", recs[0])
	}
}

func TestVolumesByInstanceAndTagValue(t *testing.T) {
	dv := &ec2.DescribeVolumesOutput{Volumes: []ec2types.Volume{
		{
			VolumeId:   aws.String("vol-1"),
			VolumeType: ec2types.VolumeType("gp3"),
			Size:       aws.Int32(20),
			Attachments: []ec2types.VolumeAttachment{
				{InstanceId: aws.String("i-a")},
				{InstanceId: aws.String("i-b")},
			},
		},
	}}
	m := volumesByInstance(dv)
	if len(m["i-a"]) != 1 || len(m["i-b"]) != 1 || m["i-a"][0].SizeGiB != 20 {
		t.Fatalf("volumesByInstance wrong: %+v", m)
	}

	tags := []ec2types.Tag{{Key: aws.String("Kind"), Value: aws.String("border")}}
	if tagValue(tags, "Kind") != "border" || tagValue(tags, "Missing") != "" {
		t.Fatal("tagValue wrong")
	}
}
