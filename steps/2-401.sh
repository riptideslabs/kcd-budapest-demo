#!/usr/bin/env bash
# Step 2: calling Claude from the pod fails with 401.
source "$(dirname "$0")/../scripts/lib.sh"
cd "$ROOT"

title "2 · Call Claude from the pod"
run "$ASK_CLAUDE"
