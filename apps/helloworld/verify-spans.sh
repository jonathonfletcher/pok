#!/usr/bin/env bash
# Prove helloworld spans reach the node-local agent AND stay node-local.
#
# With the otel-agent Service set to internalTrafficPolicy: Local, spans from an app
# pod must be handled by the agent on that pod's OWN node. We drive traffic at one
# app pod and print the accepted-span delta for EVERY agent, keyed by node: only the
# app pod's node should increment.
#
# `set -eu` WITHOUT pipefail on purpose (a curl/timeout mid-pipeline must not abort;
# the trailing awk decides each pipeline's status).
set -eu

NS_APP=app-helloworld
NS_OTEL=honeycomb
REQUESTS=5

# target app pod + its node + pod IP
APP_POD=$(kubectl -n "$NS_APP" get pod -l app=helloworld -o jsonpath='{.items[0].metadata.name}')
APP_NODE=$(kubectl -n "$NS_APP" get pod "$APP_POD" -o jsonpath='{.spec.nodeName}')
APP_IP=$(kubectl -n "$NS_APP" get pod "$APP_POD" -o jsonpath='{.status.podIP}')
echo "app pod=$APP_POD node=$APP_NODE ip=$APP_IP  (expect ONLY $APP_NODE's agent to increment)"

# map every agent: node -> pod IP
declare -A AGENT_IP
while read -r node ip; do [ -n "$node" ] && AGENT_IP["$node"]="$ip"; done < <(
  kubectl -n "$NS_OTEL" get pod -l component=agent-collector \
    -o jsonpath='{range .items[*]}{.spec.nodeName}{" "}{.status.podIP}{"\n"}{end}')

# helper pod on the cluster network; cleaned up on exit
kubectl -n "$NS_APP" delete pod curlbox --ignore-not-found >/dev/null 2>&1 || true
trap 'kubectl -n "$NS_APP" delete pod curlbox --ignore-not-found >/dev/null 2>&1 || true' EXIT
kubectl -n "$NS_APP" run curlbox --image=curlimages/curl:8.11.1 --restart=Never \
  --command -- sleep 3600 >/dev/null
kubectl -n "$NS_APP" wait --for=condition=Ready pod/curlbox --timeout=60s >/dev/null

# accepted spans on the agent at pod IP $1
accepted() {
  kubectl -n "$NS_APP" exec curlbox -- curl -s --max-time 5 "http://$1:8888/metrics" \
    | awk '$0 ~ /^#/ {next} $1 ~ /^otelcol_receiver_accepted_spans/ {s+=$NF} END {printf "%d", s+0}'
}

declare -A BEFORE
for node in "${!AGENT_IP[@]}"; do BEFORE["$node"]=$(accepted "${AGENT_IP[$node]}"); done

# traffic straight at the one app pod (each GET / => 2 spans; /healthz filtered)
kubectl -n "$NS_APP" exec curlbox -- sh -c \
  "for i in \$(seq $REQUESTS); do curl -s http://$APP_IP:8080/ >/dev/null; done"
sleep 8

echo "--- accepted-span delta per agent (by node) ---"
for node in "${!AGENT_IP[@]}"; do
  after=$(accepted "${AGENT_IP[$node]}")
  d=$(( after - ${BEFORE[$node]} ))
  mark=""; [ "$node" = "$APP_NODE" ] && mark="   <== app's node (expected > 0)"
  printf "  %-6s delta=%s%s\n" "$node" "$d" "$mark"
done
