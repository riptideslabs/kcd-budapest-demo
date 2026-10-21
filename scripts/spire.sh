#!/usr/bin/env bash
# Deploy SPIRE server and agent, and register the demo workload.
source "$(dirname "$0")/lib.sh"

kubectl apply -f "$ROOT/manifests/spire/server.yaml" >/dev/null
settings=$(kubectl -n spire create configmap spire-settings --from-env-file="$ROOT/config.env" \
  --dry-run=client -o yaml | kubectl apply -f -)

kubectl apply -f "$ROOT/manifests/spire/agent.yaml" >/dev/null
if [[ $settings == *configured* ]]; then
  log "settings changed; restarting SPIRE"
  kubectl -n spire rollout restart statefulset/spire-server daemonset/spire-agent >/dev/null
fi

log "waiting for the SPIRE server"
kubectl -n spire rollout status statefulset/spire-server --timeout=180s >/dev/null

for _ in $(seq 60); do
  [ -n "$(kubectl -n spire get configmap spire-bundle -o jsonpath='{.data.bundle\.crt}')" ] && break
  sleep 1
done

log "waiting for the SPIRE agent"
kubectl -n spire rollout status daemonset/spire-agent --timeout=180s >/dev/null
wait_spire || die "SPIRE didn't settle; kubectl -n spire get pods"

entry() {
  local id=$1; shift
  if [ "$(spire_server entry show -spiffeID "$id" -output json | jq '.entries | length')" -gt 0 ]; then
    return
  fi
  log "registering $id"
  spire_server entry create -spiffeID "$id" "$@" >/dev/null
}

td="spiffe://$TRUST_DOMAIN"
# Every agent in this cluster, so the workload entries match on any node.
entry "$td/ns/spire/sa/spire-agent" -node -selector "k8s_psat:cluster:$CLUSTER_NAME"

# Only the egress-proxy container of support-bot pods gets this identity.
entry "$td/ns/support/sa/support-bot" -parentID "$td/ns/spire/sa/spire-agent" \
  -selector k8s:ns:support -selector k8s:sa:support-bot -selector k8s:container-name:egress-proxy

log "SPIRE ready (trust domain $TRUST_DOMAIN, issuer $JWT_ISSUER)"
