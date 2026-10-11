# Contributing to SwarmCLI

We love your input! We want to make contributing to SwarmCLI as easy and transparent as possible, whether it's:

- Reporting a bug
- Discussing the current state of the code
- Submitting a fix
- Proposing new features
- Becoming a maintainer

## Development Setup

Before changing anything non-trivial, read
[docs/architecture.md](docs/architecture.md) — it maps the packages, explains how
views and commands self-register through `init()`, and names the seams the
Business Edition attaches to (renaming one of those is a cross-repo change).

SwarmCLI is built in **Go**. To get started:

1. **Clone the repository**:

   ```bash
   git clone https://github.com/Eldara-Tech/swarmcli.git
   cd swarmcli
   ```

2. **Run locally**:

   ```bash
   go run .
   ```

3. **Debug logging**:
   ```bash
   SWARMCLI_ENV=dev LOG_LEVEL=debug go run .
   ```

## Testing the TUI

Two assertion mistakes recur here, both of which produce a green test that never
reached the code it was written for.

**Call the real factory, do not model it.** `app.switchToView` batches the *view
factory's returned command* with `OnEnter()`. PR #574 gave eight views a test
asserting that entry arms exactly one poll chain, and modelled the factory as
`tea.Batch(m.Init(), m.OnEnter())`. Seven factories really did just return a load
command, so the test was right about those — but `views/services/register.go`
returns `tickCmd()` and `views/tasks/register.go` returns `model.OnEnter()`
outright. Both double-armed, and both stayed green **in the very PR whose subject
was that defect**. Calling `factory(m.deps, 80, 24, nil)` and putting *its*
command in the batch fails on both.

A hand-written stand-in encodes what you believe the caller does, so it can only
fail where your belief was already right — and the views that break the pattern
are exactly the ones the guess omits.

**Count the rows that carry content, not the height.** When a layout pads to a
fixed height, `require.Equal(t, height, lipgloss.Height(out))` passes whether the
space is *used* or *wasted*. Writing the guard for swarmcli#560, the fullscreen
frame trimmed and padded to exactly the terminal height, so a view sized three
rows too small still rendered full-height and the mutation that undersized it
stayed green.

The property under test was never "how tall is the output" — it was "did the
layout claim every row it was given". Give the fixture more content than can fit,
then assert on the rows that carry content.

**Mutation-check any guard you add**: break the thing it protects and confirm the
test fails. A guard that has never failed is an untested claim.

**Driving the real binary.** End to end, a Python `pty.fork()` harness can run
the built TUI against a swarm, send keys and SGR mouse bytes, and assert on the
raw output stream — footer text, mode escapes — with no terminal emulator. Three
behaviours of Bubble Tea v1 and lipgloss make a naive harness fail in ways that
look like app bugs:

- **Type one byte at a time**, about 150 ms apart. Printable runes arriving in
  one read become a single `KeyMsg`, so writing `:config\r` at once sends the
  key `":config"`, the `:` bar never opens, and every later key lands on the
  wrong view.
- **Answer the startup queries.** lipgloss sends `ESC]11;?` (background colour)
  and `ESC[6n` (cursor position), then waits about 5.5 s for replies, so a
  harness with a fixed boot delay passes or fails by timing. Reply
  `ESC]11;rgb:0000/0000/0000ESC\` and `ESC[1;1R` when they appear.
- **Injected mouse bytes ignore the terminal's mode.** Bubble Tea parses SGR
  sequences even after the app sent `ESC[?1002l`, so a click that works proves
  nothing about mouse reporting being restored. Assert on the `ESC[?1002h`
  escape in the output instead, and check that assertion fails against a build
  without the fix.

The renderer repaints only changed lines, so the stream is a diff: match text
that changes, never a whole screen. Set `XDG_STATE_HOME` to a temporary
directory so the app's log is readable and separate, and
`SWARMCLI_TELEMETRY=none`.

## Pull Request Process

1. Fork the repo and create your branch from `main`.
2. If you've added code that should be tested, add tests.
3. If you've changed APIs, update the documentation.
4. Ensure the test suite passes.
5. Make sure your code lints.

### Specific Commands

Please use specific commands instead of greedy commands to avoid adding unwanted files:

```bash
git add <file1> <file2>
# Avoid: git add .
```

## Community

Join us in our [GitHub Issues](https://github.com/Eldara-Tech/swarmcli/issues) for discussions!
