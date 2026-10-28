# KCD Budapest demo: no key in the pod

Live demo for *Every Workload Deserves an Identity Instead of an API Key*
(KCD Budapest, 2026-10-30). A pod calls the Claude API without ever holding a
key: SPIRE gives it an identity, [tokenex](https://github.com/riptideslabs/tokenex)
exchanges that identity for a short-lived Anthropic token, and an egress proxy
sidecar adds the token on the way out. The five steps match the deck's demo
slide.

Everything is open source: kind, SPIRE 1.15, tokenex v0.7.0, and two small Go
programs in this repo: the proxy in [`proxy/`](proxy) and a mutating admission
webhook in [`injector/`](injector) that adds the proxy to opted-in pods.

```
 support-bot pod                                         Anthropic
┌───────────────────────────────────────────────┐
│ app (curl)  ──HTTPS_PROXY──▶ egress-proxy ─────┼──▶ api.anthropic.com/v1/messages
│ no key, trusts               │  adds Authorization: Bearer sk-ant-oat01-…
│ the proxy's CA               │                 │
└──────────────────────────────┼─────────────────┘
          Workload API socket  │  tokenex: RFC 7523 jwt-bearer exchange
                               ▼                    ─▶ api.anthropic.com/v1/oauth/token
                    SPIRE agent ── JWT-SVID           (checks the SVID against SPIRE's
                    sub=spiffe://kcd-budapest.demo/     public keys and the federation rule)
                        ns/support/sa/support-bot

 egress-injector (webhook): at pod creation in a namespace labeled
 egress-proxy=enabled, adds the egress-proxy sidecar, its volumes, and
 HTTPS_PROXY + CA variables to the app containers.
```

## What's where

| Path | |
|---|---|
| `injector/` | The mutating admission webhook. For pods created in namespaces labeled `egress-proxy=enabled`, it adds `egress-proxy` as a native sidecar (it starts and writes its CA before the app does), the SPIRE socket and CA volumes, and `HTTPS_PROXY`, `NO_PROXY` and the CA variables for curl, Go/OpenSSL, Python and Node to every app container. It makes its own serving certificate at startup and writes the CA into its webhook configuration, so it needs no cert-manager. |
| `proxy/` | The sidecar. Fetches the pod's JWT-SVID (`pkg/token/spiffe`), exchanges it with `rfc7523.GetAnthropicCredentials`, keeps it refreshed, and terminates TLS for `api.anthropic.com` with a per-pod CA that exists only in memory. Every other destination is refused. |
| `manifests/injector/` | The injector's Deployment, Service, RBAC (patch its own webhook configuration only) and `MutatingWebhookConfiguration` (`failurePolicy: Fail`). |
| `manifests/spire/` | SPIRE server (k8s_psat node attestation, keys on disk) and agent (k8s workload attestor). |
| `manifests/demo/` | Namespace `support` with the `support-bot` Deployment: plain curl, nothing proxy-related. |
| `steps/` | The stage script, one file per step. |
| `scripts/` | Setup, reset, JWKS export and preflight. |
| `config.env` | Trust domain, `jwt_issuer`, model. |
| `anthropic.env` | Your Anthropic IDs (not committed; copy `anthropic.env.example`). |

## Setup

You need an Apple Silicon Mac with [Lima](https://lima-vm.io) 2.x, Go 1.25+,
`kubectl` and `jq`. Docker and kind run inside a Lima VM (`kcd-demo`, 4 CPUs,
4 GiB); kubectl talks to the cluster from the Mac.

```bash
make up
```

The first run takes a few minutes (Ubuntu image, Docker, kind node image,
SPIRE images). It's idempotent; rerun it whenever. When it's done, the
cluster has SPIRE and the injector running and the curl pod up.

### Anthropic setup (once)

Anthropic needs three things: an issuer it trusts, a service account, and a
federation rule that maps exactly one SPIFFE ID to that service account. You
set them up once in your Anthropic organization. After `make nuke` (new SPIRE
keys), only the issuer's keys need updating.

1. Export SPIRE's public signing keys:

   ```bash
   make oidc
   ```

2. **Issuer.** Claude Console → **Settings → Workload identity → Issuers →
   Register issuer** (not the Connect workload wizard: its Custom OIDC page
   insists you confirm a public discovery document, which inline keys don't
   have):
   - name `kcd-demo-spire`
   - issuer URL = `JWT_ISSUER` from `config.env`
     (`https://bonifaido.github.io/kcd-budapest-oidc`). It's only compared with
     the `iss` claim; nothing is hosted there.
   - JWKS source **Inline keys**: paste `.state/oidc/inline-keys.json`
   - leave JTI replay protection on (JWT-SVIDs carry no `jti`) and the
     maximum token lifetime at 1 hour

   To use discovery instead, publish `.state/oidc/site/` (it has `.nojekyll`,
   `.well-known/openid-configuration` and `keys.json`) at `JWT_ISSUER`, e.g.
   as a GitHub Pages repo, and pick **OIDC discovery**.

3. **Rule and service account.** **Connect workload → Use a registered issuer
   → kcd-demo-spire**, then:
   - rule name `kcd-demo-support-bot`
   - subject: `spiffe://kcd-budapest.demo/ns/support/sa/support-bot`
     (exact, no trailing `*`)
   - advanced match options → expected audience `https://api.anthropic.com`
   - workspaces: only the one the demo should use
   - advanced rule options: scope `workspace:developer` (the wizard doesn't
     offer `workspace:inference`; the Admin API does), token lifetime 5 minutes
   - target: create a new service account `kcd-demo-support-bot`

   The last page shows the four IDs and listens for an exchange;
   `make preflight` with the IDs in place triggers one.

4. Put the IDs in `anthropic.env` (template: `anthropic.env.example`), then
   `make preflight`.

The same resources through the Admin API, if you prefer:

```json
{"name": "kcd-demo-spire", "issuer_url": "https://bonifaido.github.io/kcd-budapest-oidc",
 "jwks": {"type": "inline", "keys": [ …contents of .state/oidc/inline-keys.json… ]}}

{"name": "kcd-demo-support-bot", "issuer_id": "fdis_…",
 "match": {"subject_prefix": "spiffe://kcd-budapest.demo/ns/support/sa/support-bot",
           "audience": "https://api.anthropic.com"},
 "target": {"type": "service_account", "service_account_id": "svac_…"},
 "workspace_id": "wrkspc_…", "oauth_scope": "workspace:inference", "token_lifetime_seconds": 300}
```

### Keys and rotation

The SPIRE server keeps its keys and datastore in the VM
(`/var/lib/kcd-demo/spire-server`), outside the cluster, with `ca_ttl = 2160h`.
So `make down && make up` keeps the same JWT signing key, and the JWKS you
registered stays valid for weeks. Only `make nuke` (or deleting the VM) makes
new keys; after that, `make oidc` and update the issuer. `make preflight`
checks that the key signing today's SVIDs is the one you registered.

## On stage

```bash
make preflight   # checks, rehearses steps 3-4, resets; should end with "Ready."
make demo        # the five steps; press a key before each command
```

Or run them one by one with `make step1` … `make step5`. `make reset` puts the
demo back to the state before step 1 (about 10 s). Set your terminal font large
first; commands are printed exactly as they run.

| Step | Target | What the room sees |
|---|---|---|
| 1. The pod has no key | `make step1` | One pod running curl, `No resources found` for Secrets, `env` with no key, no `/var/run/secrets` |
| 2. The API call fails | `make step2` | `curl … returned error: 401`, `authentication_error` |
| 3. Turn on the egress proxy | `make step3` | `kubectl label namespace support egress-proxy=enabled`, `kubectl rollout restart`: the injector adds the sidecar, the pod is now 2/2. The Deployment itself doesn't change. |
| 4. The same command succeeds | `make step4` | The same curl, now Claude's greeting to KCD Budapest. Then the proxy's log: the JWT-SVID SPIRE issued for `spiffe://kcd-budapest.demo/ns/support/sa/support-bot`, the Anthropic token tokenex got for it, and the request going out with that token |
| 5. Nothing to steal | `make step5` | A fake exfiltration script checks env, files, Secrets, the SPIRE socket and processes: `Nothing to steal.` |

Things to say if asked:

- **Can code in the pod still use the proxy?** Yes, while the pod runs. The
  token never leaves the sidecar and lives five minutes. There's nothing to
  take home.
- **The CA?** Generated per pod at startup, private key in memory only. That's
  the "injection isn't free" cost: the app must trust it, which the injector
  arranges through environment variables.
- **What if the injector is down?** Pods in opted-in namespaces aren't
  created (`failurePolicy: Fail`): a workload that asked for the proxy never
  runs without it. Other namespaces aren't affected.
- **Production?** A highly available injector with its certificate from
  cert-manager (each replica makes its own CA today), the SPIFFE CSI driver
  instead of a hostPath socket, the SPIRE OIDC Discovery Provider with normal
  key rotation instead of a 90-day key, and a proxy with real HTTP/2 and SigV4
  support.

## Troubleshooting

| Symptom | Fix |
|---|---|
| Step 4 returns a `permission_error` from `egress-proxy` | Anthropic refused the exchange. Check **Workload identity → Authentication events** in the Console for the reason. Usually the issuer URL differs from `JWT_ISSUER`, or the registered keys don't contain the current `kid` (`make preflight` checks both). |
| `api.anthropic.com is intercepted` in preflight | A TLS-inspecting proxy (corporate network, a local MITM proxy) re-signs the traffic and the proxy rejects it. Turn it off or use another network. |
| `cluster not reachable` | `make up`. It starts the VM, recreates the cluster if needed, and waits until SPIRE has settled, which takes a minute or two after a Mac reboot. |
| Proxy or injector changes don't show up | `make images injector reset` rebuilds and reloads both. |
| Step 3's pod stays 1/1, or isn't created | The injector isn't serving: `kubectl -n egress-system logs deploy/egress-injector`, then `make injector`. |
| Changed `config.env` | `make spire reset`; SPIRE restarts with the new settings. A new `JWT_ISSUER` also needs the Anthropic issuer updated. |

Teardown: `make down` (cluster), `make nuke` (also SPIRE keys),
`make vm-delete` (the VM).
