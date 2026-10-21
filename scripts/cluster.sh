#!/usr/bin/env bash
# Create the kind cluster in the VM (once) and write a kubeconfig for the Mac.
source "$(dirname "$0")/lib.sh"

# SPIRE server keys and datastore; kept across cluster re-creation.
vm sudo install -d -o 1000 -g 1000 -m 0700 /var/lib/kcd-demo/spire-server

if ! vm "$KIND" get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
  log "creating kind cluster $CLUSTER_NAME"
  vm "$KIND" create cluster --name "$CLUSTER_NAME" --config kind/cluster.yaml --wait 120s
fi

vm "$KIND" get kubeconfig --name "$CLUSTER_NAME" > "$KUBECONFIG"
kubectl config set-context --current --namespace=support >/dev/null

for _ in $(seq 30); do kubectl get --raw /readyz >/dev/null 2>&1 && break; sleep 1; done
kubectl get --raw /readyz >/dev/null || die "the API server isn't reachable on the Mac (Lima port forward?)"
log "cluster ready; KUBECONFIG=$KUBECONFIG"
