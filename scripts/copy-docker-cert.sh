#!/usr/bin/env bash
set -euo pipefail

REGISTRY_HOST_PORT="${REGISTRY_HOST_PORT:-air-registry.localhost:8443}"
ROOT_CRT="${CADDY_ROOT_CRT:-$HOME/Library/Application Support/Caddy/pki/authorities/local/root.crt}"
DEST_DIR="$HOME/.docker/certs.d/$REGISTRY_HOST_PORT"

if [ ! -f "$ROOT_CRT" ]; then
  echo "error: Caddy root cert not found: $ROOT_CRT" >&2
  echo "run \`just caddy\` once to create the local CA, then retry" >&2
  exit 1
fi

mkdir -p "$DEST_DIR"
cp "$ROOT_CRT" "$DEST_DIR/ca.crt"
echo "copied: $ROOT_CRT"
echo "   -> : $DEST_DIR/ca.crt"

if ! pgrep -q "Docker Desktop"; then
  echo
  echo "Docker Desktop is not running; the cert activates on next start."
  exit 0
fi

echo
read -r -p "Restart Docker Desktop now to activate the cert? [y/N] " reply || reply=""
if [ "$reply" != "y" ] && [ "$reply" != "Y" ]; then
  echo "skipped; restart Docker Desktop when ready."
  exit 0
fi

echo "restarting Docker Desktop..."
osascript -e 'quit app "Docker Desktop"' >/dev/null 2>&1 || true

for _ in $(seq 1 60); do
  pgrep -q "Docker Desktop" || break
  sleep 1
done

if pgrep -q "Docker Desktop"; then
  echo "error: Docker Desktop did not quit in time; restart it manually" >&2
  exit 1
fi

open -a "Docker Desktop"

echo -n "waiting for the docker daemon"
for _ in $(seq 1 90); do
  if docker info >/dev/null 2>&1; then
    echo
    echo "docker is back up; $REGISTRY_HOST_PORT is now a trusted HTTPS registry"
    exit 0
  fi
  echo -n "."
  sleep 2
done

echo
echo "error: docker daemon did not come back in time; check Docker Desktop" >&2
exit 1
