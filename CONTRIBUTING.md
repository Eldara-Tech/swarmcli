# Contributing to SwarmCLI

Bug reports, fixes, features, documentation and questions are all welcome. This
page is how to get a working checkout, how the tests are meant to be written,
and what a pull request needs to land in the right place in the release notes.

## Development setup

Before changing anything non-trivial, read
[docs/architecture.md](docs/architecture.md). It maps the packages, explains how
views and commands self-register through `init()`, and names the seams the
Business Edition attaches to; renaming one of those is a cross-repo change.

### Run from source

SwarmCLI is written in Go. The module path is `github.com/Eldara-Tech/swarmcli/v2`.

```bash
git clone https://github.com/Eldara-Tech/swarmcli.git
cd swarmcli
go run .                       # against your current Docker context
go install                     # puts a `swarmcli` on your PATH from this tree
```

A local build reports `version=dev`; only a tagged release carries a version
and a chart-engine stamp.

### Or in a container

The repository's Dockerfile builds a development image with the toolchain, and
mounting the Docker socket lets it drive the swarm on the host:

```bash
docker build -t swarmcli-dev .
docker run --rm -it \
  -v "$PWD":/app \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -w /app swarmcli-dev

# or, with the compose file in the repository:
docker compose run --build --rm swarmcli
```

Then `go run .` inside the container.

### Logs

```bash
go run .                                   # production mode: JSON logs
# → ~/.local/state/swarmcli/app.log

SWARMCLI_ENV=dev go run .                  # development mode: readable logs
# → ~/.local/state/swarmcli/app-debug.log

LOG_LEVEL=debug SWARMCLI_ENV=dev go run .  # and more of them
```

`tail -f ~/.local/state/swarmcli/app-debug.log | ccze -A` colours the tail if
you have `ccze` installed. Every environment variable and on-disk path, for both
editions, is in [docs/configuration.md](docs/configuration.md).

On startup, SwarmCLI asks `https://swarmcli.io/api/v1/version` whether a newer
release exists, sending its version and edition. Set
`SWARMCLI_DISABLE_VERSION_CHECK=true` while developing, so a local build does
not report itself as out of date on every run.

### Tests

```bash
go test ./...                              # unit tests
./test-setup/testenv.sh test               # integration tests against a swarm in Docker-in-Docker
TEST_LOG=1 ./test-setup/testenv.sh test    # the same, with the logs kept
```

The integration environment brings up a swarm of its own; `NODES` sizes it.
Read the next section before adding a test that asserts on a view.

## Testing the TUI

Two assertion mistakes recur here, both of which produce a green test that never
reached the code it was written for.

**Call the real factory, do not model it.** `app.switchToView` batches the _view
factory's returned command_ with `OnEnter()`. PR #574 gave eight views a test
asserting that entry arms exactly one poll chain, and modelled the factory as
`tea.Batch(m.Init(), m.OnEnter())`. Seven factories really did just return a load
command, so the test was right about those, but `views/services/register.go`
returns `tickCmd()` and `views/tasks/register.go` returns `model.OnEnter()`
outright. Both double-armed, and both stayed green **in the very PR whose subject
was that defect**. Calling `factory(m.deps, 80, 24, nil)` and putting _its_
command in the batch fails on both.

A hand-written stand-in encodes what you believe the caller does, so it can only
fail where your belief was already right, and the views that break the pattern
are exactly the ones the guess omits.

**Count the rows that carry content, not the height.** When a layout pads to a
fixed height, `require.Equal(t, height, lipgloss.Height(out))` passes whether the
space is _used_ or _wasted_. Writing the guard for swarmcli#560, the fullscreen
frame trimmed and padded to exactly the terminal height, so a view sized three
rows too small still rendered full-height and the mutation that undersized it
stayed green.

The property under test was never "how tall is the output". It was "did the
layout claim every row it was given". Give the fixture more content than can fit,
then assert on the rows that carry content.

**Mutation-check any guard you add**: break the thing it protects and confirm the
test fails. A guard that has never failed is an untested claim.

## Pull request process

1. Fork the repository and create your branch from `main`.
2. If you have added code that should be tested, add tests.
3. If you have changed a command, a key or a documented behaviour, update the
   page in `docs/` that describes it.
4. Make sure `go test ./...` passes and the code lints.
5. Stage files by name. `git add .` has shipped things that were never meant to
   be committed:

   ```bash
   git add <file1> <file2>
   ```

6. Label the pull request. The release notes are generated from PR labels, with
   no hand-written changelog: a feature, a fix, a UI change and a technical
   change each have a label, and `C1-breaking-change` is what makes the next
   release bump its major version (see [RELEASING.md](RELEASING.md)). A
   maintainer will label it if you do not, but a PR you label yourself lands in
   the right section of the notes with the title you chose.

## Reporting a security issue

Not in a public issue. [SECURITY.md](SECURITY.md) says how.

## Community

[GitHub Issues](https://github.com/Eldara-Tech/swarmcli/issues) for bugs,
proposals and discussion. Conduct is covered by
[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
