#!/usr/bin/env bash
# Step 1: the client pod has no key: not in env, not in files, no Secrets.
source "$(dirname "$0")/../scripts/lib.sh"
cd "$ROOT"

title "1 · The pod has no key"
run "kubectl get pods"
run "kubectl get secrets"
run "kubectl exec deploy/support-bot -c app -- env"
run "kubectl exec deploy/support-bot -c app -- ls /var/run/secrets"
