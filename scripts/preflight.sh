#!/usr/bin/env bash
# Checks to run before going on stage. Rehearses steps 3 and 4 quietly (with a
# free API call), then resets the demo to the state before step 1.
source "$(dirname "$0")/lib.sh"

fails=0
pass() { printf '  \033[32m✓\033[0m %s\n' "$1"; }
fail() { printf '  \033[31m✗\033[0m %s\n' "$1"; fails=$((fails + 1)); }

[ "$(limactl list --format '{{.Status}}' "$LIMA_VM" 2>/dev/null)" = Running ] && pass "VM $LIMA_VM running" || fail "VM $LIMA_VM not running (make up)"
kubectl get --raw /readyz >/dev/null 2>&1 && pass "cluster reachable" || { fail "cluster not reachable (make up)"; exit 1; }

wait_spire 180 && pass "SPIRE server and agent ready" || fail "SPIRE not ready (kubectl -n spire get pods)"
kubectl -n egress-system rollout status deployment/egress-injector --timeout=60s >/dev/null 2>&1 &&
  [ -n "$(kubectl get mutatingwebhookconfiguration egress-proxy-injector -o jsonpath='{.webhooks[0].clientConfig.caBundle}')" ] &&
  pass "injector webhook ready" || fail "injector webhook not ready (make injector)"

if [ -n "${ANTHROPIC_FEDERATION_RULE_ID:-}" ] && [ -n "${ANTHROPIC_SERVICE_ACCOUNT_ID:-}" ] && [ -n "${ANTHROPIC_ORGANIZATION_ID:-}" ]; then
  pass "anthropic.env filled in"
else
  fail "anthropic.env missing or incomplete"
fi

issuer=$(vm sh -c 'echo | openssl s_client -connect api.anthropic.com:443 -servername api.anthropic.com 2>/dev/null | openssl x509 -noout -issuer' 2>/dev/null || true)
case "$issuer" in
  "") fail "api.anthropic.com unreachable from the VM" ;;
  *[Rr]iptides*|*MITM*) fail "api.anthropic.com is intercepted on this network ($issuer); turn off TLS inspection" ;;
  *) pass "api.anthropic.com reachable (${issuer#issuer=})" ;;
esac

# Rehearse steps 3-4.
"$ROOT/scripts/reset.sh" >/dev/null 2>&1
kubectl label namespace support egress-proxy=enabled >/dev/null
kubectl -n support rollout restart deployment/support-bot >/dev/null
if kubectl -n support rollout status deployment/support-bot --timeout=90s >/dev/null 2>&1 &&
  [ "$(kubectl -n support get pod "$(pod_of support-bot)" -o jsonpath='{.spec.initContainers[*].name}')" = egress-proxy ]; then
  wait_single_pod support-bot
  pass "the injector adds the egress proxy sidecar"

  jwt=$(kubectl -n support exec deploy/support-bot -c egress-proxy -- egress-proxy svid 2>/dev/null || true)
  if [ -n "$jwt" ]; then
    iss=$(decode_jwt "$jwt" | jq -rs '.[1].iss')
    kid=$(decode_jwt "$jwt" | jq -rs '.[0].kid')
    [ "$iss" = "$JWT_ISSUER" ] && pass "SVID iss is $iss" || fail "SVID iss is $iss, config.env says $JWT_ISSUER"
    if [ -f "$STATE/oidc/inline-keys.json" ] && jq -e --arg kid "$kid" 'any(.kid == $kid)' "$STATE/oidc/inline-keys.json" >/dev/null; then
      pass "signing key $kid is the one registered with Anthropic"
    else
      fail "signing key $kid is not in .state/oidc/inline-keys.json (make oidc, update the issuer's keys)"
    fi
  else
    fail "the sidecar got no JWT-SVID from SPIRE"
  fi

  code=$(kubectl -n support exec deploy/support-bot -c app -- \
    curl -sS -o /dev/null -w '%{http_code}' https://api.anthropic.com/v1/models -H 'anthropic-version: 2023-06-01' 2>/dev/null || true)
  [ "$code" = 200 ] && pass "a request through the proxy works (GET /v1/models → 200)" ||
    fail "a request through the proxy got HTTP ${code:-error}: $(kubectl -n support logs deploy/support-bot -c egress-proxy --tail=2 2>/dev/null | tr '\n' ' ')"
else
  fail "no egress proxy sidecar (kubectl -n egress-system logs deploy/egress-injector)"
fi
"$ROOT/scripts/reset.sh" >/dev/null 2>&1
pass "reset to the state before step 1"

echo
[ "$fails" = 0 ] && printf '\033[1;32mReady.\033[0m\n' || { printf '\033[1;31m%d check(s) failed.\033[0m\n' "$fails"; exit 1; }
