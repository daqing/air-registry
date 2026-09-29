#!/usr/bin/env bash
# Set up a Lima VM (e.g. for nerdctl.lima) as a client of the local HTTPS
# registry. Three things are needed inside the VM:
#
#   1. the registry name must resolve to the host — *.localhost resolves to
#      loopback inside the VM, so an /etc/hosts entry pointing at
#      host.lima.internal is required;
#   2. Caddy's local root CA must be trusted, like Docker Desktop's trust
#      store in copy-docker-cert.sh;
#   3. registry requests must bypass the host-propagated HTTP(S) proxy —
#      Lima copies the host's proxy settings into the VM without a NO_PROXY
#      entry for *.localhost, so pushes and pulls die in the proxy with
#      `EOF`. The registry only listens on the host, so the proxy can never
#      tunnel to it. NO_PROXY goes into lima.yaml's env (ssh sessions, which
#      covers the nerdctl CLI) and into /etc/environment (the user manager
#      builds its environment from it at boot, which covers the rootless
#      containerd daemon that serves pulls).
#
# Re-running is a no-op; run it again after `limactl delete` + recreate.
set -euo pipefail

LIMA_INSTANCE="${LIMA_INSTANCE:-default}"
REGISTRY_HOST="${REGISTRY_HOST:-air-registry.localhost}"
ROOT_CRT="${CADDY_ROOT_CRT:-$HOME/Library/Application Support/Caddy/pki/authorities/local/root.crt}"
LIMA_YAML="$HOME/.lima/$LIMA_INSTANCE/lima.yaml"
CA_DEST="/usr/local/share/ca-certificates/air-registry-caddy.crt"
export LIMA_INSTANCE

if ! command -v limactl >/dev/null 2>&1; then
	echo "error: limactl not found; install lima first" >&2
	exit 1
fi

if [ ! -f "$ROOT_CRT" ]; then
	echo "error: Caddy root cert not found: $ROOT_CRT" >&2
	echo "run \`just caddy\` once to create the local CA, then retry" >&2
	exit 1
fi

if [ ! -f "$LIMA_YAML" ]; then
	echo "error: Lima config not found: $LIMA_YAML" >&2
	exit 1
fi

if ! lima true 2>/dev/null; then
	echo "error: Lima instance '$LIMA_INSTANCE' is not running" >&2
	echo "start it with: limactl start $LIMA_INSTANCE" >&2
	exit 1
fi

changed=0

# --- 1+2: /etc/hosts entry and Caddy root CA, inside the VM ------------------

HOST_IP=$(lima sh -c 'getent hosts host.lima.internal | awk "NR==1{print \$1}"')
if [ -z "$HOST_IP" ]; then
	echo "error: cannot resolve host.lima.internal inside the VM" >&2
	exit 1
fi

hosts_result=$(lima sh -s <<EOS
set -e
if grep -qE "(^|[[:space:]])${REGISTRY_HOST}([[:space:]]|\$)" /etc/hosts; then
	current=\$(grep -E "(^|[[:space:]])${REGISTRY_HOST}([[:space:]]|\$)" /etc/hosts | awk '{print \$1}' | head -1)
	if [ "\$current" != "${HOST_IP}" ]; then
		sudo sed -i -E "s/^[^[:space:]]+[[:space:]]+${REGISTRY_HOST}([[:space:]]|\$)/${HOST_IP} ${REGISTRY_HOST}\\1/" /etc/hosts
		echo updated
	fi
else
	echo "${HOST_IP} ${REGISTRY_HOST}" | sudo tee -a /etc/hosts >/dev/null
	echo added
fi
EOS
)
case "$hosts_result" in
	added | updated)
		echo "/etc/hosts: $REGISTRY_HOST -> $HOST_IP ($hosts_result)"
		changed=1
		;;
esac

if ! lima sudo test -f "$CA_DEST" || ! cmp -s "$ROOT_CRT" <(lima sudo cat "$CA_DEST"); then
	cat "$ROOT_CRT" | lima sudo tee "$CA_DEST" >/dev/null
	lima sudo update-ca-certificates >/dev/null
	echo "installed Caddy root CA into the VM trust store"
	changed=1
fi

# --- 3a: NO_PROXY in /etc/environment, inside the VM --------------------------

if ! lima sh -c "grep -qE '^NO_PROXY=.*${REGISTRY_HOST}' /etc/environment"; then
	printf 'NO_PROXY="%s,localhost,127.0.0.1"\nno_proxy="%s,localhost,127.0.0.1"\n' \
		"$REGISTRY_HOST" "$REGISTRY_HOST" | lima sudo tee -a /etc/environment >/dev/null
	echo "/etc/environment: added NO_PROXY for $REGISTRY_HOST"
	changed=1
fi

# --- 3b: NO_PROXY in lima.yaml env (host side) --------------------------------

if grep -q "${REGISTRY_HOST}" "$LIMA_YAML" && grep -qi "no_proxy.*${REGISTRY_HOST}" "$LIMA_YAML"; then
	: # already present
elif grep -qE '^env:' "$LIMA_YAML"; then
	echo "note: $LIMA_YAML already has an env: section" >&2
	echo "add the following under env: manually, then restart the instance:" >&2
	echo "  NO_PROXY: \"$REGISTRY_HOST,localhost,127.0.0.1\"" >&2
	echo "  no_proxy: \"$REGISTRY_HOST,localhost,127.0.0.1\"" >&2
else
	cat >>"$LIMA_YAML" <<EOF

# The local dev registry ($REGISTRY_HOST) is unreachable through the
# host-propagated HTTP(S) proxy, so bypass the proxy for it.
env:
  NO_PROXY: "$REGISTRY_HOST,localhost,127.0.0.1"
  no_proxy: "$REGISTRY_HOST,localhost,127.0.0.1"
EOF
	echo "$LIMA_YAML: added env NO_PROXY"
	changed=1
fi

# --- restart so the new env reaches sessions and the daemon -------------------

if [ "$changed" -eq 1 ]; then
	echo
	read -r -p "Restart the Lima instance now to apply? [y/N] " reply || reply=""
	if [ "$reply" = "y" ] || [ "$reply" = "Y" ]; then
		limactl stop "$LIMA_INSTANCE"
		limactl start "$LIMA_INSTANCE"
		echo
		echo "done; try: nerdctl pull $REGISTRY_HOST:8443/library/busybox:latest"
	else
		echo "skipped; apply later with: limactl stop $LIMA_INSTANCE && limactl start $LIMA_INSTANCE"
	fi
else
	echo "already configured; nothing to do"
fi
