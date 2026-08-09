package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	pricingtypes "github.com/aws/aws-sdk-go-v2/service/pricing/types"
)

// --- AWS client interfaces -----------------------------------------------------------------
// Each is the exact subset of an SDK client the poller uses; the concrete *ec2.Client,
// *pricing.Client and *costexplorer.Client satisfy them, and tests supply fakes.

// EC2API is the EC2 surface the poller uses to enumerate instances and volumes.
type EC2API interface {
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	DescribeVolumes(context.Context, *ec2.DescribeVolumesInput, ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
}

// PricingAPI is the AWS Pricing surface the poller uses to fetch on-demand rates.
type PricingAPI interface {
	GetProducts(context.Context, *pricing.GetProductsInput, ...func(*pricing.Options)) (*pricing.GetProductsOutput, error)
}

// CostExplorerAPI is the Cost Explorer surface the poller uses for actual daily spend.
type CostExplorerAPI interface {
	GetCostAndUsage(context.Context, *costexplorer.GetCostAndUsageInput, ...func(*costexplorer.Options)) (*costexplorer.GetCostAndUsageOutput, error)
}

// --- Pricer --------------------------------------------------------------------------------

// Pricer fills a PriceBook from the AWS Pricing API, caching each distinct lookup so a steady
// inventory costs at most one API call per new instance/volume type.
type Pricer struct {
	api        PricingAPI
	regionCode string // e.g. "eu-west-1" — the Pricing "regionCode" filter
	book       *PriceBook
	mu         sync.Mutex
}

// NewPricer returns a Pricer that writes into book for the given region.
func NewPricer(api PricingAPI, regionCode string, book *PriceBook) *Pricer {
	return &Pricer{api: api, regionCode: regionCode, book: book}
}

// EnsureInstance makes sure the hourly rate for instanceType is cached.
func (p *Pricer) EnsureInstance(ctx context.Context, instanceType string) error {
	if p.book.HasInstance(instanceType) {
		return nil
	}
	price, err := p.fetch(ctx, "AmazonEC2", []pricingtypes.Filter{
		termMatch("instanceType", instanceType),
		termMatch("regionCode", p.regionCode),
		termMatch("operatingSystem", "Linux"),
		termMatch("tenancy", "Shared"),
		termMatch("preInstalledSw", "NA"),
		termMatch("capacitystatus", "Used"),
	})
	if err != nil {
		return err
	}
	p.book.SetInstanceHourly(instanceType, price)
	return nil
}

// EnsureEBS makes sure the $/GB-month rate for volumeType is cached.
func (p *Pricer) EnsureEBS(ctx context.Context, volumeType string) error {
	if p.book.HasEBS(volumeType) {
		return nil
	}
	price, err := p.fetch(ctx, "AmazonEC2", []pricingtypes.Filter{
		termMatch("productFamily", "Storage"),
		termMatch("volumeApiName", volumeType),
		termMatch("regionCode", p.regionCode),
	})
	if err != nil {
		return err
	}
	p.book.SetEBSGBMonth(volumeType, price)
	return nil
}

// fetch runs one GetProducts and parses the first PriceList document.
func (p *Pricer) fetch(ctx context.Context, service string, filters []pricingtypes.Filter) (float64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out, err := p.api.GetProducts(ctx, &pricing.GetProductsInput{
		ServiceCode: aws.String(service),
		Filters:     filters,
	})
	if err != nil {
		return 0, fmt.Errorf("getproducts %s: %w", service, err)
	}
	if len(out.PriceList) == 0 {
		return 0, fmt.Errorf("getproducts %s: empty price list", service)
	}
	return parseOnDemandUSD(out.PriceList[0])
}

func termMatch(field, value string) pricingtypes.Filter {
	return pricingtypes.Filter{
		Type:  pricingtypes.FilterTypeTermMatch,
		Field: aws.String(field),
		Value: aws.String(value),
	}
}

// --- Poller --------------------------------------------------------------------------------

// Poller owns one estimate cycle and one actual cycle. It holds no timers; main drives it.
type Poller struct {
	ec2        EC2API
	pricer     *Pricer
	ce         CostExplorerAPI
	est        *Estimator
	emit       Emitter
	creds      CredentialsProbe
	actualDays int
}

// metricCredentialsOK is a poller-health gauge (1 ok / 0 failed) that reaches Honeycomb even when
// AWS itself is unreachable — so an IAM/IMDS credential failure is VISIBLE there, not buried in
// pod logs. The cost egress (OTLP -> otel-agent) needs no AWS credentials, so this always flows.
const metricCredentialsOK = "aws.cost.poller.credentials_ok"

// credHint is the operator-facing explanation emitted when AWS credentials can't be obtained.
// It names the exact things to check for the IMDS instance-profile path this app relies on.
const credHint = "AWS credentials unavailable via IMDS instance profile — check the worker has the cost-reader instance profile attached and IMDS http-put-response-hop-limit >= 2 (a pod is one network hop past the host)"

// CredentialsProbe reports whether AWS credentials can currently be retrieved. It is the
// aws.CredentialsProvider interface narrowed to what the poller needs, so tests can inject a
// provider that succeeds or fails without any AWS calls.
type CredentialsProbe interface {
	Retrieve(context.Context) (aws.Credentials, error)
}

// credentialsUsable retrieves credentials once and reports usability, logging a single actionable
// line (not the raw multi-line SDK error) on failure.
func (p *Poller) credentialsUsable(ctx context.Context) bool {
	if _, err := p.creds.Retrieve(ctx); err != nil {
		log.Printf("aws credentials: %s: %v", credHint, err)
		return false
	}
	return true
}

// EmitEstimated builds the live inventory, ensures its prices, and emits estimated daily-cost
// metrics. It first reports credential health to Honeycomb and short-circuits when credentials
// are missing, so an IAM/IMDS failure surfaces as credentials_ok=0 (in Honeycomb) plus one clear
// log line — rather than a per-cycle dump of the underlying SDK error against every doomed call.
func (p *Poller) EmitEstimated(ctx context.Context) error {
	ok := p.credentialsUsable(ctx)
	p.emit.Gauge(ctx, metricCredentialsOK, boolGauge(ok), map[string]string{"provider": "imds"})
	if !ok {
		return nil
	}
	inv, err := p.inventory(ctx)
	if err != nil {
		return err
	}
	p.ensurePrices(ctx, inv)
	for _, s := range p.est.Compute(inv) {
		p.emit.Gauge(ctx, estimatedMetric(s.Metric), s.Value, withCostType(s.Attrs, "estimated"))
	}
	return nil
}

// EmitActual queries Cost Explorer for the last actualDays of daily UnblendedCost, grouped by
// service and instance type, and emits one gauge point per group per day. It skips the (billable)
// Cost Explorer call entirely when credentials are unavailable.
func (p *Poller) EmitActual(ctx context.Context, now time.Time) error {
	if !p.credentialsUsable(ctx) {
		return nil
	}
	end := now.UTC().Truncate(24 * time.Hour) // exclusive end at today 00:00 -> only completed days
	start := end.AddDate(0, 0, -p.actualDays)
	out, err := p.ce.GetCostAndUsage(ctx, &costexplorer.GetCostAndUsageInput{
		Granularity: cetypes.GranularityDaily,
		Metrics:     []string{"UnblendedCost"},
		TimePeriod: &cetypes.DateInterval{
			Start: aws.String(start.Format("2006-01-02")),
			End:   aws.String(end.Format("2006-01-02")),
		},
		GroupBy: []cetypes.GroupDefinition{
			{Type: cetypes.GroupDefinitionTypeDimension, Key: aws.String("SERVICE")},
			{Type: cetypes.GroupDefinitionTypeDimension, Key: aws.String("INSTANCE_TYPE")},
		},
	})
	if err != nil {
		return fmt.Errorf("get cost and usage: %w", err)
	}
	for _, rec := range costRecords(out, "UnblendedCost", []string{"service", "instance_type"}) {
		attrs := map[string]string{"usage_date": rec.Date}
		for k, v := range rec.Groups {
			attrs[k] = v
		}
		p.emit.Gauge(ctx, "aws.cost.actual.daily.usd", rec.Amount, withCostType(attrs, "actual"))
	}
	return nil
}

// inventory enumerates every instance in the account/region (k8s node or not, running or
// stopped) and joins each instance's attached EBS volumes.
func (p *Poller) inventory(ctx context.Context) ([]ComputeResource, error) {
	di, err := p.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{})
	if err != nil {
		return nil, fmt.Errorf("describe instances: %w", err)
	}
	dv, err := p.ec2.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{})
	if err != nil {
		return nil, fmt.Errorf("describe volumes: %w", err)
	}
	volsByInstance := volumesByInstance(dv)

	var out []ComputeResource
	for _, r := range di.Reservations {
		for _, inst := range r.Instances {
			id := aws.ToString(inst.InstanceId)
			state := ""
			if inst.State != nil {
				state = string(inst.State.Name)
			}
			out = append(out, ComputeResource{
				InstanceID:   id,
				Name:         tagValue(inst.Tags, "Name"),
				InstanceType: string(inst.InstanceType),
				Kind:         tagValue(inst.Tags, "Kind"),
				State:        state,
				Running:      state == string(ec2types.InstanceStateNameRunning),
				HasPublicIP:  aws.ToString(inst.PublicIpAddress) != "",
				Volumes:      volsByInstance[id],
			})
		}
	}
	return out, nil
}

// ensurePrices fetches (and caches) the rate for every distinct instance/volume type in inv.
func (p *Poller) ensurePrices(ctx context.Context, inv []ComputeResource) {
	for _, in := range inv {
		if err := p.pricer.EnsureInstance(ctx, in.InstanceType); err != nil {
			log.Printf("price instance %s: %v", in.InstanceType, err)
		}
		for _, v := range in.Volumes {
			if err := p.pricer.EnsureEBS(ctx, v.VolumeType); err != nil {
				log.Printf("price ebs %s: %v", v.VolumeType, err)
			}
		}
	}
}

// --- pure translation helpers --------------------------------------------------------------

// CostRecord is one grouped daily actual-cost line from Cost Explorer.
type CostRecord struct {
	Date   string            // YYYY-MM-DD (period start)
	Groups map[string]string // group attribute name -> value
	Amount float64           // USD
	Unit   string
}

// volumesByInstance maps instance ID -> its attached EBS volumes.
func volumesByInstance(dv *ec2.DescribeVolumesOutput) map[string][]StorageResource {
	m := map[string][]StorageResource{}
	if dv == nil {
		return m
	}
	for _, v := range dv.Volumes {
		sr := StorageResource{
			VolumeID:   aws.ToString(v.VolumeId),
			VolumeType: string(v.VolumeType),
			SizeGiB:    int64(aws.ToInt32(v.Size)),
		}
		for _, a := range v.Attachments {
			if id := aws.ToString(a.InstanceId); id != "" {
				m[id] = append(m[id], sr)
			}
		}
	}
	return m
}

// tagValue returns the value of the named tag, or "" if absent.
func tagValue(tags []ec2types.Tag, key string) string {
	for _, t := range tags {
		if aws.ToString(t.Key) == key {
			return aws.ToString(t.Value)
		}
	}
	return ""
}

// costRecords flattens a GetCostAndUsage response into records. groupKeys names the GroupBy
// dimensions in order, so g.Keys[i] is labeled groupKeys[i].
func costRecords(out *costexplorer.GetCostAndUsageOutput, metric string, groupKeys []string) []CostRecord {
	if out == nil {
		return nil
	}
	var recs []CostRecord
	for _, byTime := range out.ResultsByTime {
		date := ""
		if byTime.TimePeriod != nil {
			date = aws.ToString(byTime.TimePeriod.Start)
		}
		for _, g := range byTime.Groups {
			mv, ok := g.Metrics[metric]
			if !ok {
				continue
			}
			amt, _ := strconv.ParseFloat(aws.ToString(mv.Amount), 64)
			groups := map[string]string{}
			for i, key := range g.Keys {
				if i < len(groupKeys) {
					groups[groupKeys[i]] = key
				}
			}
			recs = append(recs, CostRecord{Date: date, Groups: groups, Amount: amt, Unit: aws.ToString(mv.Unit)})
		}
	}
	return recs
}

// estimatedMetric maps a sample metric suffix to its full estimated metric name.
func estimatedMetric(suffix string) string { return "aws.cost.estimated." + suffix + ".usd" }

// boolGauge maps a health boolean to a gauge value (1 ok / 0 failed).
func boolGauge(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// withCostType returns attrs plus a cost.type label (attrs is not mutated).
func withCostType(attrs map[string]string, t string) map[string]string {
	out := make(map[string]string, len(attrs)+1)
	for k, v := range attrs {
		out[k] = v
	}
	out["cost.type"] = t
	return out
}
