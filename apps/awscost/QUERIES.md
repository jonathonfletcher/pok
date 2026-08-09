# awscost — Honeycomb queries

Metrics (env `k8s`, dataset `metrics`):

- `aws.cost.estimated.daily.usd` — estimated USD/day. Attrs: `instance_id`, `name`, `kind`, `state`, `cost.component` (compute/storage/public_ipv4), `instance_type`, `volume_id`, `volume_type`, `size_gib`.
- `aws.cost.actual.daily.usd` — actual USD/day (Cost Explorer). Attrs: `service`, `instance_type`, `usage_date`.
- `aws.cost.poller.credentials_ok` — 1 ok / 0 fail. Attrs: `provider`, `k8s.pod.name`.

## Rule: these are gauges — use MAX, never SUM

`SUM` adds datapoints *over time*, so it over-counts by the number of samples in the window (and, for actual, by day — it restates each day on every poll). `MAX` is stable regardless of granularity.

| Visualize | Calc | Group by | Chart |
|---|---|---|---|
| Estimated total + breakdown | `MAX(aws.cost.estimated.daily.usd)` | `name`, `cost.component` | stacked (segments stack to the fleet total) |
| Actual by service (per day) | `MAX(aws.cost.actual.daily.usd)` | `service` | categorical_bar |
| Actual compute by type | `MAX(aws.cost.actual.daily.usd)`, filter `service = "Amazon Elastic Compute Cloud - Compute"` | `instance_type` | categorical_bar |
| Poller health | `MAX(aws.cost.poller.credentials_ok)` | `k8s.pod.name` | line |

Add `kind` / `instance_type` to the estimated group-by to slice by role or type.

## Gotchas

- `MAX` on a **coarse** group returns the max single series, not the sum. For a total, group to the instance level (`name`, `cost.component`) and **stack**.
- Do **not** group `actual` by `usage_date` — it renders per-day series over the time axis. `MAX` by `service` already yields the daily value.
- `actual` `instance_type = NoInstanceType` is not an instance — it's Cost Explorer's bucket for non-compute lines (EBS, VPC/public-IPv4, CloudWatch).
- Estimated and actual should track closely per day; the residual gap is CloudWatch detailed monitoring, which the estimate does not cover.
