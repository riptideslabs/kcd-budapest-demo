SHELL := /bin/bash
.DEFAULT_GOAL := help

.PHONY: help up vm cluster images spire injector reset oidc preflight demo \
	step1 step2 step3 step4 step5 logs down nuke vm-delete

help: ## List targets
	@grep -E '^[a-z0-9-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

# --- setup ------------------------------------------------------------------

up: vm cluster images spire injector reset ## Everything: VM, kind cluster, images, SPIRE, injector, demo pods

vm: ## Create/start the Lima VM that runs Docker
	@scripts/vm.sh

cluster: ## Create the kind cluster and .state/kubeconfig
	@scripts/cluster.sh

images: ## Build the proxy and injector images, preload all images into kind
	@scripts/images.sh

spire: ## Deploy SPIRE and register the workload
	@scripts/spire.sh

injector: ## Deploy the webhook that injects the egress proxy sidecar
	@scripts/injector.sh

oidc: ## Export SPIRE's JWT signing keys (inline JWKS + static discovery site)
	@scripts/oidc.sh

reset: ## Recreate the demo pods: back to the state before step 1
	@scripts/reset.sh

preflight: ## Check everything, rehearse steps 3-4, then reset
	@scripts/preflight.sh

# --- on stage -----------------------------------------------------------------

demo: ## Steps 1-5 in a row, waiting for a key before each command
	@PAUSE=1 steps/all.sh

step1: ## The pod has no key
	@steps/1-no-key.sh
step2: ## curl to the API: 401
	@steps/2-401.sh
step3: ## Turn on the egress proxy (label namespace, restart)
	@steps/3-proxy-on.sh
step4: ## The same curl succeeds
	@steps/4-same-curl.sh
step5: ## Fake exfiltration finds nothing
	@steps/5-exfil.sh

logs: ## The proxy's log (for debugging)
	@KUBECONFIG=.state/kubeconfig kubectl -n support logs deploy/support-bot -c egress-proxy

# --- teardown -----------------------------------------------------------------

down: ## Delete the kind cluster (SPIRE keys stay in the VM)
	@source scripts/lib.sh && vm "$$KIND" delete cluster --name "$$CLUSTER_NAME"

nuke: down ## Also delete SPIRE's keys: you must re-register the JWKS afterwards
	@source scripts/lib.sh && vm sudo rm -rf /var/lib/kcd-demo/spire-server

vm-delete: ## Delete the Lima VM
	limactl delete --force kcd-demo
