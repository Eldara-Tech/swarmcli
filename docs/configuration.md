<!--
SPDX-License-Identifier: Apache-2.0
Copyright © 2026 Eldara Tech
-->

# Configuration

This page is the single source of truth for SwarmCLI's environment variables and
on-disk paths. Rows marked **BE** apply to the Business Edition only; the rest
apply to every build.

## Environment variables

| Variable | Edition | Purpose | Default | Read at |
|---|---|---|---|---|
| `SWARMCLI_ENV` | both | `dev` writes human-readable logs, `prod` writes JSON. | `prod` | startup |
| `LOG_LEVEL` | both | Log verbosity: `debug`, `info`, `warn`, `error`. | `debug` in dev, `info` in prod | startup |
| `DOCKER_CONTEXT` | both | Docker context to talk to. It overrides `docker context use`, so while it is set the context switcher refuses to move to a different context rather than writing a switch that could not take effect. | the active context | startup |
| `SWARMCLI_TELEMETRY` | both | Governs the one request swarmcli makes at startup. Unset reports usage and checks for updates. `off` stops [usage reporting](license.md#usage-reporting) — no install id, nothing about your machine — while still telling you when a newer release exists. `none` makes no outbound request at all, which is what an air-gapped or policy-restricted cluster wants. | unset (reporting on) | startup |
| `SWARMCLI_MOUSE` | both | `off` starts the TUI without mouse support, so the terminal's own text selection works without a modifier key. With it on — the default — the wheel moves the selection, a click selects a row and a double click opens it; hold Shift (Option in iTerm2) to select text. `:mouse` toggles it for the session either way. Accepts the same spellings of off as `SWARMCLI_TELEMETRY`; anything else leaves it on. | unset (mouse on) | startup |
| `SWARMCLI_CHARTS_ALLOW_PLAINTEXT` | both | Allows chart repositories served over plain `http://`, which are refused by default (see [charts/README.md](../charts/README.md#transport)). | unset (https only) | `charts` commands |
| `SWARMCLI_CHARTS_NO_AUTO_UPDATE` | both | Stops a `charts` command refreshing a repository index before resolving a chart from it. `--no-repo-update` does the same for one invocation. | unset (refreshes) | `charts` commands |
| `EDITOR` | both | Editor invoked by the in-TUI edit actions (stack, config, secret). | `nano` | edit action |
| `XDG_STATE_HOME` | both | Base directory for logs and chart state — see [On-disk paths](#on-disk-paths). | `~/.local/state` | startup, `charts` commands |
| `SWARMCLI_LICENSE` | BE | A license key to install *from*. It is never loaded on its own — the active key is the one stored on the swarm — so setting this and restarting licenses nothing. When it is set, the `:license` view offers `<e>` to install it into the connected swarm. | unset | `:license` view |
| `SWARMCLI_DISABLE_LICENSE_RENEWAL` | BE | Stops **both** licensing requests, on every license type: swarmcli builds no license client at all, so neither the daily token refresh nor a [managed license](license.md#managed-licenses-activation-is-a-second-step)'s lease renewal has anything to make it. Leases are then installed from a file, and `swarmcli license activate` refuses rather than hanging. The token refresh is the one a static or unbound license makes — it is what carries a renewal, a tier change or a rolled [free-tier](license.md#the-free-tier) term — so this switches something off for those too. | unset | startup |
| `SWARMCLI_LICENSE_API_URL` | BE | Base URL of the license service renewals are asked of. Pointing it elsewhere cannot grant anything — every artifact is verified against a key compiled into swarmcli. | `https://swarmcli.io/api/v1` | startup |
| `SWARMCLI_PROXY_URL` | BE | WebSocket URL of the rbac-proxy. Auto-derived from the active Docker context if unset. | unset | shell connect |
| `SWARMCLI_REVEAL_IMAGE` | BE | Image used for the temporary service that reveals a secret. | `alpine:latest` | reveal action |
| `SWARMCLI_SHELL_CMD` | BE | Shell command to exec when opening a shell into a task. If unset, the agent auto-detects (`bash` → `sh` → `ash`). | unset | shell connect |
| `SWARMCLI_FORWARD_IDLE_TIMEOUT` | BE | Idle timeout for an active port-forward (no traffic in either direction). Accepts any Go duration; capped at `24h`. | `30m` | per-forward, evaluated continuously |

The three on/off variables (`SWARMCLI_CHARTS_ALLOW_PLAINTEXT`,
`SWARMCLI_CHARTS_NO_AUTO_UPDATE`, `SWARMCLI_DISABLE_LICENSE_RENEWAL`) accept the
values Go's `strconv.ParseBool` does — `1`, `t`, `true`, `TRUE` and their false
counterparts. Anything else is treated as unset.

`SWARMCLI_TELEMETRY` is not one of them: it has three states rather than two,
because "send no usage data" and "make no network request" are different asks
and one variable saying both is clearer than two variables each saying half.

| Value | Usage report | Update check |
|---|---|---|
| unset | yes | yes |
| `off`, `false`, `0`, `no` | no | yes |
| `none`, `silent` | no | no |

Anything it does not recognise leaves reporting **on**. That is the safer
direction for a value whose absence also means on: a typo in a chart cannot
silently switch it off, which is a failure nobody notices until the numbers are
already wrong.

`SWARMCLI_DISABLE_VERSION_CHECK` was the earlier spelling of `none` and is still
honoured, so nothing that sets it breaks — an explicit `SWARMCLI_TELEMETRY`
always wins. New configuration should use the table above.

See [License — Activation](license.md#activation) for how `SWARMCLI_LICENSE`
and the license file are installed into a swarm. There is no precedence between
them, because neither is loaded on its own, and
[Features](features.md) for how `SWARMCLI_REVEAL_IMAGE`,
`SWARMCLI_SHELL_CMD`, and `SWARMCLI_FORWARD_IDLE_TIMEOUT` are used.

## The Docker context

swarmcli resolves the Docker context once, at startup — from `DOCKER_CONTEXT`
if it is set, otherwise from the active context — and then addresses that one
context for the rest of the session. Everything it does goes to the same
swarm: the lists, the logs, `stack deploy`, and every `docker` command it runs
for you.

So running `docker context use <other>` in another terminal does **not** move a
running swarmcli. Instead, within a few seconds it asks:

```
The Docker context changed outside swarmcli: 'a' → 'b'.

swarmcli is still using 'a'. Switch to 'b'?
```

Answer `y` to follow the switch — swarmcli drops its connection, reconnects to
`b` and reloads the cluster. Answer `n` to stay on `a`; you are not asked about
`b` again unless the context changes to something else, or changes back to `a`
and away again. Switching from inside swarmcli, with `:contexts`, needs no
prompt and still runs `docker context use`, so your shell follows along.

## On-disk paths

| Path | Edition | Mode | Contents |
|---|---|---|---|
| `~/.local/state/swarmcli/app.log` | both | `0600` | JSON logs (`SWARMCLI_ENV=prod`). Rotated at 20 MB, 5 compressed backups, 14 days. |
| `~/.local/state/swarmcli/app-debug.log` | both | `0600` | Human-readable logs (`SWARMCLI_ENV=dev`), rotated the same way. |
| `~/.local/state/swarmcli/charts/repos.json` | both | `0644` | Chart repositories configured with `swarmcli charts repo add`. |
| `~/.local/state/swarmcli/charts/cache/index-<repo>.yaml` | both | `0644` | Cached repository index per configured repository. |
| `~/.local/state/swarmcli/charts/cache/charts/<sha256>.tgz` | both | `0644` | Chart archives already downloaded, kept under the sha256 their repository index publishes. Re-verified on every read, and swept 30 days after the last one. |
| `~/.config/swarmcli/update-notice.json` | both | `0644` | The release version at which the startup update notice was dismissed. |
| `~/.config/swarmcli/license.key` | BE | `0600` | A license key to install *from*, if you put one there. Nothing in swarmcli writes this file and nothing loads it automatically; when it exists, the `:license` view offers `<m>` to install it into the connected swarm. The active key lives in the swarm, as the Docker config `swarmcli-license`. |
| `~/.config/swarmcli/certs/<stack>/ca.pem` | BE | `0600` | CA cert from `:bootstrap`. |
| `~/.config/swarmcli/certs/<stack>/cert.pem` | BE | `0600` | Admin client cert (CN = seed username). |
| `~/.config/swarmcli/certs/<stack>/key.pem` | BE | `0600` | Admin client private key. |
| `~/.docker/contexts/…` | BE | (Docker default) | Managed Docker contexts — the `<original>-managed` entry created by `:bootstrap` and any `<user>-managed` entries imported via `docker context import`. |

Everything under `~/.local/state/swarmcli/` moves with `XDG_STATE_HOME` when
that is set, and falls back to the system temp directory when there is no home
directory. Directory mode is `0755` there, and `0700` for
`~/.config/swarmcli/certs/<stack>/`. `<stack>` is the bootstrap stack name;
default `swarmcli-infra`.

Release state is not on disk: `swarmcli charts` stores each release's values and
rendered manifest in the swarm itself, as Docker configs.

## Stack-side configuration (rbac-proxy)

The `:bootstrap` command renders an embedded stack template. A handful of
proxy-side environment variables are set on the rbac-proxy service and
are useful to know when operating the deployment:

| Variable | Purpose | Set by bootstrap |
|---|---|---|
| `PROXY_LISTEN` | mTLS listen address. | `:<port>` (default `:2376`). |
| `PROXY_INTERNAL_LISTEN` | Plaintext loopback listener for management calls. | `127.0.0.1:2375`. |
| `PROXY_DOCKER_SOCKET` | Backend socket. | `/var/run/docker.sock`. |
| `PROXY_AGENT_MANAGER_URL` | Where the proxy reaches the agent-manager. | `tcp://swarmcli-infra_agent-manager:8080`. |
| `PROXY_STORE` / `PROXY_DATABASE_PATH` | User/role store. | `sqlite` at `/data/proxy.db` (volume `proxy-data`). |
| `PROXY_TLS_*` | TLS material — server cert/key, client CA cert/key. | Mounted from Docker secrets. |
| `PROXY_ADMIN_TOKEN_FILE` | Bearer token for the management API. | Mounted from the `_admin-token` secret. |
| `PROXY_EXTERNAL_URL` | URL embedded into onboarding bundles. | `tcp://<host>:<port>`. |
| `PROXY_SEED_USERNAME` / `PROXY_SEED_ROLE` | Bootstrap admin user. | `admin` / `admin`. |
| `PROXY_ONBOARDING_TOKEN_TTL` | Time-to-live for onboarding tokens. | Default `24h`. |

Changing these requires editing the deployed service (`docker service update`)
or re-running `:bootstrap` from scratch — see
[Bootstrap — Re-bootstrap](bootstrap.md#re-bootstrap-and-teardown).

## Stack-side configuration (agent)

One agent-side variable is worth knowing when using [Volumes](volumes.md):

| Variable | Purpose | Set by bootstrap |
|---|---|---|
| `FILEOP_MAX_UPLOAD_BYTES` | Maximum size of a single volume file/directory upload; the agent rejects a larger payload with `413`. | `2147483648` (2 GiB). |

Deployments bootstrapped before this was templated fall back to the agent's
built-in 2 GiB default. Raise or lower it the same way as the proxy-side
variables above — `docker service update` the agent service, or re-run
`:bootstrap`.
