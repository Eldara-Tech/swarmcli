<!--
SPDX-License-Identifier: Apache-2.0
Copyright © 2026 Eldara Tech
-->

# Editions: the two artefacts, and which one you have

Every release publishes **two** artefacts from one tag. They are built from
different trees, they are named differently, and one of them can be unlocked by
a license. This page is what each of them is.

| | Built from | Contains | License |
|---|---|---|---|
| `swarmcli_*` archives, `eldaratech/swarmcli:<tag>`, `:latest`, the `swarmcli` Homebrew cask and Scoop manifest | a private build wrapper around this repository | this repository, plus licensed code that is **inert** without a license | this repository's code is Apache-2.0; the licensed code is proprietary |
| `swarmcli_*_oss` archives, `eldaratech/swarmcli:<tag>-oss`, the `swarmcli-oss` cask and manifest | this repository, and nothing else | this repository, and nothing else | wholly Apache-2.0 |

The command inside both archives is `swarmcli`. Every invocation in these docs,
every script, alias and CI job is identical for the two, deliberately: the
difference between them is what the build contains, not how it is driven.

## Why there are two

The default artefact carries the licensed code because a single binary is the
only arrangement in which "install a license" is not "download a different
product". Nothing is hidden by that — the code is compiled in and does nothing
until a license verifies.

But a released binary that contains proprietary code cannot honestly be called
open source, and a project whose only download is that binary has no answer when
somebody says so. `swarmcli-oss` is the answer, and it only works if it ships
from the first merged release rather than after somebody complains. It is also
the artefact a distribution packager, an air-gapped compliance review and a
contributor verifying what they built actually need.

The OSS build is not a subset with features removed. It is this repository,
which is the whole Community Edition: the TUI, the chart package manager, the
declarative `charts apply` workflow, the CLI. Nothing that has ever shipped
Apache-2.0 is reclaimable; the free line is drawn there or wider, never
narrower.

## Which one am I running

Three signals, in descending order of how much you should trust them.

**`swarmcli version`.** The strongest, because it is a property of the build and
nothing at runtime can change it:

```console
$ swarmcli version
1.14.0 (oss build, chart engine 1.14.0)
```

`(business build)` is the merged artefact — **whether or not a license
verified**. That is exactly what distinguishes "this binary has no licensed
code" from "this binary has no license", and neither the edition label nor the
absence of features answers that alone.

The chart engine is reported beside it because the two can legitimately differ:
the engine is this module's code, so a build that embeds swarmcli as a
dependency carries whichever engine it pinned rather than its own tag. `chart
engine unstamped` means the build's ldflag did not take, and every chart's
declared `swarmcliVersion` floor is being admitted unchecked.

**The startup log line**, for a binary you cannot re-run:

```
swarmcli version=1.14.0 edition=ce commit=… date=…      ← the OSS build
swarmcli-be version=1.14.0 commit=… date=…              ← the merged build
```

The prefix is the signal, and it is written on every start, including
non-interactive `swarmcli charts …` runs.

**The edition label in the TUI header** is the weakest of the three, and is not
a build signal at all: it follows live license state, so the merged build with
no valid license reads *Community Edition* — correctly, because that is what it
is behaving as. Do not use it to answer this question.

## What a license changes, and when

A license is installed into the swarm and read at startup. It turns features on
in the merged build only, and the OSS build has nothing to install one *with*:
the `license` command and the `:license` view are both part of the licensed
code, so `swarmcli license …` exits `2` with `unknown command "license"` and
`:license` is not a command the TUI knows.

A license need not be a paid one: the [free tier](license.md#the-free-tier) is a
license like any other and turns the same features on, on a swarm of up to three
nodes.

The Business Edition documentation covers acquiring, installing and managing a
key, and what each feature does.

## Getting the OSS build

```bash
# The archive, from any release:
curl -sSLO https://github.com/Eldara-Tech/swarmcli/releases/download/v1.14.0/swarmcli_Linux_x86_64_oss.tar.gz

# The image:
docker pull eldaratech/swarmcli:v1.14.0-oss

# Homebrew or Scoop:
brew install Eldara-Tech/tap/swarmcli-oss
scoop install swarmcli-oss
```

There is deliberately no moving `:oss` image tag and no `:latest` on this half.
A deployment that wants the verifiably-Apache-2.0 artefact pins a version, which
is what an air-gapped or compliance-reviewed deployment does anyway — and a
moving tag on the artefact whose whole point is being checkable is a way to be
surprised by what is running.

`checksums-oss.txt` in each release covers these artefacts;
`checksums-merged.txt` covers the merged ones. Neither is named plain
`checksums.txt`: a release carries two sets of artefacts, and an unqualified
name would not say which set it verified.

Building it yourself is the same thing:

```bash
go build -o swarmcli .
```

## How the claim is checked rather than remembered

`swarmcli-oss` is only worth publishing while "this one is the Apache-2.0 build"
is something you can verify rather than something we assert. So it is checked on
the artefact, not on the source:
[`scripts/check-oss-artefact.sh`](../scripts/check-oss-artefact.sh) reads the
module graph Go stamped into each published binary and fails if the main module
is not this repository or if anything private is linked. It runs from
`.goreleaser.yml`'s per-build hook on every binary the release publishes, and
from `ci.yml` on every change that could affect it — the build job is gated
on a paths filter of `**.go`, `go.mod`, `go.sum`, the Dockerfiles, `ci.yml`
itself and `scripts/check-oss-artefact.sh`, so a change that weakens the guard
has to run the guard. A docs-only pull request does not. Either way it is not a
gate that only ever fires on a tag.

It also fails when it cannot read a module stamp at all, rather than passing.
A binary whose graph is unreadable looks exactly like a binary with no private
dependencies, and that is the shape of every guard that quietly stops guarding.
