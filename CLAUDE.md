# SwarmCLI

Keyboard-driven TUI for Docker Swarm management ("k9s for Docker Swarm"). Single static Go binary, Bubble Tea framework, zap logging.

## Quick Reference

```bash
# Build & run
go build -v -o swarmcli .
SWARMCLI_ENV=dev LOG_LEVEL=debug go run .

# Unit tests
go test ./...

# Integration tests (full E2E against real Docker Swarm via DinD)
./test-setup/testenv.sh integration           # up → deploy → test → down
./test-setup/testenv.sh up                    # start Swarm environment only
./test-setup/testenv.sh deploy                # deploy test stack
./test-setup/testenv.sh test                  # run integration tests
./test-setup/testenv.sh test TestScaleWhoami  # single test
./test-setup/testenv.sh down                  # teardown + cleanup
KEEP=1 ./test-setup/testenv.sh integration    # keep env running after tests
TEST_LOG=1 ./test-setup/testenv.sh test       # enable test logging

# Lint (CI uses this)
golangci-lint run ./... --build-tags=integration

# Logs
tail -f ~/.local/state/swarmcli/app-debug.log   # dev mode
tail -f ~/.local/state/swarmcli/app.log          # prod mode (JSON)
```

## Architecture

The package map and the registration patterns moved to
**[docs/architecture.md](docs/architecture.md)** — a contributor needs them as much
as an agent does, and while they lived here they drifted: six top-level and four
view packages were missing by the time #620 restored them.

Two rules from that page are worth having in front of you, because breaking either
is silent:

- **The seams are a published contract.** `registry`, `views/view`, `features` and
  the `docker` operation interfaces are consumed by `swarmcli-be`, a private module
  you cannot see from this repo. Renaming or narrowing one is a cross-repo change,
  and the BE side has to merge first or its CI breaks on `undefined:`.
- **Ask `features.IsEnabled`, never assume an edition.** The base build enables
  nothing; the paid build calls `features.Enable()` from `init()` after a licence
  verifies.

- **Error text quotes with `'…'`, not `%q`**: an error names things in single quotes — `chart '%s' version '%s' not found`. `swarmcli-cd` consumes `charts/` and `docker/` and logs their errors through logfmt, which escapes every `"` inside a value, so `%q` reached an operator as `chart \"x\" version \"1\" not found`. Applies to anything constructing an error (`fmt.Errorf`, `errf`, `usageErr`) and to status text that travels with one (`Convergence.Reason`, compat `Reason`, preflight reasons, `RepoStore.Warnf` — cd routes that straight into `slog`). **Keep `%q`** for the zap logs (`l().Infof`), TUI presentation (dialogs, toasts, the status bar), CLI success output, and anywhere the escaping is the point — a content `preview: %q`, a keypress, a rune. See swarmcli-cd#157.

## Adding New Functionality

**New command**: Create `commands/command/mycommand.go`, implement `registry.Command` (Name/Description/Execute), call `registry.Register()` in `init()`. Also implement `Spec() registry.CommandSpec` — declare every flag the command reads (`a.Has`/`a.Get`) plus `Usage`/`Examples`, or `:cmd --help` shows only a fallback and strict validation rejects the command's own flags. Aliases (`Aliaser`) inherit the primary's spec; do not add a spec to the alias. See `commands/command/docker/node/ls.go` for a zero-flag spec. No command in this repo declares a flag today, so for one that does, `registry/spec.go` documents `FlagSpec` field by field with examples — that type is the contract, and it is the reference an extension build writes against too.

**New non-interactive verb from an extension build**: call `cli.RegisterCommand("myverb", "One line for swarmcli help", run)` from an `init()` in the embedding module. Nothing is added to this repository — that is the point of the seam; a list of names here would put the extension's vocabulary in the OSS module. Built-in verbs (`charts`, `version`, `help`) cannot be shadowed.

**New view**: Create `views/myview/`, implement `view.View` interface, and add a `register.go` whose `init()` calls `view.RegisterView(name, factory)`. Add its blank import to `views/autoload.go` so the package is loaded. See `views/nodes/register.go`.

**New `swarmcli charts <sub>` CLI subcommand** (this is *not* the TUI `:` registry — it's the arg-based dispatcher): add a row to `chartsCommands` in `cli/commands.go` — name, group, usage line(s), the flags the handler reads, the handler — and nothing else has a list to update: dispatch, `charts --help`, `charts <sub> --help` and the command blocks in `README.md` and `charts/README.md` all render from it. Put the logic in `charts/` and keep the `cli/` half thin (the `charts` package is where the coverage bar applies). A new flag goes in the single **global** `flags` struct in `cli/args.go`, is listed by every row whose handler reads it — a flag a row does not list is rejected, so an operator is told rather than silently ignored — and gets a `flagDocs` entry so both help surfaces can describe it. Then regenerate the README blocks:

```bash
go test ./cli -run TestGeneratedCommandBlocks -update
```

`go test ./cli` fails if you forget; it also compares each row's flag list against what the handler and its callees actually read, so an allow-list cannot quietly drift from the code. It further requires `integration-tests/charts/charts_cli_test.go` to run the new command against the DinD swarm with every flag its row lists — a row the allow-list matches perfectly is still a row nothing has ever run.

## Environment Variables

| Variable | Purpose | Default |
|---|---|---|
| `SWARMCLI_ENV` | `dev` (console logs) or `prod` (JSON logs); also registers the dev-only `:dev-update` command (force-shows the update-available notice for previewing) | `prod` |
| `LOG_LEVEL` | `debug`/`info`/`warn`/`error` | `debug` (dev), `info` (prod) |
| `DOCKER_CONTEXT` | Override Docker context | `docker context show` |
| `TEST_LOG` | Enable logging in tests | unset |
| `SWARMCLI_CHARTS_ALLOW_PLAINTEXT` | Opt out of the https-only default for chart repositories; read only by `cli`, which wires it to `charts.RepoStore.AllowPlaintext` (the `charts` package never reads the environment, so embedders keep the default) | unset (https only) |
| `SWARMCLI_CHARTS_NO_AUTO_UPDATE` | Stop the CLI refreshing a repository index before resolving a chart from it; read only by `cli`, which wires it to `charts.RepoStore.Refresh` = `RefreshNever` (same for `--no-repo-update`). Embedders keep the `RefreshExplicit` default either way | unset (refreshes) |

## Pro Feature Boundary

This repo is public. Business Edition's source is not, and the line between them
is about **mechanism, not marketing**.

**Public, and fine to write here.** What a BE feature *does*, its CLI surface,
its flags, the operator workflow around it, and how it fails. Naming a BE
feature is fine — `README.md` advertises several, and `app/updatenotice.go`
ships an upsell string naming three. Generic extension points — registries,
hooks, feature flags, the `docker` decorator seams — are the whole point of the
two-repo model and belong here in full, including *what data* an extension may
supply and *why the Swarm API cannot*.

**Private. Must not appear in this repo, in any form, including comments and
tests.**

- The licence signature scheme, payload schema and any field of it.
- Key custody and the verification implementation, and the dev-pubkey override
  or any `swarmcli_devkeys` build surface.
- The inter-component topology and `/v1/*` endpoint names — how the TUI, the
  RBAC proxy and the per-node agents talk to each other.
- Kernel-level mechanics behind a feature (namespace entry for port-forward).
- mTLS bundle composition and overlay trust.
- Threat-model reasoning: why a guard exists, and what it is defending against.
- **Private symbol names, private file paths and private issue numbers.** This
  one is independent of the rest: a comment saying "matches
  `license.FeatureFoo`" or "see `swarmcli-be/commands/pro/x.go`" discloses BE's
  internal layout *and* is unopenable by every contributor it is addressed to,
  whether or not the mechanism behind it is secret.

When adding code an extension will call, document it as an extension point and
describe the contract, not the caller. The two feature-name constants in this
tree (`service-health`, `volumes-all-nodes`) are shared vocabulary the seam
cannot work without; the tier→entitlement mapping that consumes them is not
here, and must not arrive.

**`docs/` is the end-user documentation for both editions**, and that is
deliberate rather than a lapse: those pages describe behaviour, CLI surface and
operator workflow, which is the public half of the line above. They are also the
place the line is most easily crossed — a page explaining *how* a feature works
rather than what it does is the failure mode. When adding one, the test is
whether an operator needs the sentence to use the product, or whether it only
explains our implementation to them.

## Integration Test Infrastructure

- Tests in `integration-tests/` use `//go:build integration` tag
- `test-setup/docker-compose.yml`: DinD multi-node Swarm (1 manager on tcp://localhost:22375,
  plus a scaled `worker` service — `NODES=<n> testenv.sh up` sizes the whole swarm, default 3)
- `test-setup/test-stack.yml`: Demo services (whoami, whoami_single, log_ticker) with volumes, networks, and configs
- `test-setup/testenv.sh`: Orchestrator script
- Tests use `gotestsum` as test runner (with `--format=testname` locally, `--format=github-actions` in CI)
- Docker context name for tests: `swarmcli`
- When adding new resource types (volumes, networks, secrets, configs), update `test-setup/test-stack.yml` and add integration test assertions to ensure inspect and compose reconstruction cover them

## Pull Requests

Every PR to `main` must pass the `check_labels.yml` workflow which requires one label from each of three groups:

| Group | Labels | Meaning |
|---|---|---|
| A — Change type | `A0-ui`, `A1-feature`, `A2-bugfix`, `A3-technical`, `D0-dependency` | What kind of change (`D0-dependency` is the Dependabot one, and counts as an A label) |
| B — Urgency | `B0-low-priority`, `B2-high-priority` | How urgent |
| C — Breaking | `C0-breaks-nothing`, `C1-breaking-change` | Backward compatibility |

Add all three labels when creating a PR: `gh pr edit <number> --add-label "A0-ui,B0-low-priority,C0-breaks-nothing"` (or use the REST API if `gh pr edit` fails due to classic projects deprecation).

When a PR fixes a GitHub issue, copy the issue's labels to the PR and add any missing required group labels (A, B, C). Use `gh api repos/OWNER/REPO/issues/<pr-number>/labels -f "labels[]=LABEL"` to add labels via API.

**Versioning at release.** A `C1-breaking-change` PR merged since the last GA forces a **major** tag (`vX.0.0`). The release changelog is type-only (no dedicated "Breaking" section), so the label is the breaking-change gate — the pushed tag is authoritative, overriding release-drafter's `$RESOLVED_VERSION`. For a breaking release, fill `.github/UPGRADE_NOTES.md` before tagging.

## CI Workflows (.github/workflows/)

All seven:

- `ci.yml`: go fmt, golangci-lint, go build, Docker image build
- `integration-tests.yml`: Full E2E
- `release.yml`: GoReleaser on tags (multi-platform, Homebrew tap)
- `check_labels.yml`: PR label validation — it reads labels from the `pull_request` **event payload**, not the API, so a run queued from `opened` sees none; only a new event fixes that, never a job re-run
- `licence.yml`: License header check
- `govulncheck.yml`: scheduled vulnerability scan
- `dependabot-tidy.yml`: keeps `go.sum` tidy on Dependabot PRs (which carry `D0-dependency`)

## Go Version & Build

Go 1.27. No Makefile — use `go build` directly. GoReleaser handles releases with `-trimpath -s -w` ldflags and version injection.

When updating the Go version, keep these in sync:
- `go.mod` — `go` and `toolchain` directives
- `Dockerfile` — `FROM golang:<major.minor>`, the `swarmcli-dev` image. Dependabot bumps this one on its own schedule, and a bump that arrives unpaired leaves the dev container compiling on a Go nothing else in CI uses
- `.devcontainer/Dockerfile` — `mcr.microsoft.com/devcontainers/go` image tag, consumed by swarmcli-be's devcontainer (tracks major.minor; patch versions are handled by `GOTOOLCHAIN=auto`). MCR lags the `golang` image by a release, so this tag legitimately trails — check `https://mcr.microsoft.com/v2/devcontainers/go/tags/list` before bumping it
- `govulncheck` CI step — bump suppressed vuln IDs if the new toolchain resolves them, or add new ones if it introduces new unfixed stdlib vulns
