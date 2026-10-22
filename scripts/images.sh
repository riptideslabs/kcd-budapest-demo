#!/usr/bin/env bash
# Build the proxy and injector images and preload every image into kind, so
# nothing is pulled during the demo.
source "$(dirname "$0")/lib.sh"

arch=$(vm uname -m | sed 's/aarch64/arm64/; s/x86_64/amd64/')

for app in proxy:egress-proxy injector:egress-injector; do
  dir=${app%%:*} name=${app#*:}
  log "building $name (linux/$arch)"
  mkdir -p "$STATE/images/$name"
  (cd "$ROOT/$dir" && GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$STATE/images/$name/$name" .)
  cp "$ROOT/$dir/Dockerfile" "$STATE/images/$name/"
  vm docker build -q -t "$name:dev" ".state/images/$name" >/dev/null
done

images=$(grep -rhoE 'image: [^ ]+' "$ROOT/manifests" | awk '{print $2}' | grep -v '^egress-' | sort -u)
for img in $images; do
  vm docker image inspect "$img" >/dev/null 2>&1 || { log "pulling $img"; vm docker pull -q "$img" >/dev/null; }
done

log "loading images into kind"
# shellcheck disable=SC2086
vm "$KIND" load docker-image --name "$CLUSTER_NAME" egress-proxy:dev egress-injector:dev $images
