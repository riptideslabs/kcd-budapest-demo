# Shared by the setup scripts (scripts/) and the stage scripts (steps/).
# shellcheck shell=bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STATE="$ROOT/.state"

set -a
# shellcheck source=../config.env
source "$ROOT/config.env"
if [ -f "$ROOT/anthropic.env" ]; then
  # shellcheck disable=SC1091
  source "$ROOT/anthropic.env"
fi
set +a

export KUBECONFIG="$STATE/kubeconfig"
LIMA_VM="${LIMA_VM:-kcd-demo}"
KIND="$STATE/bin/kind"

# Run a command in the Lima VM, in this directory (it's mounted at the same path).
vm() { limactl shell --workdir "$ROOT" "$LIMA_VM" -- "$@"; }

log() { printf '\033[1;34m==>\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

spire_server() { kubectl -n spire exec spire-server-0 -c spire-server -- /opt/spire/bin/spire-server "$@"; }

# Wait until SPIRE has settled. After a VM or node restart the server can crash
# once while cluster networking comes up, so require it to have been up for a
# while, and the agent to serve the Workload API.
wait_spire() {
  local deadline=$((SECONDS + ${1:-240}))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if up_for spire-server-0 45 && up_for "$(kubectl -n spire get pod -l app=spire-agent -o name | head -1)" 15 &&
      kubectl -n spire exec ds/spire-agent -- /opt/spire/bin/spire-agent healthcheck \
        -socketPath /run/spire/agent-sockets/spire-agent.sock >/dev/null 2>&1; then
      return 0
    fi
    sleep 3
  done
  return 1
}

# A spire pod is ready and its container has run for at least $2 seconds.
up_for() {
  kubectl -n spire get pod "${1#pod/}" -o json 2>/dev/null | jq -e --argjson min "$2" '
    .status.containerStatuses[0] | .ready and .state.running != null
    and (.state.running.startedAt | fromdateiso8601) <= (now - $min)' >/dev/null
}

# The newest running pod of a demo deployment.
pod_of() {
  kubectl -n support get pods -l "app=$1" -o json |
    jq -r '[.items[] | select(.metadata.deletionTimestamp == null)] | sort_by(.metadata.creationTimestamp) | last | .metadata.name // empty'
}

# Wait until a deployment has exactly one pod left (old ones gone after a rollout).
wait_single_pod() {
  for _ in $(seq 60); do
    [ "$(kubectl -n support get pods -l "app=$1" --no-headers 2>/dev/null | wc -l | tr -d ' ')" = 1 ] && return 0
    sleep 0.5
  done
  die "old $1 pods did not go away"
}

# --- stage output -----------------------------------------------------------

B=$'\033[1m' DIM=$'\033[2m' CYAN=$'\033[36m' YELLOW=$'\033[33m' R=$'\033[0m'

title() { printf '\n%s%s━━ %s%s\n\n' "$B" "$YELLOW" "$*" "$R"; }
note() { printf '%s# %s%s\n' "$DIM" "$*" "$R"; }

# With PAUSE=1, wait for a key before each command (for live pacing).
pause() { if [ -n "${PAUSE:-}" ]; then read -rsn1 </dev/tty || true; fi; }

# Print a command the way the audience should read it, then run it.
run() {
  printf '%s%s$ %s%s\n' "$B" "$CYAN" "$1" "$R"
  pause
  eval "$1" || true
  echo
}

# The request steps 2 and 4 send: plain curl in the app container, no key.
ASK_CLAUDE="kubectl exec deploy/support-bot -c app -- \\
    curl -sS --fail-with-body https://api.anthropic.com/v1/messages \\
      -H 'anthropic-version: 2023-06-01' -H 'content-type: application/json' \\
      -d '{\"model\": \"$MODEL\", \"max_tokens\": 100,
           \"messages\": [{\"role\": \"user\", \"content\": \"Say hello to KCD Budapest in one sentence.\"}]}' \\
  | jq '.error // .content[0].text'"

# Decode a JWT's header and claims (base64url, no signature check), with
# iat and exp as UTC dates.
decode_jwt() {
  jq -R 'split(".") | .[0:2][] | gsub("-"; "+") | gsub("_"; "/") | @base64d | fromjson
         | if has("exp") then .iat |= todate | .exp |= todate else . end' <<<"$1"
}
