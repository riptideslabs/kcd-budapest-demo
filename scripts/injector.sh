#!/usr/bin/env bash
# Deploy the egress proxy injector (mutating admission webhook).
source "$(dirname "$0")/lib.sh"

kubectl apply -f "$ROOT/manifests/injector/injector.yaml" >/dev/null
# Pick up a rebuilt image.
kubectl -n egress-system rollout restart deployment/egress-injector >/dev/null

log "waiting for the injector"
# Ready means it has published its CA to the webhook configuration.
kubectl -n egress-system rollout status deployment/egress-injector --timeout=120s >/dev/null
[ -n "$(kubectl get mutatingwebhookconfiguration egress-proxy-injector -o jsonpath='{.webhooks[0].clientConfig.caBundle}')" ] ||
  die "the webhook configuration has no caBundle"
log "injector ready: pods in namespaces labeled egress-proxy=enabled get the sidecar"
