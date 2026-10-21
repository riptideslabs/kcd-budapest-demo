#!/usr/bin/env bash
# Export the trust domain's JWT signing keys for Anthropic, both ways:
#   .state/oidc/inline-keys.json  the "keys" array, for an inline JWKS issuer
#   .state/oidc/site/             a static OIDC discovery site (GitHub Pages etc.)
source "$(dirname "$0")/lib.sh"

out="$STATE/oidc"
mkdir -p "$out/site/.well-known"

keys=$(spire_server bundle show -format spiffe | jq '[.keys[] | select(.use == "jwt-svid")
  | del(.use) + {use: "sig", alg: (if .kty == "EC" then {"P-256": "ES256", "P-384": "ES384"}[.crv] else "RS256" end)}]')

jq . <<<"$keys" > "$out/inline-keys.json"
jq -n --argjson keys "$keys" '{keys: $keys}' > "$out/site/keys.json"
jq -n --arg iss "$JWT_ISSUER" '{
  issuer: $iss,
  jwks_uri: ($iss + "/keys.json"),
  authorization_endpoint: "",
  response_types_supported: ["id_token"],
  subject_types_supported: [],
  id_token_signing_alg_values_supported: ["RS256", "ES256", "ES384"]
}' > "$out/site/.well-known/openid-configuration"
# GitHub Pages skips dot-directories unless Jekyll is off.
touch "$out/site/.nojekyll"

log "issuer:  $JWT_ISSUER"
log "key IDs: $(jq -r '[.[].kid] | join(", ")' "$out/inline-keys.json")"
log "inline JWKS:   $out/inline-keys.json"
log "static site:   $out/site (publish at $JWT_ISSUER)"
