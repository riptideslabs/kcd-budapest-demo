#!/usr/bin/env bash
# Step 1: the client pod has no key: not in env, not in files, no Secrets.
# SPIRE is already running in its own namespace.
source "$(dirname "$0")/../scripts/lib.sh"
cd "$ROOT"

title "1 · The pod has no key"
run "kubectl get pods"
note "SPIRE runs next to it: the server, and an agent on every node"
run "kubectl get pods -n spire"
run "kubectl get secrets"
run "kubectl exec deploy/support-bot -c app -- env"
run "kubectl exec deploy/support-bot -c app -- ls /var/run/secrets"
