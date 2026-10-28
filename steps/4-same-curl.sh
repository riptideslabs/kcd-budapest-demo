#!/usr/bin/env bash
# Step 4: the same command now succeeds. The proxy's log shows why: SPIRE gave
# the pod its SPIFFE ID, tokenex exchanged it for an Anthropic token, and the
# request went out with that token.
source "$(dirname "$0")/../scripts/lib.sh"
cd "$ROOT"

title "4 · The same command"
run "$ASK_CLAUDE"
note "the proxy's log: the pod's identity from SPIRE, the token from Anthropic, the request"
run "kubectl logs deploy/support-bot -c egress-proxy"
