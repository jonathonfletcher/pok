# Runbook — edge error rate / front-door 4xx-5xx

**Service:** `edge` (the public reverse proxy for `pok.somegroup.net` / `dev.somegroup.net`).
**Alert:** Honeycomb trigger **`edge — server error spans`** (env `k8s`, dataset `edge`) — fires
when edge emits spans with OTel `status_code = 2` (Error; otelhttp sets this on 5xx). Pairs with
the board **Live-feed — edge overview** and (optionally) an edge availability SLO.

## Impact
`edge` is the single front door; every `/`, `/static/*`, `/hw/*`, `/ws` request transits it.
Sustained 5xx = users can't load the feed. 4xx spikes are usually external noise (scanners,
crawlers) but can also mean a broken route or a dropped upstream.

## Triage (Honeycomb `k8s` env, `edge` dataset)

1. **Confirm + scope.** Is it 5xx (server fault) or 4xx (client/scan)? Break COUNT down by
   `http.response.status_code` filtered to `span.kind = server`, last 24h.
2. **Localize the endpoint.** Add `url.path` to the breakdown (+ `P95(duration_ms)`). A single
   path = a broken route or a targeted probe; broad = an upstream/systemic issue.
3. **Localize the source (4xx).** Break the 404/4xx down by `client.address` +
   `user_agent.original`. One IP with rotating/spoofed browser UAs = a scanner; declared bots
   (ClaudeBot, GPTBot, MetaCrawler) hitting `/robots.txt` are benign.
4. **For 5xx — find the cause.** `run_bubbleup` on the error spans vs baseline to surface the
   distinguishing attribute (pod, node, upstream, deploy). Then pull a trace (`get_trace`) of an
   example error span — edge propagates context to upstreams, so the trace shows which upstream
   (`static`/`wsfeed`/`helloworld`) failed.
5. **Correlate with the platform.** `kubectl -n app-livefeed get pods` (edge + `static`/`wsfeed`
   Running? recent restarts?; `helloworld` is in `app-helloworld`); `kubectl top pods`
   (OOM/pressure); check the Kafka path if the failing route is `/hw` (visits) or `/ws` (feed).

## Remediation
- **Bad upstream:** the failing upstream pod(s) — check readiness, logs, `kubectl -n
  app-livefeed rollout restart deploy/<static|wsfeed>` (or `-n app-helloworld deploy/helloworld`);
  edge auto-recovers when the upstream is Ready.
- **Bad deploy:** `kubectl -n app-livefeed rollout undo deploy/edge` (or the failing app).
- **Scanner / abusive 4xx:** no server-side fix needed if all 404 (nothing exposed). Optionally
  rate-limit or block the source IP at the border (Caddy), and confirm no secret paths resolve.
- **VIP / front door down:** verify `edge` holds the apps-pool VIP `10.0.20.240` and the border
  Caddy proxies to it (`pok.somegroup.net`); see the AWS VIP datapath notes.

## Escalation
Page the on-call SRE if 5xx > 1% for >5 min or the VIP/border is down. The border VM is the
single AWS failure domain (EIP + Caddy + BGP + failover agent) — border loss needs a `tofu
apply` rebuild (EIP is preserved).

---

## Worked example — 2026-08-02 (real, from this cluster)

A 4xx-rate look at `edge` (the alert's sibling condition) surfaced a **credential scanner**, not
an incident:

1. **Scope** (status × path): all non-200 were **404**, to secret-hunting paths —
   `/.env`, `/.env.production`, `/.aws/credentials`, `/.git/config`, `/.git/HEAD`, `/api/.env`,
   plus crawler `/robots.txt`. No 5xx.
   → `(run the same query in your Honeycomb env)`
2. **Source** (404 by `client.address` + `user_agent.original`): **`<scanner-ip>`** drove 13
   of 18, cycling through mismatched browser UAs (Chrome/Firefox/Safari/Edge) — a scanner
   masquerading as browsers. The remaining 404s were declared bots (ClaudeBot, GPTBot,
   OAI-SearchBot, MetaCrawler) hitting `/robots.txt` — benign.
   → `(run the same query in your Honeycomb env)`
3. **Conclusion:** internet background credential-scanning against the public front door. All
   requests 404 — `static` serves nothing for those paths, so **no exposure**. No 5xx, no user
   impact.
4. **Actions:** none required. Optional hardening — serve a `/robots.txt`, and rate-limit or
   drop `<scanner-ip>` at the border Caddy. Verified no secret path (`/.env`, `/.git/*`,
   `/.aws/*`) returns anything but 404.

Front-door tracing (edge OTel → Honeycomb) localized the 4xx to a source IP and a set of paths in
two queries; no code or config change was required.
