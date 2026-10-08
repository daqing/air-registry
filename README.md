# Air Registry

An [Airway](https://github.com/daqing/airway) application: an OCI-compatible
container image registry with a web UI for searching and browsing images.
Conceptually similar to [Zot](https://zotregistry.dev/), but implemented from
scratch on the Airway framework — no Zot source code, no Zot dependencies.

Passes the official OCI Distribution Spec conformance suite (v1.1.1,
74/74 specs: pull, push, content discovery, content management).

## Features

- **OCI Distribution Spec API** — standard `/v2/` endpoints, so regular OCI
  clients (docker, crane, containerd, oras, …) push and pull without any
  client-side changes:
  - Blob upload (monolithic and chunked, with resume), blob download
  - Manifest PUT/GET/HEAD/DELETE, tag listing with `n`/`last` pagination
  - `DELETE` for blobs, cross-repository blob mount
  - `/v2/_catalog` repository enumeration
  - OCI 1.1 Referrers API (`?artifactType=` filter included) for signatures,
    SBOMs and other artifacts attached via `subject`
- **Storage** — blobs as content-addressed files on disk (under
  `DATA_DIR`, default `data/storage/`); repository, tag, digest and referrer
  metadata in the database via Airway models
- **Garbage collection** — runs automatically after every manifest delete;
  a manual `gc` command sweeps orphaned blobs uploaded without a manifest
- **Web UI** — server-rendered pages to search repositories by name, browse
  them paginated, and inspect image details (tags, layers, platforms,
  annotations, referrers)
- **Authentication** — optional HTTP basic auth on the OCI API: set
  `REGISTRY_AUTH_USERNAME`/`REGISTRY_AUTH_PASSWORD` and clients (docker,
  crane, oras) prompt for the credentials on push/pull; the web UI stays
  public

## Quick start

```bash
cp .env.example .env   # then set AIRWAY_ENV=local, DSN and LISTEN
go run . db:migrate    # create the schema
go run .               # start the server
```

With the defaults from `.env.example` the server listens on
`127.0.0.1:1905`. A typical local `DSN` is
`sqlite://./tmp/registry-dev.db`.

## Using the registry

### docker

The registry speaks plain HTTP, so Docker needs it listed as an insecure
registry. Add the address to `/etc/docker/daemon.json` (Docker Desktop:
*Settings → Docker Engine*):

```json
{
  "insecure-registries": ["192.168.1.10:1905"]
}
```

(`192.168.1.10` is the host running the registry; `localhost` works as-is.)
Then restart docker and push:

```bash
docker tag myapp:v1 192.168.1.10:1905/demo/app:v1
docker push 192.168.1.10:1905/demo/app:v1
docker pull 192.168.1.10:1905/demo/app:v1
```

### Authentication

Basic auth is off by default. Set a username and password in `.env` to turn
it on:

```bash
REGISTRY_AUTH_USERNAME="admin"
REGISTRY_AUTH_PASSWORD="change-me"
```

Only the `/v2/` API is gated; the web UI stays public. The first push or pull
receives a `401` with a Basic challenge, so docker prompts on the terminal:

```text
$ docker push 192.168.1.10:1905/demo/app:v1
Username: admin
Password:
```

`docker login 192.168.1.10:1905` caches the credentials so later pushes and
pulls don't prompt; `crane`/`oras` prompt the same way and `curl -u user:pass`
works too. Auth turns on as soon as both credentials are set;
`REGISTRY_AUTH_ENABLED` is an optional explicit override (`true`/`false`), and
enabling auth without credentials fails closed with `401` rather than exposing
the registry.

### crane / oras

Both work over HTTP against `localhost` directly:

```bash
crane ls localhost:1905/demo/app
crane copy busybox localhost:1905/library/busybox:latest

oras attach localhost:1905/demo/app:v1 sbom.json:application/json \
  --artifact-type application/vnd.example.sbom
oras discover localhost:1905/demo/app:v1
```

### Local HTTPS with Caddy

The repo ships a `Caddyfile` that terminates TLS at
`https://air-registry.localhost:8443` and proxies to the app's `LISTEN`
address (from `.env`, default `127.0.0.1:1905`).
Caddy's internal CA issues and renews the certificate automatically — no
real domain or ACME setup needed:

```bash
brew install caddy   # once
caddy trust          # once: add Caddy's local root CA to the system trust store
just caddy           # run alongside the app
```

Browsers, `crane` and `oras` then work against `air-registry.localhost:8443`
with no insecure-registry config. Docker Desktop's daemon runs in a VM with
its own trust store, so for `docker` run:

```bash
just copy-docker-cert
```

It copies Caddy's root cert into `~/.docker/certs.d` and restarts Docker
Desktop; afterwards `docker push air-registry.localhost:8443/demo/app:v1`
works over HTTPS with no `insecure-registries` entry.

For [Lima](https://lima-vm.io/) (`nerdctl.lima`), the VM needs the same trust
setup plus a proxy bypass: Lima propagates the host's HTTP(S) proxy settings
into the VM without a `NO_PROXY` entry for `*.localhost`, so registry requests
die in the proxy with `EOF`. Run:

```bash
just setup-lima-client
```

It maps `air-registry.localhost` onto the host (`host.lima.internal`), trusts
Caddy's root CA, and adds the `NO_PROXY` entries where both the nerdctl CLI
and the rootless containerd daemon will see them, then restarts the instance.
Afterwards `nerdctl push air-registry.localhost:8443/demo/app:v1` works from
inside the VM. Re-run it after `limactl delete` + recreate.

### Web UI

Open `http://localhost:1905/` — a home page with a search box and the most
recently pushed repositories. `/repos` lists all repositories (paginated,
searchable via `?q=`), and `/repos/<name>` shows a repository's tags; click a
tag to see its manifest (config, layers, total size, annotations) or drill
into an index's platforms. Referrers are listed with links to their manifests.

## Configuration

All settings are environment variables (see `.env.example`):

| Variable | Default | Meaning |
|---|---|---|
| `LISTEN` | `127.0.0.1:1905` | HTTP listen address |
| `DSN` | — | Database DSN, e.g. `sqlite://./tmp/registry-dev.db` |
| `DATA_DIR` | `./data/storage` | Blob storage root (`STORAGE_ROOT` still honored as a legacy fallback) |
| `MAX_UPLOAD_SIZE` | `0` (unlimited) | Per-request blob upload cap in bytes; over the cap uploads fail with 413 |
| `REGISTRY_AUTH_USERNAME` | — | Basic-auth username for the `/v2/` API |
| `REGISTRY_AUTH_PASSWORD` | — | Basic-auth password; setting both credentials enables auth |
| `REGISTRY_AUTH_ENABLED` | follows credentials | Optional explicit override (`true`/`false`); enabled without credentials fails closed with `401` |
| `URL_PREFIX` | — | Mount the web UI and API under a sub-path behind a reverse proxy |

## Garbage collection

Deleting a manifest unlinks the blobs only it referenced and triggers a GC
sweep automatically. Blobs uploaded without ever being referenced (e.g. an
abandoned push) stay on disk until collected manually:

```bash
DSN=sqlite://./tmp/registry-dev.db go run . gc
# gc: kept 12 blobs, deleted 1
```

## Known limitations

- **Single shared credential over plain HTTP** — basic auth protects the API
  with one username/password pair, and over HTTP the header is base64-encoded
  rather than encrypted, so pair it with TLS beyond a trusted LAN. Per-user
  accounts, tokens and read-only/push ACLs are roadmap items.
- **No built-in TLS** — terminate HTTP at a reverse proxy (nginx, Caddy) for
  anything beyond a trusted LAN; remember to remove the docker
  `insecure-registries` entry then.
- **Upload sessions live in memory** — restarting the server discards
  in-progress chunked uploads; clients retry automatically.
- **Single-node, SQLite-first** — no clustering, no event queue; the schema
  works on Postgres/MySQL via Airway, but only SQLite is exercised regularly.
- **No quotas or read-only mode** — repository size limits and pull-only
  toggles are roadmap items.

## Development

```bash
go run .            # serve (local env rebuilds the frontend bundle in memory)
go test ./...       # unit and API tests
go vet ./...
```

The OCI conformance suite (vendored under `deps/distribution-spec`, ignored
by git) can be run against a local instance — see the T20 entry in
[docs/TASKS.md](docs/TASKS.md) for the exact invocation. Task history and
acceptance records live there as well.
