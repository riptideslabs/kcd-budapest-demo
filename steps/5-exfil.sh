#!/usr/bin/env bash
# Step 5: a fake exfiltration script in the app container finds nothing to steal.
source "$(dirname "$0")/../scripts/lib.sh"
cd "$ROOT"

title "5 · A fake exfiltration script"
run "kubectl exec -i deploy/support-bot -c app -- sh < steps/exfil.sh"
