#!/usr/bin/env bash
set -euo pipefail

default_repo="$HOME/transpara-ai/repos/site"
if [ ! -d "$default_repo" ] && [ -d /Transpara/transpara-ai/repos/site ]; then
  default_repo=/Transpara/transpara-ai/repos/site
fi
REPO="${SITE_REPO:-$default_repo}"

node_bin=""
if [ -d "$HOME/.nvm/versions/node" ]; then
  node_root=$(find "$HOME/.nvm/versions/node" -mindepth 1 -maxdepth 1 -type d -name 'v*' | sort -V | tail -n 1)
  if [ -n "$node_root" ]; then
    node_bin="$node_root/bin"
  fi
fi
export PATH="$HOME/go/bin:$HOME/.local/bin:${node_bin:+$node_bin:}/snap/bin:$PATH"

step() { echo "==> $1"; }
fail() { echo "FAIL: $1" >&2; exit 1; }

cd "$REPO" || fail "cd to $REPO"

step "make css"
make css || fail "make css"

step "templ generate"
templ generate || fail "templ generate"

step "go build with source revision"
site_revision=$(git rev-parse --verify HEAD) || fail "resolve source revision"
go build -ldflags "-X github.com/transpara-ai/site/internal/buildinfo.embeddedRevision=$site_revision" -o site ./cmd/site/ || fail "go build"

step "setcap"
sudo /usr/sbin/setcap 'cap_net_bind_service=+ep' ./site || fail "setcap"

step "restart service"
systemctl --user restart site || fail "systemctl restart"

step "health check"
sleep 2
status=$(curl -s -o /dev/null -w "%{http_code}" http://localhost/health)
[ "$status" = "200" ] || fail "health check returned $status"

echo "deploy complete — health 200"
