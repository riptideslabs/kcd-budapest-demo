#!/usr/bin/env bash
# Step 4: the same command now succeeds.
source "$(dirname "$0")/../scripts/lib.sh"
cd "$ROOT"

title "4 · The same command"
run "$ASK_CLAUDE"
