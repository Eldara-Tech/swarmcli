![CI](https://github.com/Eldara-Tech/swarmcli/actions/workflows/ci.yml/badge.svg)

<br/>

<div align="center">
  <br />
  <img src="assets/logo-black.svg" alt="SwarmCLI Logo" width="300">
  <h1>Swarm Management at the Speed of Thought</h1>

  <p>
    <strong>The terminal-UI for Docker Swarm you’ve been waiting for. Fast, keyboard-driven, and open source.</strong>
  </p>

  <p>
    <a href="https://github.com/Eldara-Tech/swarmcli/actions"><img src="https://img.shields.io/github/actions/workflow/status/Eldara-Tech/swarmcli/ci.yml?branch=main&style=flat-square&logo=github" alt="Build Status"></a>
    <a href="https://goreportcard.com/report/github.com/Eldara-Tech/swarmcli"><img src="https://img.shields.io/badge/go%20report-A+-brightgreen?style=flat-square" alt="Go Report Card"></a>
    <a href="https://github.com/Eldara-Tech/swarmcli/releases"><img src="https://img.shields.io/github/v/release/Eldara-Tech/swarmcli?style=flat-square&color=re-blue" alt="Latest Release"></a>
    <a href="https://github.com/Eldara-Tech/swarmcli/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue?style=flat-square" alt="License"></a>
  </p>

  <p>
    <a href="#why-swarmcli">Why SwarmCLI?</a> •
    <a href="#quickstart">Quickstart</a> •
    <a href="#features">Features</a> •
    <a href="#installation">Installation</a> •
    <a href="CONTRIBUTING.md">Contributing</a> •
    <a href="https://swarmcli.io/">Homepage</a>
  </p>
</div>

---

## Visual Feast

![alt text](assets/swarmcli.gif)

## Why SwarmCLI?

In the Kubernetes world, **k9s** is the gold standard for cluster management. We believe Docker Swarm users deserve the same level of quality, speed, and intuition.

**"Because Swarm is not dead, it's just efficient. And it deserves better tools."**

SwarmCLI translates the complexity of Docker Swarm into a sleek, keyboard-centric terminal UI. It’s built for developers who want to manage their clusters without leaving the terminal or waiting for heavy Web UIs to load.

## Quickstart

Get up and running in seconds:

```bash
# macOS/Linux via Homebrew
brew tap eldara-tech/tap
brew install swarmcli

# Run it
swarmcli
```

`swarmcli` is the full build: everything below, plus Business Edition's
features, which stay inert until a license verifies. For the wholly Apache-2.0
build, install `swarmcli-oss` instead — same executable name, same commands. See
[docs/editions.md](docs/editions.md), and `swarmcli version` to check which one
you have.

## Features

- **Real-time Observability**: Live monitoring of Services, Tasks, Nodes, and Containers.
- **Stack Awareness**: Navigate your cluster hierarchically (Stacks > Services > Tasks).
- **Instant Logs**: No more `docker service logs -f`. Just press `l` on any service.
- **Secrets & Configs**: Manage, rotate, and — with [Business Edition](#business-edition) — **reveal** secrets for debugging.
- **Management Actions**: Scale, restart, remove, and update services with single keystrokes.
- **Chart Package Manager**: Helm-like packaging for Swarm — install, upgrade, roll back, and diff releases from chart repositories via `swarmcli charts`, or converge the swarm to a committed release file with `swarmcli charts apply`.
- **Zero Config**: Works out-of-the-box with your local Docker engine or remote via SSH/Contexts.
- **Lightweight**: Built with Go. Single static binary (< 20MB). Zero dependencies.

## Charts (CLI)

A bare `swarmcli` launches the TUI, but a few commands run non-interactively
when arguments are supplied: `swarmcli version`, `swarmcli help`, and the
Helm-like chart package manager `swarmcli charts <command>`:

<!-- BEGIN generated: charts commands -->
```bash
# Repository
swarmcli charts repo add <name> <url>           # Add a chart repository and download its index
swarmcli charts repo list                       # List configured repositories
swarmcli charts repo update [name]              # Refresh repository indexes (all, or one)
swarmcli charts repo remove <name>              # Remove a repository

# Discovery
swarmcli charts search [keyword]                # Search charts across repositories
swarmcli charts show chart <repo/chart>         # Show chart metadata
swarmcli charts show values <repo/chart>        # Show default values.yaml
swarmcli charts show schema <repo/chart>        # Show values.schema.json

# Authoring
swarmcli charts lint <chart>                    # Check a chart without deploying it

# Releases
swarmcli charts template <release> <chart>      # Render manifest to stdout (no deploy)
swarmcli charts install <release> <chart>       # Install a chart as a release
swarmcli charts upgrade <release> <chart>       # Upgrade a release to a new revision
swarmcli charts uninstall <release>             # Remove a release (keeps volumes)
swarmcli charts rollback <release> <rev>        # Re-deploy the contents of a past revision
swarmcli charts history <release>               # Show a release's revision history
swarmcli charts prune [release]                 # Delete old revisions beyond --history-max
swarmcli charts get values|manifest <release>   # Show stored values or rendered manifest
swarmcli charts diff upgrade <release> <chart>  # Preview manifest changes before upgrading
swarmcli charts list                            # List releases (alias: ls)
swarmcli charts status <release>                # Show release status and services

# GitOps
swarmcli charts apply -f <file>                 # Converge the swarm to a declarative release file
swarmcli charts outdated                        # Show releases with a newer chart version available
```
<!-- END generated -->

Every command takes `--help`, which lists that command's own options —
anything else it does not read is rejected rather than quietly ignored:

```bash
swarmcli charts install --help
swarmcli charts apply -f swarmcli-release.yaml --diff   # preview, never deploys
```

The TUI has a **read-only** browser for what those commands installed: `:charts`
lists every release with its revision, recorded status and live rollout health,
expands one in place to its revision history and services, and diffs any two
consecutive revisions. Everything that changes a release stays on the command
line above; the view names the command for you.

`apply` reads a release file pinning each release to a chart version, so the
deployed state is reproducible and an automated updater (e.g. Renovate) has
something concrete to bump. It never removes a release the file does not mention —
it reports those instead. Releases can declare a `wave`, which groups the ones
that go out together and makes each group converge before the next begins — so a
migration that fails stops what depends on it from ever starting. See
[charts/README.md](charts/README.md#declarative-releases-gitops).

A chart can declare the swarmcli it needs (`swarmcliVersion: ">= 1.13.0"` in
`Chart.yaml`). Installing it on an older build fails naming the version to
upgrade to, instead of breaking somewhere inside the template; `--skip-compat-check`
proceeds anyway. See [charts/README.md](charts/README.md#declaring-the-swarmcli-a-chart-needs).

Run `swarmcli charts --help` for the full command and option reference.

## Business Edition

The Community Edition is the full open-source TUI. **Business Edition** is a
commercial superset that adds:

- `:bootstrap` — one-command deploy of an mTLS-fronted RBAC proxy and
  per-node agent stack onto your existing Swarm.
- Per-user RBAC, identity by client certificate, role-gated mutation and
  exec.
- Thirteen licensed entitlements. Eight in this TUI — shell into a running
  service task, reveal-secret, port-forward, volumes across all nodes, RBAC
  user management, service health, pull progress and container statistics —
  and five more consumed by swarmcli-cd, because one license covers both
  products. See
  [docs/features.md](docs/features.md#where-the-gates-live).

The license that turns them on need not be a paid one: the [free
tier](docs/license.md#the-free-tier) grants the same features on a swarm of up
to three nodes.

The binary on disk is named `swarmcli` (BE is a strict superset of CE — same
binary name, expanded feature set), so existing scripts and aliases continue to
work.

It is not a separate download. The `swarmcli` archives, image, cask and Scoop
manifest published from this repository's releases *are* the Business Edition
build, carrying the licensed code inert; a license turns it on. The wholly
Apache-2.0 build ships beside it as `swarmcli-oss`. Which one you have is
[docs/editions.md](docs/editions.md), and `swarmcli version` answers it
directly.

Documentation lives in [`docs/`](docs/) — [installation](docs/installation.md),
[license](docs/license.md), [bootstrap](docs/bootstrap.md),
[RBAC](docs/rbac.md), [features](docs/features.md), [volumes](docs/volumes.md),
[configuration](docs/configuration.md) and
[troubleshooting](docs/troubleshooting.md). License sign-up is at
[swarmcli.io/be](https://swarmcli.io/be).

## Installation

<!-- ### Docker (No Installation)

```bash
docker run --rm -it \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/eldara-tech/swarmcli:latest
``` -->

### Build from Source

```bash
git clone https://github.com/Eldara-Tech/swarmcli.git
cd swarmcli
go install
```

## Using Docker container to build and run locally

```
docker build -t swarmcli-dev .
docker run --rm -it -v "$PWD":/app -v /var/run/docker.sock:/var/run/docker.sock  -w /app swarmcli-dev
```

or with docker compose:

```
docker compose run --build --rm swarmcli
```

Then run:

```
go run .
```

## Logging

```bash
# Production (default)
$ go run .
# → writes JSON logs to  ~/.local/state/swarmcli/app.log

# Development
$ SWARMCLI_ENV=dev go run .
# → writes pretty logs to ~/.local/state/swarmcli/app-debug.log

# Optional: control verbosity
$ LOG_LEVEL=debug SWARMCLI_ENV=dev go run .
```

### Environment variables

Every environment variable and on-disk path, for both editions, is documented in
[docs/configuration.md](docs/configuration.md) — `SWARMCLI_ENV` and `LOG_LEVEL`
above among them.

On startup, SwarmCLI makes one request. It checks whether a newer release is available, and reports that swarmcli was started — a random install id, the version and edition, your OS and architecture, how swarmcli was installed, and the shape of your swarm as counts (nodes, managers, services, Docker version). It does not send the *names* of anything: no services, images, stacks, nodes, hostnames, command arguments or IP address. Your country is derived from the connection and the address is never stored. The first run says all of this on screen before anything is sent.

`SWARMCLI_TELEMETRY=off` stops the usage report and keeps the update check. `SWARMCLI_TELEMETRY=none` makes no outbound request at all. Full detail in [docs/license.md](docs/license.md#usage-reporting).

Colorize log tails. Not perfect but simple:

```bash
sudo apt install ccze
tail -f ~/.local/state/swarmcli/app-debug.log | ccze -A
```

### Integration tests

The logs for the integration tests can be enabled with:

```bash
TEST_LOG=1 ./test-setup/testenv.sh test
```

## Key Bindings

| Key      | Action                  |
| :------- | :---------------------- |
| `?`      | Show Help / Cheat Sheet |
| `:stack` | Navigate to Stacks      |
| `:svc`   | Navigate to Services    |
| `:node`  | Navigate to Nodes       |
| `:config`  | Navigate to Config    |
| `:secret`  | Navigate to Secret    |
| `:network` | Navigate to Networks  |
| `:volume`  | Navigate to Volumes   |
| `:charts`  | Browse chart releases |
| `l`      | View Logs               |
| `s`      | Scale Service           |
| `r`      | Restart Service         |
| `ctrl-c` | Quit                    |

## Project Hygiene

Impeccable project hygiene is the backbone of a thriving ecosystem.

- **[Contributing](CONTRIBUTING.md)**: We ❤️ contributions! Check our guide to get started.
- **[Security](SECURITY.md)**: Found a bug? Let us know securely.
- **[Code of Conduct](CODE_OF_CONDUCT.md)**: Expectations for our community.
- **[License](LICENSE)**: Apache 2.0.

## Releasing

Releases are fully automated — **no manual version file to update**. The git tag is the single source of truth.

```bash
git tag v1.5.0
git push origin v1.5.0
```

Pushing a `v*` tag triggers the [release workflow](.github/workflows/release.yml) which:

1. Injects the tag into the binary via GoReleaser ldflags (`-X main.version=1.5.0`)
2. Builds for Linux, macOS, Windows, and FreeBSD (multiple architectures)
3. Publishes a GitHub release with auto-generated changelog
4. Pushes a multi-arch Docker image to Docker Hub
5. Updates the [Homebrew tap](https://github.com/Eldara-Tech/homebrew-tap) and [Scoop bucket](https://github.com/Eldara-Tech/scoop-bucket)

A tag here publishes the **`oss` half** of a release — `swarmcli_*_oss`
archives, `checksums-oss.txt`, `eldaratech/swarmcli:<tag>-oss`, and the
`swarmcli-oss` cask and manifest. The unsuffixed names and `:latest` belong to
the Business Edition build, which is tagged in a second repository at the same
version and published into this same GitHub release. [RELEASING.md](RELEASING.md)
is the procedure; [docs/editions.md](docs/editions.md) is what the two artefacts
are.

The in-app `Version:` header reads from the injected value. Local builds without GoReleaser show `dev`.

---

<div align="center">
  <sub>Built by the community for the community. Made with ❤️ for the Docker Swarm community.</sub>
</div>
