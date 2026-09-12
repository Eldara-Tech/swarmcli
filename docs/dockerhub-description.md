<!--
SPDX-License-Identifier: Apache-2.0
Copyright © 2026 Eldara Tech
-->

# Docker Hub description (source of truth)

This file is the source of truth for the public Docker Hub description of
`eldaratech/swarmcli`. After each user-visible change, paste the sections below
into the Docker Hub repo console (*Repository Settings → General → Description /
Full Description*). This is a manual step today; if it becomes a recurring chore
it can be scripted against the Docker Hub API later. `swarmcli-agent` keeps the
same file for its two images.

The image has two fields:

- **Short description** — one line, maximum ~100 characters.
- **Full description** — Markdown, rendered on the repo page. Relative links do
  **not** resolve there, so every link below is absolute.

---

### Short description

```
Keyboard-driven Docker Swarm TUI. One image, both editions; Business features need a licence.
```

### Full description

Paste everything between the two markers below (markers excluded):

<!-- BEGIN swarmcli full description -->
## SwarmCLI

A keyboard-driven terminal UI for Docker Swarm: stacks, services, tasks, nodes,
logs, secrets and configs, driven from one screen instead of a dozen
`docker service` incantations. Single Go binary, no agent to install on the
cluster, works against a local socket or any Docker context.

- Website: https://swarmcli.io
- Source: https://github.com/Eldara-Tech/swarmcli
- Documentation: https://github.com/Eldara-Tech/swarmcli/tree/main/docs

### Which tag to pull

One image, two builds of the same command:

| Tag | Contains | Licence |
|---|---|---|
| `:<version>`, `:latest` | the whole product — Business Edition features compiled in and **inert** until a licence verifies | this repository's code is Apache-2.0; the licensed code is proprietary |
| `:<version>-oss` | the Community Edition and nothing else | wholly Apache-2.0 |

`:<version>-oss` is not a cut-down build — it is the whole Community Edition,
published for anyone who needs an artefact that is verifiably open source.
`swarmcli version` names which one you are running. Tags carry a leading `v`
(`v2.1.0`), matching the GitHub release tags.

### Run it

```bash
docker run --rm -it --pull always \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$HOME/.docker/contexts:/root/.docker/contexts" \
  -v "$HOME/.config/swarmcli:/root/.config/swarmcli" \
  -e EDITOR=vi \
  eldaratech/swarmcli:latest
```

The TUI needs a TTY — keep `-it`, or the container starts and exits immediately.
Mounting `~/.config/swarmcli` is what makes a licence and your settings survive
`--rm`.

### Business Edition

Business features are already in this image and stay inert until a licence key
verifies; there is no second download. Get a key, including a free trial, at
https://swarmcli.io/be and pass it with `-e SWARMCLI_LICENSE=<key>`, or let the
TUI store it on the swarm.

The older `swarmcli-be` cask, Scoop manifest and `eldaratech/swarmcli-be` image
were retired in v2.1.0. They published this same build under a second name; pull
`eldaratech/swarmcli` instead.

### Other install channels

```bash
brew install Eldara-Tech/tap/swarmcli          # macOS / Linux
scoop install swarmcli                          # Windows (bucket: Eldara-Tech/scoop-bucket)
curl -fsSL https://swarmcli.io/install.sh | sh -s -- ~/.local/bin
```

`swarmcli-oss` is the Apache-2.0 build on both package managers.

### The one request it makes on startup

swarmcli makes exactly one outbound call per launch. It reports that an
installation exists and answers the update check in the same request.

**It sends** a random install id (generated on first run, not derived from
anything on your machine), the version and edition, the OS and CPU
architecture, how swarmcli was installed, whether the TUI or the controller is
asking, and the shape of the swarm **as counts** — nodes, managers, services,
and the Docker engine version.

**It does not send** the *names* of anything: services, images, stacks,
networks, volumes, nodes, your cluster, hostnames, command arguments or error
text. The receiving end turns the connection into a country and discards the
address before anything is written.

It is on by default and says so on screen on the first run that reports, before
anything leaves the machine: one line on the status bar, naming the `:telemetry`
command, which lists exactly the above on any run.

| Value | Usage report | Update check |
|---|---|---|
| unset | yes | yes |
| `SWARMCLI_TELEMETRY=off` | no | yes |
| `SWARMCLI_TELEMETRY=none` | no | no |

Full account, field by field: https://github.com/Eldara-Tech/swarmcli/blob/main/docs/license.md#usage-reporting

### Environment variables

| Variable | Effect |
|---|---|
| `SWARMCLI_LICENSE` | Business Edition licence key |
| `SWARMCLI_TELEMETRY` | `off` stops usage reporting; `none` stops every outbound request |
| `SWARMCLI_ENV` | `dev` enables pretty debug logs (default `prod`) |
| `LOG_LEVEL` | `debug`, `info`, `warn`, `error` |
| `EDITOR` | editor used for in-place config and secret editing |

### Support

Issues and discussion: https://github.com/Eldara-Tech/swarmcli/issues
<!-- END swarmcli full description -->
