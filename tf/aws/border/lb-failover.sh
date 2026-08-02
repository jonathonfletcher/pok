#!/usr/bin/env bash
# =============================================================================
# lb-failover.sh — BGP-driven active/standby failover for the Cilium LB pool.
# =============================================================================
# WHY: AWS VPC route tables can't ECMP and only deliver to real ENIs, so the
# Cilium LB pool (DEST_CIDR, e.g. 10.0.20.0/24 — a subnet with no ENIs) is made
# reachable by ONE static route -> a node's ENI. That single ENI is a SPOF. This
# agent removes the SPOF by watching FRR's BGP sessions (the same peering that
# Cilium runs to this border) and, whenever the node the route currently points
# at is no longer an Established peer, repointing the route to a live node via
# ec2:ReplaceRoute. It is the AWS analogue of the bhyve OpenBSD gateway dropping
# a dead BGP nexthop — active/standby (AWS can't do active/active) with automatic
# failover. See docs/README.md §5.2.
#
# Requires: frr (vtysh), awscli v1+, jq. AWS creds come from the border's IAM
# instance role (ec2:ReplaceRoute on this route table + ec2:Describe*).
# Config: /etc/lb-failover.env (ROUTE_TABLE_ID, DEST_CIDR, AWS_REGION, VPC_ID,
#         POLL_SECONDS). Runs as a loop under systemd (lb-failover.service).
# =============================================================================
set -uo pipefail

: "${ROUTE_TABLE_ID:?}" "${DEST_CIDR:?}" "${AWS_REGION:?}" "${VPC_ID:?}"
POLL_SECONDS="${POLL_SECONDS:-5}"

log() { echo "$(date -u +%FT%TZ) lb-failover: $*"; }

# Established BGP peers (node IPs) that are also receiving/advertising at least one
# prefix — i.e. live Cilium nodes. Falls back to all Established if the pfx filter
# yields nothing (a node can still forward LB traffic in eBPF without advertising).
live_nodes() {
  local json
  # NB: use "show ip bgp summary" (nests peers under .ipv4Unicast) — the flat
  # "show bgp ipv4 unicast summary" form puts peers at the top level instead.
  json="$(vtysh -c 'show ip bgp summary json' 2>/dev/null)" || return 1
  local withpfx allest
  withpfx="$(jq -r '.ipv4Unicast.peers // {} | to_entries[]
              | select(.value.state=="Established" and (.value.pfxRcd // 0) > 0) | .key' <<<"$json")"
  allest="$(jq -r '.ipv4Unicast.peers // {} | to_entries[]
              | select(.value.state=="Established") | .key' <<<"$json")"
  # Emit nothing (not a blank line) when there are no peers, so the caller's array is
  # genuinely empty rather than a single empty element.
  if   [ -n "$withpfx" ]; then printf '%s\n' "$withpfx"
  elif [ -n "$allest" ];  then printf '%s\n' "$allest"
  fi
}

# These return EMPTY on a successful call with no result ("None"), but non-zero if the AWS
# call itself fails — so the caller can skip the iteration on a transient API error instead
# of mistaking it for "target is dead" and forcing a spurious failover. (stderr is left
# attached so throttling/errors show up in the journal.)

# ENI id currently targeted by the DEST_CIDR route ("" if the route has no target).
current_target_eni() {
  local out
  out="$(aws ec2 describe-route-tables --region "$AWS_REGION" --route-table-ids "$ROUTE_TABLE_ID" \
        --query "RouteTables[0].Routes[?DestinationCidrBlock=='${DEST_CIDR}'].NetworkInterfaceId | [0]" \
        --output text)" || return 1
  printf '%s' "$out" | grep -v '^None$' || true
}

# Private IP of an ENI ("" if the ENI no longer exists — e.g. node was replaced).
eni_ip() {
  local out
  out="$(aws ec2 describe-network-interfaces --region "$AWS_REGION" --network-interface-ids "$1" \
        --query 'NetworkInterfaces[0].PrivateIpAddress' --output text)" || return 1
  printf '%s' "$out" | grep -v '^None$' || true
}

# ENI id that owns a given private IP inside our VPC ("" if none).
ip_eni() {
  local out
  out="$(aws ec2 describe-network-interfaces --region "$AWS_REGION" \
        --filters "Name=addresses.private-ip-address,Values=$1" "Name=vpc-id,Values=$VPC_ID" \
        --query 'NetworkInterfaces[0].NetworkInterfaceId' --output text)" || return 1
  printf '%s' "$out" | grep -v '^None$' || true
}

log "started: DEST_CIDR=$DEST_CIDR RTB=$ROUTE_TABLE_ID region=$AWS_REGION poll=${POLL_SECONDS}s"

while true; do
  mapfile -t LIVE < <(live_nodes)
  if [ "${#LIVE[@]}" -eq 0 ]; then
    log "no Established BGP peers — leaving route unchanged (no healthy target)"
    sleep "$POLL_SECONDS"; continue
  fi

  # On an AWS API error here, leave the route alone (don't flap off a healthy node).
  if ! cur_eni="$(current_target_eni)"; then
    log "describe-route-tables failed (AWS API error) — leaving route unchanged"
    sleep "$POLL_SECONDS"; continue
  fi
  cur_ip=""
  if [ -n "$cur_eni" ] && ! cur_ip="$(eni_ip "$cur_eni")"; then
    log "describe-network-interfaces failed (AWS API error) — leaving route unchanged"
    sleep "$POLL_SECONDS"; continue
  fi

  # If the current target is a live node, do nothing (sticky — avoids flapping).
  healthy=""
  for ip in "${LIVE[@]}"; do [ "$ip" = "$cur_ip" ] && healthy=1 && break; done
  if [ -n "$healthy" ]; then sleep "$POLL_SECONDS"; continue; fi

  # Current target dead/missing — pick the lowest-IP live node (deterministic).
  target_ip="$(printf '%s\n' "${LIVE[@]}" | sort -V | head -1)"
  target_eni="$(ip_eni "$target_ip")"
  if [ -z "$target_eni" ]; then
    log "could not resolve ENI for live node $target_ip — retrying"
    sleep "$POLL_SECONDS"; continue
  fi

  if aws ec2 replace-route --region "$AWS_REGION" --route-table-id "$ROUTE_TABLE_ID" \
       --destination-cidr-block "$DEST_CIDR" --network-interface-id "$target_eni" 2>/dev/null; then
    log "FAILOVER: $DEST_CIDR -> $target_ip ($target_eni)  [was ${cur_ip:-none}/${cur_eni:-none}]"
  else
    log "ERROR: replace-route to $target_ip ($target_eni) failed — retrying"
  fi
  sleep "$POLL_SECONDS"
done
