#!/usr/bin/env bash
# Step 3: turn on the egress proxy sidecar for the pod.
source "$(dirname "$0")/../scripts/lib.sh"
cd "$ROOT"

title "3 · Turn on the egress proxy for the pod"
run "kubectl patch deployment support-bot --patch-file manifests/demo/egress-proxy-patch.yaml"
run "kubectl rollout status deployment/support-bot"
wait_single_pod support-bot
run "kubectl get pods"
