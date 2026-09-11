<!--
SPDX-License-Identifier: Apache-2.0
Copyright © 2026 Eldara Tech
-->

# Installation

SwarmCLI Business Edition ships as a single static Go binary. The executable
on disk is named **`swarmcli`** — not `swarmcli-be` — because BE is a strict
superset of CE: the OSS feature set is included unchanged. Aliases, scripts,
and CI invocations from a CE installation continue to work without
modification when you switch to BE.

The package/formula/image names are `swarmcli` for this build and
`swarmcli-oss` for the wholly Apache-2.0 one, so the package manager can offer
both artefacts side by side. They install the same binary name, so **you can
have either at any time, but not both**. See [CE and BE are one
download](#ce-and-be-are-one-download) below. The older `swarmcli-be` formula,
manifest and image were renamed on 2026-08-07 and retired on 2026-09-11 — see
[The retired `swarmcli-be` names](#the-retired-swarmcli-be-names).

## Channels

### Homebrew (macOS / Linux)

```bash
brew install Eldara-Tech/tap/swarmcli
```

The formula is hosted at
[Eldara-Tech/homebrew-tap](https://github.com/Eldara-Tech/homebrew-tap).
It conflicts with the `swarmcli-oss` cask and with the retired `swarmcli-be`
one; brew will refuse to install two at once and will offer to remove the
other.

### Scoop (Windows)

```powershell
scoop bucket add eldara https://github.com/Eldara-Tech/scoop-bucket
scoop install swarmcli
```

### Docker

```bash
docker pull eldaratech/swarmcli:latest
docker run --rm -it \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$HOME/.docker/contexts:/root/.docker/contexts" \
  -v "$HOME/.config/swarmcli:/root/.config/swarmcli" \
  eldaratech/swarmcli:latest
```

The container runs as root (matching the upstream `docker:N-cli` base, so
the mounted Docker socket works without `--group-add`). Mounting
`~/.docker/contexts` and `~/.config/swarmcli` keeps your Docker contexts and
your bootstrap certificates persistent across container runs. The license is
**not** among them — it lives in the swarm's own Raft state, as the Docker
config `swarmcli-license`, so it is already persistent and follows the context
rather than the container (see
[License — Per-swarm storage](license.md#per-swarm-storage)).

Available tags: `latest`, `vX.Y.Z`. Multi-arch images cover `linux/amd64`
and `linux/arm64`.

### Raw binary

Download platform archives from the
[Releases page](https://github.com/Eldara-Tech/swarmcli/releases).
Each release ships:

- `swarmcli_<Os>_<arch>.tar.gz` (Linux/macOS/FreeBSD) or `.zip` (Windows) —
  this build. There is no version component in the name, the OS is capitalised,
  and `amd64` is written `x86_64`: `swarmcli_Linux_x86_64.tar.gz`.
- `swarmcli_<Os>_<arch>_oss.tar.gz` — the wholly Apache-2.0 artefact, see
  [Editions](editions.md).
- `checksums-merged.txt` — SHA-256 of this build's archives, and
  `checksums-oss.txt` for the OSS ones. There is deliberately no unqualified
  `checksums.txt`: a release carries two sets of artefacts, and one file would
  not say which set it verified.

Verify and install:

```bash
sha256sum -c checksums-merged.txt --ignore-missing
tar -xzf swarmcli_Linux_x86_64.tar.gz
install -m 0755 swarmcli /usr/local/bin/swarmcli
```

Build matrix per release:

| OS | Architectures |
|---|---|
| Linux | amd64, arm64, arm (v6, v7), 386 |
| macOS | universal (amd64 + arm64) |
| Windows | amd64, arm64, 386 |

## First run

```bash
swarmcli
```

With no license, swarmcli starts normally as the Community Edition — there is
no startup prompt. The status bar says so, and the license dialog appears
just-in-time, the first time you ask for a feature a license gates. The fastest
way to a licensed swarm is one command:

```bash
swarmcli license activate
```

which covers the [free tier](license.md#the-free-tier) as well as a paid plan.
[License — Start to finish](license.md#start-to-finish) is the whole sequence,
and the detail of every state the dialog can show is in [License — The license
prompt](license.md#the-license-prompt).

If your terminal does not look right (colors, key handling), check that
your `TERM` is set to a 256-color value — SwarmCLI expects `xterm-256color`
or equivalent.

## Verifying version and edition

`swarmcli version` is the direct answer, and `--version` and `-v` are accepted
as aliases for it. Two more ways to check:

- **Inside the TUI**: the version is shown in the header bar; `:license`
  additionally shows license status (see [License](license.md)).
- **In the log file**: every run writes one startup line. After running
  the binary at least once, look for the most recent entry:

```bash
grep -o 'swarmcli[^"]*version=[^ "]*' ~/.local/state/swarmcli/app.log \
  | tail -n 1
# → swarmcli-be version=v1.4.0       ← BE binary
# → swarmcli version=v1.4.0          ← CE binary (also has edition=ce)
```

The log-line prefix (`swarmcli-be ` vs `swarmcli `) says which build you have,
regardless of whether a licence verified. `swarmcli version` says the same
thing on stdout:

```console
$ swarmcli version
1.14.0 (business build, chart engine 1.14.0)
```

## CE and BE are one download

There is no separate Business Edition download to switch to. One release
publishes two artefacts under one tag, and the plain name is the full product:

| | `swarmcli` | `swarmcli-oss` |
|---|---|---|
| License | this repository's code is Apache 2.0; the licensed code is commercial | wholly Apache 2.0 |
| Contains | everything, with Business Edition features **inert** until a licence verifies | the Community Edition, and nothing else |
| Binary on disk | `swarmcli` | `swarmcli` (same name) |
| Get it | `brew install swarmcli`, `scoop install swarmcli`, `eldaratech/swarmcli:<tag>` | `swarmcli-oss` on either, or `eldaratech/swarmcli:<tag>-oss` |

So **activating Business Edition is installing a licence, not installing a
different program.** If you already run `swarmcli` from the Homebrew tap, the
Scoop bucket, the Docker image or a release archive, you have the build a key
unlocks — see [License](license.md).

`swarmcli-oss` exists for anyone who needs an artefact that is verifiably open
source: a distribution packager, an air-gapped compliance review, or a
contributor checking what they built. It is not a cut-down edition — it is the
whole Community Edition. [Editions](editions.md) is the fuller account,
including how the build proves which one it is.

### The retired `swarmcli-be` names

Before the editions split, Business Edition shipped as separate `swarmcli-be`
packages and an `eldaratech/swarmcli-be` image. They carried the same build
under a second name from the rename on 2026-08-07 until they were retired on
2026-09-11. Switch to `swarmcli`; it is the build they were installing.

| If you track | You now see | Switch with |
|---|---|---|
| the `swarmcli-be` cask | `brew install`/`upgrade` fails, naming `swarmcli` | `brew uninstall --cask swarmcli-be && brew install Eldara-Tech/tap/swarmcli` |
| the `swarmcli-be` Scoop manifest | the bucket no longer carries it | `scoop uninstall swarmcli-be; scoop install swarmcli` |
| `eldaratech/swarmcli-be` | the pull fails — the repository is gone | `docker pull eldaratech/swarmcli:latest` |

An install already on disk keeps working, licence included: nothing about the
product changed, only the names it is published under.

## Upgrade

```bash
brew upgrade swarmcli             # Homebrew
scoop update swarmcli             # Scoop
docker pull eldaratech/swarmcli:latest      # Docker
```

The `swarmcli-be` forms of these commands no longer work — see [The retired
`swarmcli-be` names](#the-retired-swarmcli-be-names) for the one-line switch.

For binary installs, replace the file on disk with the new archive's
contents.

**swarmcli does not update itself.** There is no `update` verb, no background
download, and nothing that replaces the running binary — upgrading is always the
package manager or the file on disk. If you have been told a swarm is running
"the version the auto-updater installed", there is nothing behind that.

What swarmcli does have is a **startup notice**: one request to
`https://swarmcli.io/api/v1/version` at launch, a version badge in the
system-info header, and a dismiss-only modal pointing back at this page. It tells
you a newer release exists; it never fetches one.

(The one thing that *does* renew itself is a managed licence's lease, which is
unrelated to the binary — see [license.md](license.md).)

The compatibility matrix between BE, the bundled CE codebase, and the
agent/rbac-proxy versions is published in each release's notes — see
[Releases](https://github.com/Eldara-Tech/swarmcli/releases).

Upgrading the binary does not change an already-deployed infra stack. To
refresh the agent/rbac-proxy images in place, run `:bootstrap --upgrade`.
A stack first deployed before application-layer mTLS (encrypted `agent-net`)
needs a one-time, non-destructive `:bootstrap --migrate` instead — see
[Migration](migration.md).
