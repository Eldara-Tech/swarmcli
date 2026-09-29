<!--
SPDX-License-Identifier: Apache-2.0
Copyright © 2026 Eldara Tech
-->

# SwarmCLI Charts

A Helm-inspired package manager for Docker Swarm. Charts package Docker Stack
(Compose) templates with default values and metadata; installing a chart
produces a **release** — a Docker stack whose revision history is stored in
Docker Configs.

Implemented so far ([issue #413]): repository management, discovery, templating,
the full release lifecycle — install, upgrade, rollback, uninstall, list, status,
history, diff, get, and prune — and declarative releases (`apply`, `outdated`).
Chart-dev tooling (`create`, `lint`, `dependency`) and subchart resolution are the
remaining phase.

## Invocation

Charts run through swarmcli's **non-interactive CLI**: when the binary is given
arguments it executes a one-shot command and exits (a bare `swarmcli` still
launches the TUI). This makes charts scriptable for CI/CD and GitOps.

```bash
swarmcli charts repo add eldara https://charts.example.com
swarmcli charts repo update
swarmcli charts search traefik
swarmcli charts show values eldara/traefik > values.yaml
swarmcli charts template my-traefik eldara/traefik -f values.yaml
swarmcli charts install  my-traefik eldara/traefik -f values.yaml
swarmcli charts status   my-traefik
swarmcli charts diff upgrade my-traefik eldara/traefik --set replicas=3
swarmcli charts upgrade  my-traefik eldara/traefik --set replicas=3
swarmcli charts history  my-traefik
swarmcli charts get manifest my-traefik      # also: get values
swarmcli charts rollback my-traefik 1
swarmcli charts list
swarmcli charts uninstall my-traefik
```

The TUI complements this with a **read-only** browser, `:charts`: the installed
releases with their revision, recorded status and live rollout health, expandable
in place to the revision history and running services, with a diff between any
two consecutive revisions. It changes nothing — install, upgrade, rollback and
uninstall are the commands above — but it is the fastest way to see what a swarm
is actually running, and it names the command for whatever you reach for next.

A chart reference is either a configured `repo/chart` or a local path to a chart
directory or packaged `.tgz`. **`--version` selects a version from a repository
index, so it applies only to a `repo/chart` reference** — a local chart carries its
version in its own `Chart.yaml`, and passing `--version` with one is an error
rather than being silently ignored.

### Every command

The full command set, as `swarmcli charts --help` lists it. Run
`swarmcli charts <command> --help` for a command's own options.

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

## Declarative releases (GitOps)

The commands above are imperative, and release state lives in the swarm — so
nothing in git says what *should* be running. `charts apply` closes that gap: it
converges the swarm to a file you commit.

```yaml
# swarmcli-release.yaml
apiVersion: v1
owner: prod-swarm              # optional; see Ownership below
repositories:
  - name: swarmcli-charts
    url: https://eldara-tech.github.io/swarmcli-charts
releases:
  - name: edge
    chart: swarmcli-charts/traefik
    version: "0.1.1"
    values: [./traefik.yaml]     # relative to THIS FILE, not the working directory
  - name: hello
    chart: swarmcli-charts/whoami
    version: "0.1.8"
```

```bash
swarmcli charts apply -f swarmcli-release.yaml --dry-run   # plan
swarmcli charts apply -f swarmcli-release.yaml --diff      # plan + manifest diffs
swarmcli charts apply -f swarmcli-release.yaml             # converge
swarmcli charts outdated                                   # what has a newer chart?
```

| Behaviour | |
|---|---|
| Missing release | installed |
| Changed chart version, values, rendered manifest, or referenced `files/` content | upgraded |
| Identical | **skipped — no new revision** |
| On the swarm but not in the file | **reported, never removed** |
| Installed by this file, no longer in it | **reported as an orphan, still never removed** |
| A `wave` that does not converge | **every later wave is skipped entirely** |

Two of those deserve the emphasis. Releases are **never deleted** — apply prints
the `uninstall` command and leaves the decision to you. And an unchanged release
is skipped **entirely**: history is one Docker Config per revision, so re-applying
on every CI push would otherwise grow the swarm's config store without bound.

### Ownership

`owner:` names the manifest, and every release it installs is stamped with that
name — in the release-history Config's `com.swarmcli.owner` label and in the
stored record — so a later apply can tell a release *this file* installed from
one it has simply never seen. Dropping a release from the file then reports it as
an **orphan**: provably obsolete, because the stamp says this manifest produced
it and nothing else claims it. A release with no stamp, or another manifest's
stamp, stays merely *unmanaged*.

Nothing is deleted either way. The distinction is the prerequisite for a prune
that could be: it is what separates "this is obsolete" from "I do not recognise
this", and only the first is ever safe to act on.

```
com.swarmcli.owner: apply/prod-swarm:release/hello
                    └──── id ─────┘ └── resource ──┘
```

The stamp names the **resource** as well as the owner, and both halves must match
for it to count. A bare owner string cannot tell a release this file installed
from a copy of one — ArgoCD shipped exactly that as `app.kubernetes.io/instance`
and replaced it in 3.0 for the same reason. The `apply/` prefix keeps a manifest
applied from the command line from colliding with a controller that happened to
pick the same name. The controller is the other side of that: a library consumer
passes its own id as `PlanOptions.Owner` and plans against it instead, so the
releases it installed under its own `cd/…` ids read back as its own.

`owner:` is optional and has **no default**. A derived one would either change
between a laptop and a CI checkout (a path hash) or be shared by every repository
using the conventional filename (a basename) — and either would let two unrelated
manifests claim each other's releases. Omit it and nothing is claimed, which is
exactly the behaviour of every version before this one.

For a `repo/chart`, **`version` is required** — a floating pin would silently
upgrade production on the next `apply`. For a **local** chart path (`chart:
./charts/mine`, resolved against the file) it must be **omitted**: the chart's own
`Chart.yaml` sets the version, so there is nothing to select.

Unknown keys are rejected, so a typo fails loudly instead of quietly doing nothing.
Releases are applied in wave order, then file order — see below.

#### Sync waves

`wave` groups releases that go out together. Every release in a wave is deployed,
the whole wave converges, and only then does the next wave start:

```yaml
releases:
  - name: db
    chart: ./charts/postgres          # no wave: means wave 0
  - name: migrate
    chart: ./charts/migrate
    wave: 1
  - name: api
    chart: ./charts/api
    wave: 2
  - name: worker
    chart: ./charts/worker
    wave: 2                           # with api — the order between them is not meaningful
```

The failure semantics are the point as much as the ordering. **A wave that does
not converge stops every wave after it**: nothing later is deployed at all, no
service and no revision record, so a migration that fails can never let the API
that depends on it start.

Waves are ascending and default to `0`, so a file that declares none is one wave
applied in file order — exactly what it has always done, with no waiting added.
Negative numbers are legal, and are how you put something in front of an existing
set without renumbering it.

**`wave` is not `--wait`, and does not need it.** The barrier between waves always
happens; `--wait` is the separate, older question of whether each *individual*
release blocks until it is live. Setting both serialises everything inside a wave,
which is the thing waves exist to stop being necessary — so with waves declared,
leave `--wait` off unless you also want the last wave waited for. `--timeout`
bounds each wave, and defaults to five minutes.

`wave` is the one key here that is not Helmfile's, so Renovate ignores it (see
below). Note that a release file using it cannot be read *at all* by a swarmcli
older than the release this landed in: unknown keys are refused rather than
skipped, which is the same trade every key in this file has made.

`apply` honours `--wait`, `--timeout` and `--history-max`. It **rejects** `--set`,
`--version`, `--reuse-values`, `--install`, `--purge-volumes`, `--requirements` and
`--revision` rather than ignoring them — the file is the only source of truth, so a
value passed on the command line would be a lie. `--diff` implies `--dry-run` and
never deploys.

### Keeping it up to date automatically

If your charts come from [swarmcli-charts], extend its Renovate preset — that is
the whole configuration:

```json
{ "extends": ["github>Eldara-Tech/swarmcli-charts"] }
```

For any other chart repository, one line does the same job:

```json
{ "helmfile": { "managerFilePatterns": ["/(^|/)swarmcli-release\\.ya?ml$/"] } }
```

Both work because the file's key names match [Helmfile](https://helmfile.readthedocs.io/)'s,
so [Renovate](https://docs.renovatebot.com/)'s **built-in** `helmfile` manager reads
it — no custom regex to maintain. Renovate resolves each chart against the
`repositories` you declared and opens a PR bumping `version:` when a new chart
version is published, with the chart's release notes attached. Merge it, and
`swarmcli charts apply` in CI rolls it out.

[swarmcli-charts]: https://github.com/Eldara-Tech/swarmcli-charts

## Chart format

```
mychart/
├── Chart.yaml          # apiVersion, name, version, appVersion, swarmcliVersion, …
├── values.yaml         # default values
├── values.schema.json  # optional JSON Schema validated before render
├── README.md
├── templates/          # Go-templated Compose fragments
│   ├── stack.yaml
│   ├── configs.yaml
│   ├── secrets.yaml
│   └── volumes.yaml
└── files/              # optional; files the chart carries, read recursively
    ├── nginx.conf
    └── tls/ca.pem
```

`apiVersion` is `v1` (the only format this build reads; absent means `v1`).

### Files a chart ships

`files/` is collected and carried with the chart, keyed by each file's path
relative to the chart root — `files/nginx.conf`, `files/tls/ca.pem`. It is
deliberately not Helm's `.Files`: only `files/` is collected, never "everything
that is not a known member", because a chart's file set is destined for a swarm
config and a rule that sweeps up whatever is lying beside `values.yaml` ships
`values.yaml.bak`, a `.env` or an editor swap file to the swarm. Templates stay
in `templates/`, which unlike `files/` is flat.

Compose gives a config or a secret its content in exactly one way, which is what
these are for:

```yaml
# templates/configs.yaml
configs:
  nginx:
    file: files/nginx.conf
```

A config's `file:`, a secret's `file:` and a service's `env_file:` all mean **the
chart**: the path is resolved against the chart root, and the file the manifest
names is carried to the deploy and written beside the rendered manifest for the
`docker` CLI to read.

That is new, and it replaces something worse. Those three keys are resolved by
the docker CLI against the directory of the compose file it is handed, which was
the temp file swarmcli wrote — so `file: ./nginx.conf` read `$TMPDIR/nginx.conf`,
a path any local user can plant a file in, and `file: /etc/shadow` was read as
the invoking operator into a Docker config readable by anyone with Docker access.
A relative path has therefore never meant what a chart author intended.

So a path that cannot mean a file in the chart is refused, and the rule is the
same for every chart however it was loaded — from a repository, from a `.tgz`, or
from a directory on your own disk:

| `file:` / `env_file:` | |
|---|---|
| `files/nginx.conf`, `files/tls/ca.pem` | resolved against the chart |
| `files/${X}/app.conf` | refused — contains `$`; a chart names its files literally |
| `values/config` | resolved against the values — see below; a config's `file:` only |
| `nginx.conf` | refused — outside `files/`, so not something the chart ships |
| `files/missing.conf` | refused — the chart does not contain it |
| `../../etc/shadow` | refused — escapes the chart |
| `/etc/shadow` | refused — absolute, **for every chart** |

A local-directory chart is not an exception, deliberately: vendoring a repository
chart to disk would otherwise convert it from the refused case to the permitted
one, granting the most privilege to the workflow that most obscures where a chart
came from.

**To keep a file the operator manages**, create the resource outside the chart
and reference it — the same answer as for any other input a chart must not
carry:

```bash
docker config create nginx-site /etc/myapp/nginx.conf   # or docker secret create
```

```yaml
configs:
  nginx-site:
    external: true
```

### A config the operator supplies

The section above is about files a chart *ships*. A config the **operator**
writes — a `config.js` they keep in their own git repository — is the other
half, and `values/` is how a chart accepts one:

```yaml
# templates/configs.yaml
configs:
  config:
    file: values/config
    # Swarm config data is immutable, so the name has to change when the
    # content does. Hashing the content makes that automatic and idempotent.
    name: "{{ .Release.Name }}_config_{{ .Values.config | sha256sum | trunc 12 }}"
```

```yaml
# values.yaml — "" so the name above renders; the operator supplies the content
config: ""
```

```bash
swarmcli charts upgrade renovate swarmcli-charts/renovate \
  -f values.yaml --set-file config=./config.js
```

`--set-file <key>=<path>` reads the file into `.Values.<key>` verbatim — no comma
splitting, no `{a,b}` list literal and no type inference, all of which `--set`
does and all of which would corrupt a file. `values/<key>` then materialises that
value beside the manifest for the `docker` CLI to read, exactly as `files/` does
for a file the chart ships. The key is the full values path, so a nested one is
`values/renovate.config`.

Two rules keep this as safe as the refusals above:

- **The operator names the path, never the chart.** That is the whole asymmetry.
  A path on the operator's own command line carries exactly the authority they
  already have; a path a chart chose does not, which is why `file: /etc/shadow`
  stays refused.
- **Only a config's `file:` may name `values/`.** A secret's is refused, and so
  is `env_file:`. Values are stored in the release record — see below — so secret
  *material* would land in the one place a secret exists to keep it out of.
  Secrets stay `docker secret create` + `external: true`.

An absent or empty value is refused rather than deployed, so an operator who
forgets `--set-file` gets a message and not an empty config over a working one.

**Why rotation, rather than editing the config?** Swarm config data is immutable.
`docker stack deploy` reacts to changed content under an existing name by calling
`ConfigUpdate`, and swarmkit refuses that with `only updates to Labels are
allowed` — so a stable name does not update the config, it fails the deploy. A
content-derived name is the only mechanism Swarm offers, and it is idempotent for
free: unchanged content resolves to the same name and the same bytes, which is
the one `ConfigUpdate` swarmkit does allow. Superseded configs are left in place
— Swarm refuses to delete one still in use, and a custom `name:` still carries
the stack's namespace label, so `docker stack rm` collects them.

**Declare a `swarmcliVersion` floor on any chart that uses `files/` or
`values/`** (see
[Declaring the swarmcli a chart needs](#declaring-the-swarmcli-a-chart-needs),
and set it to the release that introduced them). A swarmcli older than that
parses `Chart.yaml` leniently, ignores both entirely, carries no guard, and
resolves `file: files/nginx.conf` against a temp directory of its own — so it
does not fail, it deploys something else. Nothing can be done to an
already-released binary, which makes the constraint the only thing that turns
that into a refusal.

Two consequences worth knowing, and they apply to `values/` exactly as they do
to `files/`:

- **The referenced files are stored in the release record**, so a rollback
  deploys the bytes the original deploy sent rather than whatever the chart says
  today (it may say nothing: rollback replays a stored manifest and never reads a
  chart). Only the files the manifest names are stored, and they share the
  record's ~500 KiB gzipped Docker Config budget with the manifest — an install
  whose record would not fit is refused before anything is deployed, naming the
  sizes.
- **That record is as readable as the manifest beside it.** A file referenced by
  a chart is stored verbatim in the same Docker Config, with the same exposure
  [issue #465](https://github.com/Eldara-Tech/swarmcli/issues/465) describes for
  the manifest — and so is every value, for **every retained revision**, not just
  the current one. That is the reason a `values/` path may only give a *config*
  its content: secret *material* still belongs in a Docker secret created outside
  the chart and referenced with `external: true`.

### Bind mounts name the node, so their source must be absolute

A `file:` reads the chart. A bind mount does not read anything — it names a path
on **whichever node runs the task**, which is a machine the chart author has
never seen. So a source that is not absolute has nothing to resolve against, and
is refused before the deploy:

```yaml
services:
  web:
    volumes:
      - ./data:/data          # refused — relative
      - ~/data:/data          # refused — that is your home, not the node's
      - /srv/app/data:/data   # deployed — a path on the node
      - data:/data            # deployed — a named volume
```

This is the same defect as the one above, one key over: the docker CLI resolved
those sources against the temp directory swarmcli deploys from, so `./data` meant
a directory swarmcli itself deletes when the deploy returns. Nothing was read or
disclosed — a bind source is a string the daemon acts on — but the mount was
never the one the chart meant.

An absolute source is deployed as written, and is deliberately privileged: a bind
of `/var/run/docker.sock` on a manager is the swarm's control plane. That is a
property of compose, and CE leaves the decision with the operator who installs
the chart. swarmcli-cd, which reconciles charts nobody is watching, additionally
requires the application's own allowlist to name the path.

### Declaring the swarmcli a chart needs

A chart may state the chart engine it requires, as a SemVer constraint:

```yaml
# Chart.yaml
swarmcliVersion: ">= 1.13.0"
```

`install`, `upgrade` and `apply` refuse a chart this build is too old for, so the
failure names the version to upgrade to rather than surfacing as whatever error
the missing feature happens to produce (`function "toYamlPretty" not defined`).
`install` and `upgrade` ask before refusing when run interactively; `apply` never
does — it is meant to run unattended. `template`, `diff` and `show` only warn:
they change nothing, and `show` is how you find out what a chart wants. Pass
`--skip-compat-check` to proceed anyway.

The constraint is checked against the **chart engine's** version, which is not
necessarily the version the binary reports for itself — a binary embedding this
package carries whichever engine it pinned. A build that reports no engine
version (any `go build`) warns rather than refusing: not knowing is not evidence
of incompatibility. Charts declaring nothing are unaffected.

Note that only builds carrying this check can honour it: `Chart.yaml` is parsed
leniently, so an older swarmcli silently ignores `swarmcliVersion` — the same
bootstrapping limit Helm's own `apiVersion` gate has.

### Linting a chart

```bash
swarmcli charts lint ./mychart                       # against this build
swarmcli charts lint ./mychart -f ./ci/default-values.yaml
swarmcli charts lint ./mychart --for-version 1.12.0  # against another version
```

`lint` renders the chart and reports every problem it finds — a broken template,
values that fail `values.schema.json`, a `swarmcliVersion` this build does not
satisfy — rather than stopping at the first. It renders from the chart defaults,
layering any `-f`/`--set` on top: a chart with a required, undefaulted input (a
`{{ required }}` / `{{ fail }}` guard) cannot render from bare defaults, so lint
it with the values a real install would supply. A chart that declares no
`swarmcliVersion` gets a warning, not an error: the field is optional, but a
chart naming no floor leaves an operator on an old build nothing to act on.

It also warns when a service's `environment:` block holds a credential inline —
a key named like a password, secret, token, API key or credential, or a value
carrying a PEM private key. Such a value is stored verbatim in both the release
record and the service spec, and both are readable by anyone with Docker access,
so the credential is disclosed to every operator who can run `docker service
inspect`. Reference an external Docker secret instead and read it from
`/run/secrets/<name>`, through the image's `*_FILE` variable or in the service's
own command — a value that already does either is not flagged, nor is an escaped
`$${VAR}`, which reaches the service as the literal text `${VAR}`. An unescaped
`$` is not a lint finding: `template` and every deploy refuse it, because a chart
is not interpolated from the environment it is deployed from. The credential
check is a warning rather than a refusal because the engine cannot tell a
credential from a value that merely reads like one, and it fires on the key even
when the rendered value is empty: the key is what will carry the credential once
an operator supplies one.

`--for-version` asks **whether the chart's declared floor admits that version**.
It cannot tell you the chart *runs* on it: this binary carries one engine's
behaviour and cannot emulate another's, so it checks the claim's shape, not its
truth. Rendering with a real binary of that version is the only thing that
settles it — which is what a chart repository's CI should do:

```bash
SWARMCLI_REF=v1.10.0 scripts/install-swarmcli.sh ./bin  # build the declared floor
./bin/swarmcli charts template t ./mychart              # prove the chart runs on it
```

Templates are rendered with Go `text/template` + [Sprig], exposing:

- `.Values` — merged values (defaults < `-f` files < `--set`)
- `.Release` — `.Name`, `.Namespace`, `.Revision`
- `.Chart` — `.Name`, `.Version`, `.AppVersion`

Each `templates/*.yaml` is rendered then **deep-merged** into a single Compose
document (Compose is one document, unlike Helm's concatenated manifests). Files
beginning with `_` (e.g. `_helpers.tpl`) define named templates only. The merged
manifest is validated as a Docker stack before use.

## Repositories

A repository is an HTTPS-served `index.yaml` listing chart versions, each with
a tarball URL (Helm repository format) — hostable on GitHub Pages/Releases, S3,
or any static host. Configured repos, cached indexes and downloaded chart
archives live under `$XDG_STATE_HOME/swarmcli/charts` (default
`~/.local/state/swarmcli/charts`); a repository's name is a component of its
cache filename, so it is limited to letters, digits, `-`, `_` and `.`.

A chart archive is downloaded once. The index publishes a sha256 for it, which
is checked on every download and again on every read of the cache, so a cached
archive is used only while it still hashes to what the repository publishes
*now* — a chart rebuilt and re-uploaded under one version misses the cache
rather than resolving to the copy you already had. An entry that publishes no
digest is never cached, because nothing could validate the read. A download that
fails on the way — a gateway answering 502 or 504, a dropped connection — is
retried twice before the command gives up, and each wait is reported. Archives
are swept 30 days after the last time one was used.

### Staying current

Every command that resolves a `<repo>/<chart>` reference refreshes that
repository's index first — `install`, `upgrade`, `template`, `diff`, `show`,
`lint` and `search`. `install foo swarmcli-charts/bar` therefore installs the
`bar` the repository publishes, not the one the cache happened to hold when
`repo update` was last run. `repo update` is still there and still does exactly
what it says; you no longer have to remember it before installing.

The refresh is best-effort and bounded by a short deadline. If the repository is
unreachable the command says so and resolves from the cache — a cached index is
a correct answer, merely possibly an old one, and refusing to install would
trade a stale answer for no answer. If that cache is also more than a day old,
a second line says how old, because at that point the version you get may not
be the one you asked for.

Each repository is fetched at most once per invocation, so an `apply` over
twenty releases from one repository downloads one index.

Turn it off for a single command with `--no-repo-update`, or for a machine:

```bash
export SWARMCLI_CHARTS_NO_AUTO_UPDATE=1
```

Either one means *no network*, not merely *no implicit fetch*: `apply` skips its
own refresh too, and `outdated` reports against the cached indexes and says so.
That is what an air-gapped or deliberately offline run wants — the same answer,
without paying a timeout per repository to reach it. The one thing still
fetched is a repository being added for the first time, by `repo add` or by an
`apply` whose release file declares one this machine has never seen: there is
no cached index to fall back to, so the alternative is not an offline answer
but no answer.

Programs embedding this package are unaffected. `RepoStore.Refresh` defaults to
`RefreshExplicit`, which downloads only when asked (`Update`, `Add`,
`EnsureRepos`); `cli` is what selects `RefreshAlways`. A daemon's network
behaviour is not something a CLI convenience should change underneath it.

### Transport

Repositories are **HTTPS by default**. A repository serves the tarball that
*becomes* the deployed workload, so anything on the network path to it decides
what runs on your swarm — and the digest below does not close that, because it is
published in the same `index.yaml` and fetched over the same connection.

Plain `http://` is therefore refused wherever bytes would cross the network:
adding a repository, refreshing its index, and downloading a tarball an index
points at — a repository already configured over `http://` included.

An internal registry on a network you already trust is a legitimate setup, so
this is a default rather than a rule. Opt that machine out:

```bash
export SWARMCLI_CHARTS_ALLOW_PLAINTEXT=1
```

It is read once, where the CLI builds its repository store (the same place
`SWARMCLI_CHARTS_NO_AUTO_UPDATE` is read), so it covers every command that
touches a repository — `repo add`, `repo update`, `search`, `install`,
`upgrade`, `apply`. One line in a shell profile or a CI job's
environment restores a plaintext setup in full; nothing about it is one-way.
Programs embedding this package get the https-only default and decide for
themselves — the `charts` package never reads the environment.

### Integrity

The index is fetched over HTTPS from the repository, but a chart's tarball URL may
point anywhere — a GitHub Release asset, a CDN. The `digest` the index publishes
for each version is what binds the two together, so swarmcli **verifies it**:

| Index entry | Behaviour on `install`/`upgrade`/`template` |
|---|---|
| `digest: sha256:<hex>` matches the download | installs |
| digest does **not** match | **fatal** — `digest mismatch … refusing to install` |
| digest uses an algorithm other than sha256 | **fatal** — verification cannot be performed, so it is not skipped |
| entry publishes **no** digest | installs, with a warning on stderr |

An absent digest warns rather than fails because nothing was verified before this
existed, so rejecting would break every repository that publishes none — including
older and hand-written ones. Both the bare-hex form (`helm repo index`) and the
`sha256:`-prefixed form are accepted.

Chart archives are also capped at 20 MiB on the wire (the decompressed contents
have their own limits), so a hostile or truncated download cannot exhaust memory
before it is hashed.

TLS is not sufficient on its own here: it authenticates the *host* you downloaded
from, not that the bytes are the ones the repository index vouched for.

## Release storage

Each release revision is stored as an immutable, gzipped Docker Config named
`swarmcli.release.<release>.v<N>`, labeled `com.swarmcli.*`. The log is
append-only: the highest revision is the current state; lower deployed revisions
display as `superseded`. This gives HA (Swarm Raft) and rollback with no
external database.

```bash
docker config ls --filter label=com.swarmcli.release=my-traefik
```

## Pruning release history

Because every revision is its own Config, history grows without bound. Trim it
with the retention window — keep only the newest `N` revisions:

```bash
# Apply a window inline, after the deploy:
swarmcli charts install my-traefik eldara/traefik --history-max 20
swarmcli charts upgrade my-traefik eldara/traefik --history-max 20

# Or prune existing history on demand:
swarmcli charts prune my-traefik --history-max 20   # one release
swarmcli charts prune --history-max 20              # every release
swarmcli charts prune my-traefik --history-max 20 --dry-run  # preview only
```

`prune` deletes the oldest revisions beyond the window and **always keeps the
current (highest) revision**, so the live release and rollback targets inside the
window are preserved. Without `--history-max` (or with `0`) it keeps everything
and reports that no window was given. `--dry-run` prints the keep/delete decision
without touching Docker.

> **Use `swarmcli charts prune`, not raw Docker.** `docker config prune`,
> `docker system prune` and `docker config rm swarmcli.release.*` are not
> SwarmCLI-aware and will corrupt release history, rollback targets and audit
> lineage. SwarmCLI is the only sanctioned way to delete a release's resources.

Per-revision protection labels (`com.swarmcli.keep`, `com.swarmcli.protected`)
are a planned Phase-3 addition; today the current revision plus the newest-`N`
window are the protections.

## Notes & limitations

- **`install --dry-run`** renders, validates, and computes the next revision
  but does not deploy. For fully offline rendering use `charts template`.
- **`--purge-volumes`** removes volumes on the connected node only, and warns
  when the swarm has more nodes; purging volumes on every node is a Business
  Edition feature.
- **Secrets:** the rendered manifest is stored **unredacted** in a Docker Config,
  which is readable by anyone with Docker access — as are `charts get manifest`
  and the TUI config viewer, which read it back. Do **not** inline secret
  material in templates: reference Docker secrets as separate objects instead.
  `charts lint` warns about the common shape of getting this wrong (see
  [Linting a chart](#linting-a-chart)), but it is advice at authoring time, not a gate:
  nothing refuses a deploy over it, and no read path redacts. The same material
  is equally visible in the service spec to anyone who can run `docker service
  inspect`, so treat the release record as no more secret than the stack it
  records.
- **Chart integrity** is only as good as the index: a repository that publishes
  no `digest` gets a warning, not a refusal (see [Integrity](#integrity)). Chart
  archives are capped at 20 MiB on the wire.
- `docker stack deploy` is used under the hood, so only the Compose-on-Swarm
  subset is supported and the `docker` CLI must be on `PATH`.

[issue #413]: https://github.com/Eldara-Tech/swarmcli/issues/413
[Sprig]: https://masterminds.github.io/sprig/
