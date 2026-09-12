<!--
SPDX-License-Identifier: Apache-2.0
Copyright © 2026 Eldara Tech
-->

# SwarmCLI — Documentation

These pages cover installing, licensing and operating SwarmCLI, including its
Business Edition features. For TUI navigation, key bindings, search and
contexts, start at the [README](../README.md); for what the two
published artefacts are, see [Editions](editions.md).

Business Edition is not a separate program — it is the same binary with
features a license unlocks. On top of the Community Edition it adds:

- **`:bootstrap`** — one-command deploy of an mTLS-fronted RBAC proxy and a
  per-node agent stack onto your existing Swarm.
- **Per-user RBAC** — identity by client certificate, role-based gating of
  destructive and exec endpoints.
- **Thirteen licensed entitlements.** Eight are this TUI's — interactive shell
  into a running service (`x` on a service), reveal-secret (`x` on a secret),
  port-forward, volume management across all swarm nodes, RBAC user and role
  management, service health, pull progress and container statistics. The
  other five are consumed by swarmcli-cd, since one license covers both
  products. [Features — Where the gates
  live](features.md#where-the-gates-live) is the list.

The license that unlocks them need not be a paid one: the [free
tier](license.md#the-free-tier) grants the same features on a swarm of up to
three nodes.

## Contents

- [Editions](editions.md) — the two artefacts one tag publishes, and which one you have.
- [Installation](installation.md) — install channels, first run, edition check.
- [License](license.md) — the tiers including the free one, acquiring a key, activation paths, grace period.
- [Bootstrap](bootstrap.md) — `:bootstrap` end-to-end: what gets deployed and how.
- [Migration](migration.md) — moving an existing stack to application-layer mTLS (`:bootstrap --migrate`).
- [RBAC](rbac.md) — managing users, roles, onboarding, and revocation.
- [Features](features.md) — shell, reveal-secret, port-forward and container statistics in detail.
- [Volumes](volumes.md) — listing across nodes, create/delete/prune, in-volume file browser.
- [Charts](../charts/README.md) — the chart package manager both editions ship: repositories, install/upgrade, and declarative releases (`charts apply`, `charts outdated`).
- [Configuration](configuration.md) — environment variables and on-disk paths for both editions.
- [Troubleshooting](troubleshooting.md) — common errors and fixes.

For install commands see [Installation](installation.md), or the
[repository README](../README.md) for a quickstart.

### For contributors

- [Architecture](architecture.md) — the package map, how views and commands
  self-register, and which seams the Business Edition attaches to.
- [CONTRIBUTING.md](../CONTRIBUTING.md) — building, testing, and opening a pull
  request.

## Reporting issues

Bugs and feature requests for either edition belong in this repository's
[issue tracker](https://github.com/Eldara-Tech/swarmcli/issues). **Security
vulnerabilities do not** — see [SECURITY.md](../SECURITY.md) and email
hello@eldara.io rather than opening a public issue.
- [swarmcli-agent](https://github.com/Eldara-Tech/swarmcli-agent) — per-node agent and agent-manager (deployed by `:bootstrap`).
- [swarmcli.io](https://swarmcli.io) — product home and license sign-up.
