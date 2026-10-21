#!/usr/bin/env bash
# The five steps back to back. With PAUSE=1, waits for a key before each command.
source "$(dirname "$0")/../scripts/lib.sh"

for s in "$ROOT"/steps/[1-5]-*.sh; do
  "$s"
done
