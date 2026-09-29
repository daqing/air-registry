dev:
  overmind start -f Procfile.dev

compose:
  docker-compose up --build

# Local HTTPS in front of the app via Caddy's internal CA (run `caddy trust` once).
# The upstream is the app's LISTEN address from .env (default 127.0.0.1:1905),
# injected as AIR_REGISTRY_UPSTREAM for the Caddyfile.
caddy:
  #!/usr/bin/env bash
  set -euo pipefail
  if [ -f .env ]; then
    set -a
    . ./.env
    set +a
  fi
  : "${AIR_REGISTRY_UPSTREAM:=${LISTEN:-127.0.0.1:1905}}"
  export AIR_REGISTRY_UPSTREAM
  exec caddy run --config Caddyfile

# Copy Caddy's root CA into Docker Desktop so pushes to the HTTPS registry need no insecure-registries entry.
copy-docker-cert:
  ./scripts/copy-docker-cert.sh

# Point a Lima VM's nerdctl at the HTTPS registry: hosts entry, Caddy CA trust, NO_PROXY bypass.
setup-lima-client:
  ./scripts/setup-lima-client.sh

install-deps:
  go install github.com/air-verse/air@latest
  brew install tmux
  brew install overmind

# Regenerate *_templ.go from the .templ views under app/views.
generate:
  go generate ./...

# Regenerate the views, then keep them fresh while you edit .templ files.
generate-watch:
  go tool templ generate -path app/views -watch
