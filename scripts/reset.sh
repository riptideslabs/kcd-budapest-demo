#!/usr/bin/env bash
# Put the demo back to the state before step 1: a fresh curl pod, no proxy.
source "$(dirname "$0")/lib.sh"

[ -f "$ROOT/anthropic.env" ] || log "anthropic.env is missing; the proxy will start but can't get a token"

kubectl delete namespace support --ignore-not-found --wait=true >/dev/null
kubectl apply -f "$ROOT/manifests/demo/namespace.yaml" >/dev/null
kubectl -n support create configmap anthropic-wif \
  --from-env-file="${ANTHROPIC_ENV:-$ROOT/anthropic.env}" >/dev/null 2>&1 ||
  kubectl -n support create configmap anthropic-wif >/dev/null
kubectl apply -f "$ROOT/manifests/demo/support-bot.yaml" >/dev/null

kubectl -n support rollout status deployment/support-bot --timeout=120s >/dev/null
log "demo reset: $(kubectl -n support get pods --no-headers | awk '{print $1" "$2}' | paste -sd ',' - | sed 's/,/, /g')"
