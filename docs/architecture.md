# Architecture

SwarmCLI is a single static Go binary: a Bubble Tea TUI for Docker Swarm, plus a
small non-interactive CLI reached by passing arguments. This page is the map — what
each package is for and how the pieces find each other at startup.

It is written for someone about to change the code. For installing and using the
binary, start at [README.md](README.md); for what the Business Edition adds, see
[editions.md](editions.md).

## Entry points

`main.go` does one branch. With arguments it dispatches a non-interactive
subcommand through `cli.Dispatch`; with none it launches the TUI via
`tea.NewProgram`. Version strings are injected at build time with `ldflags`.

Almost nothing is wired by hand after that. Views and commands register themselves
from `init()` and are pulled in by blank imports, so adding one is a matter of
creating the package and adding it to the right autoload file — see
[Key patterns](#key-patterns) below.

## Layout

```
main.go                    Entry point; version injection via ldflags. With args, dispatches non-interactive CLI subcommands via cli.Dispatch(); bare invocation launches the TUI (tea.NewProgram())
cli/                       Arg-based CLI dispatch (cli.Dispatch): `charts`, `version`, `help`; cli/commands.go holds chartsCommands — the table dispatch, the usage text, per-command --help and the generated README blocks all read from; cli/apply.go holds the GitOps subcommands (`charts apply`, `charts outdated`)
charts/                    Helm-like package manager (repos, chart rendering, releases) + declarative releases (releasefile.go, apply.go, outdated.go). charts.ChartSource (source.go) is the seam that resolves a chart ref — repo or local path — so release planning is testable without Docker, a network or a filesystem. charts.NewDockerBackend(ctxName) + NewEngineWith is the seam that targets a *specific* swarm: the default backend uses the ambient Docker context, the SDK client singleton and the shared snapshot cache, all three of which are process-global
app/
  app.go                   Init(); triggers command autoload via _ "github.com/Eldara-Tech/swarmcli/v2/commands" and view autoload via _ "github.com/Eldara-Tech/swarmcli/v2/views" (view factory registry lives in views/view/registry.go)
  hooks.go                 PreUpdateHook registration; StartupOverlay; RegisterShutdownHook / RunShutdownHooks (extension builds register cleanup for long-lived resources here)
  model.go                 Central state: Model struct (viewport, currentView, viewStack, commandInput, searchInput, systemInfo)
  update.go                Main message router: navigation, resize, events, key dispatch
views/
  view/interface.go        View contract: Update/View/Init/Name/OnEnter/OnExit/HasErrors/ShortHelpItems + Filterable interface
  stacks/                  Stack list → drill into services
  services/                Service list (filterable by stack/node/all), scale/restart actions
  tasks/                   Task list per service/stack
  nodes/                   Cluster node list
  secrets/                 Secret management
  configs/                 Config management
  logs/                    Service log streaming
  contexts/                Docker context switcher
  help/                    Keybinding cheat sheet
  inspect/                 JSON inspect viewer
  networks/                Network list
  loading/                 Loading spinner
  commandinput/            ":" command bar
  searchinput/             "/" search filter bar (app-level, drives Filterable views)
  confirmdialog/           Confirmation prompts
  scaledialog/             Scale replica input
  helpbar/                 Dynamic keybinding bar
  systeminfo/              Header with cluster info
  viewstack/               Navigation stack (push/pop)
  charts/                  The `:charts` browser — the chart-engine's TUI surface
  volumes/                 Volume list and file browser. This is the view the `volumes-all-nodes` Pro gate below applies to
  taskutil/                Shared task helpers used by the task-bearing views
  unlockdialog/            Secret/config unlock prompt
commands/
  api/                     Command context & arg parsing
  command/                 Top-level built-in commands (help.go, contexts.go, quit.go, alias.go, bootstrap.go, devupdate.go); docker-entity commands live under command/docker/<entity>/ls.go (service, node, network, volume, secret, config). devupdate.go registers `:dev-update` only when SWARMCLI_ENV=dev (force-shows the update-available notice for previewing)
  autoload.go              Blank import triggers init() registration
docker/
  client.go                Context-aware Docker client factory
  snapshot.go              In-memory cache (3s TTL, sync.RWMutex, atomic refresh flag)
  events.go                Docker event stream subscription
  service.go               Service ops: scale, restart, update
  node.go, task.go         Entity queries (TaskEntry includes ContainerID, populated from task.Status.ContainerStatus.ContainerID with nil-guard)
  stack.go                 Stack queries
  secret.go, config.go     Secret/config CRUD
registry/
  registry.go              Global command map: Register(), Get(), All(), Suggest()
features/
  features.go              Feature-flag registry. The base build enables nothing; extension builds call Enable() from init(). This is the seam swarmcli-be's profeatures/ drives from the license, so a change here is a cross-repo change
args/
  args.go                  Argument parsing shared by the CLI dispatch path
settings/
  settings.go              Persists small single-purpose CLI preferences to the user config dir
ui/
  columns.go, dialog/, components/, framebox
                           Shared widgets nearly every view under views/ builds on — the filterable list, dialog styling, the frame box
core/primitives/hash/      Small shared primitives
assets/                    Logos and the demo GIF (no Go code)
utils/log/
  logger.go                zap wrapper: Init(), L(), Sync(), SetLevel(), lumberjack rotation
  slogcore.go              InitSlog(slog.Handler): forwards this package's output into a host program's log/slog handler instead of a file. For consumers importing swarmcli as a library (swarmcli-cd), where Init's lumberjack file is wrong and not initialising at all silently discards everything L()'s callers write
```

## Key patterns

- **Bubble Tea MVC**: Input → Update() → tea.Cmd → View(). All state changes via `tea.Msg` types.
- **View Stack**: `viewStack.Push(old)` / `Pop()` for breadcrumb navigation.
- **View Factory**: Views auto-register via `init()` + `view.RegisterView(name, factory)` in each view's `register.go` (registry in `views/view/registry.go`, looked up with `GetFactory`). `app/app.go`'s blank import `_ "github.com/Eldara-Tech/swarmcli/v2/views"` pulls `views/autoload.go`, which blank-imports every view — exactly mirroring the command autoload pattern.
- **Command Registry**: Commands in `commands/command/` auto-register via `init()` + `registry.Register()`. Accessed via `:` input.
- **Command Spec**: Commands optionally implement `registry.CommandWithSpec` (`Spec() registry.CommandSpec`, discovered by type assertion like `Aliaser`). The spec declares `Usage`, `Flags` (the allow-list), and `Examples`. `api.ParseInput` is the single chokepoint that, in order: short-circuits `Passthrough` specs, intercepts `--help`/`-h`/`-help` (and `:help <cmd>`) into a per-command help screen reusing the detailed help view, then rejects any undeclared flag (**global strict**, with a `did you mean --x?` suggestion). Unknown-flag rejection means every registered command MUST declare a spec — a missing/empty spec rejects all flags. `Passthrough:true` is the narrow escape-hatch for delegating/unavailable stubs (e.g. the OSS `bootstrap` stub): it skips both help interception and validation so every arg reaches `Execute` unchanged and the command keeps its own messaging (and no Pro flag internals leak into OSS — see Pro Feature Boundary).
- **CLI Command Seam**: `cli.RegisterCommand(name, summary, run)` adds a top-level *non-interactive* verb (`swarmcli <name> …`) from a build embedding this module — the headless counterpart of `registry.Register`, which only reaches the TUI's `:` palette because `Execute` returns a `tea.Cmd`. `run` gets everything after the verb and returns the exit code (2 usage / 1 failure / 0, per `cli/output.go`). It panics on a duplicate or on a name this package dispatches itself, so the vocabulary can be extended and never re-pointed. This repository registers nothing, so `swarmcli help` still lists exactly its own verbs.
- **Snapshot Cache**: `docker.GetSnapshot()` / `docker.RefreshSnapshot()` — 3s TTL, background event-driven invalidation.
- **Navigation**: `view.NavigateToMsg{ViewName, Payload, Replace}` dispatched in `update.go`.

## Two seams worth knowing before you widen them

**`charts.Backend` (`charts/release.go`) has exactly two implementers**: CE's own
`dockerBackend`, and `swarmcli-cd`'s `backend.Backend`. `swarmcli-be` does *not*
implement it — its `DeployStack` is `docker.DeployStack`, an unrelated two-arg
shell-out used by `bootstrap`. So widening that interface is a contained
cross-repo change (swarmcli#532 did it).

The `docker.*` snapshot helpers are the opposite case. `RefreshSnapshot` and
`GetOrRefreshSnapshot` keep their bare, context-free forms because `swarmcli-be`
and the TUI depend on them; the context-taking variants are separate `…Ctx`
functions rather than a signature change.

**The help view has three layouts, and they are not interchangeable.**
`views/help/register.go`'s factory routes by payload type:

- `[]CommandInfo` → `New` — the command table behind `:help`.
- `[]HelpCategory` → `NewDetailed` — a **multi-column** layout, one column per
  category with a fixed key width, built for keybindings.
- per-command help → `CommandHelp` — a vertical, long-form layout.

Reaching for the columnar `NewDetailed` to render long prose produces a
fixed-width column that truncates. Route by what the content *is*, not by which
constructor is nearest.

## The chart engine's release records outlive the stack

Every revision is written as a Docker config named
`swarmcli.release.<release>.v<n>`, labelled `com.swarmcli.type=release`. That
label is **not** `com.docker.stack.namespace`, so `docker stack rm <name>`
removes the services and leaves the records — which is why a torn-down
application can still report as synced. See
[troubleshooting.md](troubleshooting.md#charts) for the operator-facing symptom.

Those records store the rendered manifest, and that is deliberate. swarmcli#465
asked for a secret-redaction pass over them and was closed not-planned: an
inlined `environment:` value is already readable from the service spec by anyone
who can read the config, so the record duplicates an exposure rather than
widening it — and redacting it would break rollback and `charts apply`, which
need the manifest they actually deployed.

## What the Business Edition attaches to

This repository is the open half of an open-core pair. `swarmcli-be` is a separate
private module that imports this one and registers additional views, commands and
middleware through the same `init()` seams described above — it does not fork or
patch anything here.

Two consequences matter when changing this repo:

- **The seams are a published contract.** `registry`, `views/view`, `features` and
  the `docker` operations interfaces are consumed by a module you cannot see from
  here. Renaming or narrowing one is a cross-repo change, and the BE side must
  merge first or its CI breaks.
- **`features/` is the license seam.** The base build enables nothing; the paid
  build calls `features.Enable()` from `init()` once a license verifies. Code here
  should ask `features.IsEnabled` rather than assume an edition.

[editions.md](editions.md) describes the split from a user's point of view.

## Related

- [configuration.md](configuration.md) — environment variables and on-disk paths
- [features.md](features.md) — what the TUI can do
- [rbac.md](rbac.md), [bootstrap.md](bootstrap.md) — the managed-infrastructure path
- [troubleshooting.md](troubleshooting.md) — known environment traps
