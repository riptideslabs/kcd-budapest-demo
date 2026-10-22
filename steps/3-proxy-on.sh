#!/usr/bin/env bash
# Step 3: turn on the egress proxy for the pod. Labeling the namespace opts it
# in; the injector webhook adds the sidecar to pods created from then on.
source "$(dirname "$0")/../scripts/lib.sh"
cd "$ROOT"

title "3 · Turn on the egress proxy for the pod"
run "kubectl label namespace support egress-proxy=enabled"
run "kubectl rollout restart deployment/support-bot"
run "kubectl rollout status deployment/support-bot"
wait_single_pod support-bot
run "kubectl get pods"
