<div align="center">
  <br />
  <img src="assets/logo-black.svg" alt="SwarmCLI" width="300">
  <h1>Swarm management at the speed of thought</h1>
  <p><strong>A terminal UI for Docker Swarm: keyboard-driven, single binary, open source.</strong></p>
  <p>
    <a href="https://github.com/Eldara-Tech/swarmcli/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/Eldara-Tech/swarmcli/ci.yml?branch=main&style=flat-square&logo=github" alt="CI"></a>
    <a href="https://goreportcard.com/report/github.com/Eldara-Tech/swarmcli/v2"><img src="https://goreportcard.com/badge/github.com/Eldara-Tech/swarmcli/v2?style=flat-square" alt="Go Report Card"></a>
    <a href="https://github.com/Eldara-Tech/swarmcli/releases"><img src="https://img.shields.io/github/v/release/Eldara-Tech/swarmcli?style=flat-square" alt="Latest release"></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-blue?style=flat-square" alt="Apache 2.0"></a>
  </p>
  <p>
    <a href="#install">Install</a> •
    <a href="#first-run">First run</a> •
    <a href="#what-you-can-do">What you can do</a> •
    <a href="#charts">Charts</a> •
    <a href="#business-edition">Business Edition</a> •
    <a href="docs/">Docs</a> •
    <a href="https://swarmcli.io/">swarmcli.io</a>
  </p>
</div>

<p align="center">
  <a href="assets/swarmcli.gif"><img src="assets/swarmcli-screenshot.png" alt="SwarmCLI showing a stack's services, their tasks, and the node each runs on" width="800"></a>
  <br /><sub>Click for the animated tour.</sub>
</p>

In the Kubernetes world, `k9s` is how people run a cluster from a terminal. Docker Swarm never had that. SwarmCLI is it: one keyboard-driven screen for stacks, services, tasks, nodes, networks, secrets, configs and volumes, with logs across replicas one key away and the reason a task is stuck shown instead of buried in JSON. It talks to the Docker socket or context you already use, so there is nothing to deploy on the swarm.

## Install

Every channel installs the same `swarmcli` binary. Linux (amd64, arm64, armv6, armv7, 386), macOS (universal), Windows (amd64, arm64, 386) and FreeBSD.

```bash
# Script (Linux, macOS)
curl -fsSL https://swarmcli.io/install.sh | sh

# Homebrew (macOS, Linux)
brew install Eldara-Tech/tap/swarmcli

# Scoop (Windows)
scoop bucket add eldara https://github.com/Eldara-Tech/scoop-bucket
scoop install swarmcli

# Docker
docker run --rm -it \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$HOME/.docker/contexts:/root/.docker/contexts" \
  -v "$HOME/.config/swarmcli:/root/.config/swarmcli" \
  eldaratech/swarmcli:latest

# Release archive, with checksum
sha256sum -c checksums-merged.txt --ignore-missing
tar -xzf swarmcli_Linux_x86_64.tar.gz && install -m 0755 swarmcli /usr/local/bin/
```

`swarmcli` is the full build: everything on this page, with the [Business Edition](#business-edition) features compiled in and inert until a licence turns them on. If you want a build that contains nothing but Apache-2.0 code, install `swarmcli-oss` instead; same command name, same everything else. `swarmcli version` tells you which one you have, and [docs/editions.md](docs/editions.md) explains the two. Upgrading is `brew upgrade swarmcli`, `scoop update swarmcli` or a new `docker pull`; the licence lives in the swarm, not the binary, so nothing needs re-activating. Full detail: [docs/installation.md](docs/installation.md).

## First run

SwarmCLI needs a Docker context that reaches a swarm manager. Locally, that is the socket; for a remote swarm, a context:

```bash
docker context create prod --docker "host=ssh://ops@manager1.example.com"
docker context use prod
swarmcli
```

It starts as the Community Edition with no prompt. Every feature, on a swarm of up to three nodes, is one command away and needs no card:

```bash
swarmcli license activate
```

The CLI prints a short code and a link, you confirm in the browser, and the key is installed into the swarm itself, so it follows the context rather than the machine. Larger swarms take a paid licence the same way; [docs/license.md](docs/license.md) is the whole story, from the free tier to moving a licence to a rebuilt swarm.

If the screen looks wrong, set `TERM` to a 256-colour value such as `xterm-256color`.

## What you can do

- **See the whole swarm**: stacks, then services, then tasks, then containers, with the node each task runs on and its last state change.
- **Read the real error**: a task stuck in Pending shows the scheduler's reason, a resource shortfall or a constraint no node matches, where `docker service ps` truncates it.
- **Logs across replicas**, interleaved, with a separator you can drop in before a deploy.
- **Act with one key**: scale, restart, roll back, inspect, edit a stack and save its YAML back to disk.
- **Secrets and configs**: create, rotate, see which services use each and which are orphaned.
- **Nodes, networks and volumes**: labels, availability, promote and demote, which overlay is attached where.
- **Charts**: a Helm-style package manager for Swarm, below.
- **Remote and multi-swarm** through Docker contexts, and through the RBAC proxy for teams.

| Key                     | Does                                                                                                        |
| ----------------------- | ----------------------------------------------------------------------------------------------------------- |
| `?`                     | Help and the full key list for the current view                                                             |
| `:`                     | Command mode: `:stack`, `:svc`, `:node`, `:network`, `:secret`, `:config`, `:volume`, `:charts`, `:license` |
| `j` `k`, `Enter`, `Esc` | Move, drill in, go back                                                                                     |
| `/`                     | Filter the view                                                                                             |
| `l`                     | Logs for the selected service or task                                                                       |
| `s` `r`                 | Scale, restart                                                                                              |
| `d`                     | Remove the selected entity                                                                                  |
| `x`                     | Reveal a secret, or shell into a task (Business Edition)                                                    |
| `Ctrl+Q`                | Quit                                                                                                        |

The tour above shows most of these. The site's [command reference](https://swarmcli.io/blog/docker-swarm-commands-reference) maps every `docker` verb to the SwarmCLI equivalent.

## Charts

`swarmcli charts` is a package manager for Swarm stacks in the shape of Helm: a repository with an index, versioned chart archives, `values.yaml` overrides, revisions you can roll back to, and a release file for GitOps.

```bash
swarmcli charts repo add swarmcli-charts https://eldara-tech.github.io/swarmcli-charts
swarmcli charts search postgres
swarmcli charts install db swarmcli-charts/postgres --set resources.limits.memory=2G
swarmcli charts upgrade db swarmcli-charts/postgres --version 0.2.2 --wait
swarmcli charts rollback db 1
swarmcli charts apply -f releases.yaml --diff     # converge the swarm to a committed file; preview first
```

Chart archives are verified against the digest the index publishes, a chart can declare the SwarmCLI version it needs, and `:charts` in the TUI browses what is installed with each release's revision history and rollout health. Every command takes `--help`. The full reference, the chart format and the release-file format are in [charts/README.md](charts/README.md); the public repository is [Eldara-Tech/swarmcli-charts](https://github.com/Eldara-Tech/swarmcli-charts).

## Business Edition

The same binary, with more of it switched on by a licence:

- **`:bootstrap`** deploys an mTLS-fronted RBAC proxy and a per-node agent onto the swarm in one command, and a managed context that carries the TLS material for you.
- **Per-user identity, roles and an audit log** through the proxy: viewer, operator and admin, scoped to the stacks a team owns, with one-time onboarding links instead of certificate ceremonies.
- **Shell into a task, port-forward into an overlay network, reveal a secret, volumes across every node, live service health, pull progress and container statistics.**

The free tier grants all of it on a swarm of up to three nodes. Above that, licences are priced per node at [swarmcli.io/be](https://swarmcli.io/be), bound to one swarm, movable to a new one from the customer portal, and valid for up to 60 days without contact so an outage on our side is never one on yours. Air-gapped swarms activate from a file.

Documentation: [installation](docs/installation.md), [license](docs/license.md), [bootstrap](docs/bootstrap.md), [RBAC](docs/rbac.md), [features](docs/features.md), [volumes](docs/volumes.md), [configuration](docs/configuration.md), [troubleshooting](docs/troubleshooting.md). The proxy has its own repository, [swarmcli-rbac-proxy](https://github.com/Eldara-Tech/swarmcli-rbac-proxy).

## Going further

- [The Definitive Docker Swarm Guide](https://swarmcli.io/blog/The-Definitive-Docker-Swarm-Guide-for-2026), setup to production.
- [Docker Swarm security hardening](https://swarmcli.io/blog/docker-swarm-security-hardening-guide), the checklist the proxy fits into.
- [Docker Compose to Docker Swarm](https://swarmcli.io/blog/docker-compose-to-swarm-migration-guide-2026), what changes in the file.
- [Changelog](https://swarmcli.io/blog/swarmcli-changelog) for every release of SwarmCLI, the proxy and the charts, and the [release notes](https://github.com/Eldara-Tech/swarmcli/releases) for the full detail.

## Contributing and community

- [CONTRIBUTING.md](CONTRIBUTING.md): building from source, the dev container, logging and environment variables, integration tests.
- [RELEASING.md](RELEASING.md): how a tag becomes a release, and which artefacts each half of a release publishes.
- [SECURITY.md](SECURITY.md) for reporting a vulnerability, and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
- Licence: [Apache 2.0](LICENSE) for this repository. The licensed code that the full build carries is proprietary; [docs/editions.md](docs/editions.md) draws the line exactly.

<div align="center"><sub>Built for the Docker Swarm community.</sub></div>
