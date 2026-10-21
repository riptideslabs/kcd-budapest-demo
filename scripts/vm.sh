#!/usr/bin/env bash
# Create (once) and start the Lima VM that runs Docker for kind, and build kind for it.
source "$(dirname "$0")/lib.sh"

if ! limactl list -q | grep -qx "$LIMA_VM"; then
  log "creating Lima VM $LIMA_VM"
  limactl create --name="$LIMA_VM" --tty=false "$ROOT/lima/kcd-demo.yaml" \
    --set ".mounts += [{\"location\": \"$ROOT\", \"writable\": true}]"
fi

if [ "$(limactl list --format '{{.Status}}' "$LIMA_VM")" != Running ]; then
  log "starting Lima VM $LIMA_VM"
  limactl start --tty=false "$LIMA_VM"
fi

# Lima rewrites daemon.json at boot without restarting Docker; make the
# containerdSnapshotter=false setting take effect.
if vm docker info --format '{{.DriverStatus}}' | grep -q containerd.snapshotter; then
  log "switching Docker to the overlay2 image store"
  vm sudo systemctl restart docker
fi

if [ ! -x "$KIND" ]; then
  log "building kind for the VM"
  arch=$(vm uname -m | sed 's/aarch64/arm64/; s/x86_64/amd64/')
  GOOS=linux GOARCH="$arch" CGO_ENABLED=0 GOBIN='' go install sigs.k8s.io/kind@v0.33.0
  mkdir -p "$STATE/bin"
  cp "$(go env GOPATH)/bin/linux_$arch/kind" "$KIND"
fi
