#!/usr/bin/env bash
# Build the proxy image and preload every image into kind, so nothing is pulled
# during the demo.
source "$(dirname "$0")/lib.sh"

arch=$(vm uname -m | sed 's/aarch64/arm64/; s/x86_64/amd64/')

log "building egress-proxy (linux/$arch)"
mkdir -p "$STATE/image"
(cd "$ROOT/proxy" && GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$STATE/image/egress-proxy" .)
cp "$ROOT/proxy/Dockerfile" "$STATE/image/"
vm docker build -q -t egress-proxy:dev .state/image >/dev/null

images=$(grep -rhoE 'image: [^ ]+' "$ROOT/manifests" | awk '{print $2}' | grep -v '^egress-proxy' | sort -u)
for img in $images; do
  vm docker image inspect "$img" >/dev/null 2>&1 || { log "pulling $img"; vm docker pull -q "$img" >/dev/null; }
done

log "loading images into kind"
# shellcheck disable=SC2086
vm "$KIND" load docker-image --name "$CLUSTER_NAME" egress-proxy:dev $images
