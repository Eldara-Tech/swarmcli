<!--
SPDX-License-Identifier: Apache-2.0
Copyright © 2026 Eldara Tech
-->

# Troubleshooting

Common errors and fixes for Business Edition deployments. For issues with
general TUI behaviour, see the
[swarmcli](https://github.com/Eldara-Tech/swarmcli) issue tracker.

## License

**"Your license key is invalid."** in the license dialog — which opens when a
license-gated feature is requested, not at startup.
The key did not verify. Usually it was truncated when copied or picked up an
extra newline; it can also mean the key was issued for a different product
build, or is too old for this release to accept. Re-paste it from its source,
and if that does not help, contact whoever issued it.

**"Your license expired on YYYY-MM-DD. / The 5-day grace period has ended."**
The key is past its grace window. Paste a fresh key into the dialog, or run
`swarmcli license install <file>`. Setting `SWARMCLI_LICENSE` and restarting
does **not** do it: neither the environment variable nor
`~/.config/swarmcli/license.key` is loaded on its own — they are install-*from*
sources, offered on the `:license` view as `<e>` and `<m>`, which store the key
in the swarm. See [License — Activation](license.md#activation) and [License —
Lifecycle](license.md#lifecycle-states).

**`:license` shows `Nodes: 12 of 10 — nothing is switched off`.**
The swarm has more nodes than the license allows. Nothing is blocked and no
feature is switched off — it is a commercial limit, not a technical one.
Either upgrade from the portal link beside it, scale the cluster down, or
treat it as informational. There is no equivalent line for users: nothing
counts against `max_users`. See [License — Limits](license.md#limits).

## Bootstrap

**"Cannot bootstrap from a managed context (foo-managed)."**
The current Docker context is itself a managed context produced by a
previous bootstrap. `:bootstrap` refuses to operate through the rbac-proxy.
`:contexts` and select the original (non-`-managed`) context, then retry.

**"Infrastructure stack swarmcli-infra is already running."**
Detection found all three services already active. If this is intentional
and you only wanted a status report, run `:bootstrap --check`. If you
genuinely want to redeploy, follow
[Bootstrap — Re-bootstrap](bootstrap.md#re-bootstrap-and-teardown) — do
not reach for `--force` on a live cluster.

**A worker-node shell times out (`waiting for attach: … i/o timeout`) while
manager-node shells work**, or `:bootstrap --upgrade` reports the stack
*"predates application-layer agent-net TLS (encrypted overlay)"*.
The stack still uses the legacy encrypted `swarmcli-agent-net` overlay, whose
lower-level transport some clusters block, dropping connectivity to worker
nodes. Run `:bootstrap --migrate` to move to application-layer mTLS. It is
**non-destructive** — your CA, admin token, managed context and RBAC database
are preserved (no re-onboarding). See [Migration](migration.md).

**`:bootstrap --migrate` stops with "connected through the rbac-proxy … refuses
to remove its own protected stack."**
The active Docker context reaches the daemon *through* the proxy — a managed
connection — so migrate cannot tear down the stack it would recreate. This
happens when the context is a `<name>-managed` one, or when `DOCKER_HOST` /
`DOCKER_CONTEXT` points at the proxy port. Migrate must run with **direct daemon
access**: `:contexts` and select the original (non-`-managed`) context (or unset
`DOCKER_HOST`), then retry. The stopped run makes no changes, so it is safe to
re-run once you have switched.

**Bootstrap fails at "deploy stack" with a port-in-use error.**
Another process on the manager is bound to the chosen port (default
`2376`). Either free the port, or pick another:
`:bootstrap --port 12376`.

**Bootstrap succeeds but the new managed context cannot connect** —
`Cannot connect to the Docker daemon at tcp://…:2376`.
The auto-detected proxy host is not reachable from your workstation. The
common reason is that the manager's `Status.Addr` is an internal cluster
IP (multi-NIC, NAT, or a containerised dev environment). Tear the bootstrap
down (the four commands in
[Bootstrap — Re-bootstrap](bootstrap.md#re-bootstrap-and-teardown)) and
re-run with `:bootstrap --host <reachable-host>` to bake the right value
into the cert SAN and the context endpoint.

**Bootstrap succeeds but does not switch to the managed context.**
The success screen says which of the three reasons applies. If it names
`DOCKER_CONTEXT`, that variable takes precedence over the active context, so
`docker context use` — and therefore the switch — would not affect the running
session; unset it and use `:contexts`. If it reports that the proxy did not
answer, the deployment or the proxy host's reachability is the real problem —
work through *"the new managed context cannot connect"* and *"port-in-use"*
above. If you pressed Esc, the wait was cancelled on purpose. In every case
`:contexts` still switches by hand.

**`:bootstrap --check` reports `agent-manager: false` but `agent: true`.**
The agent-manager service is not running on a manager. Inspect with
`docker service ps swarmcli-infra_agent-manager` and look at the latest
task's error column. Most often it's a placement-constraint miss (the
service requires a manager node and your cluster has none in the
expected state).

## Shell

**`403 exec on protected stack requires admin role` when pressing `x` on a
service.**
The current managed context belongs to a non-admin user. Switch to an
admin context with `:contexts`, or escalate the user with
`swcproxy user delete` + re-add as `--admin` (see
[RBAC — `swcproxy` CLI](rbac.md#the-swcproxy-cli)).

**`EXEC_ERROR: no shell available`.**
The container image has no `bash`, `sh`, or `ash` at the standard paths.
Set `SWARMCLI_SHELL_CMD` to a command that exists in the image (for
distroless or scratch-based images, you may have no usable shell at all
and need a debug sidecar instead).

**WebSocket connect fails immediately, no error message.**
The rbac-proxy URL is wrong or unreachable. Try
`docker --context <name>-managed ps` from a shell — if that fails, the
issue is at the Docker context / network level, not BE-specific. See
the bootstrap-host troubleshooting above.

## Reveal-secret

**"Reveal timed out — no output captured."**
The temporary service did not produce log output within 20 seconds. Most
common causes:

- The reveal image (default `alpine:latest`) is not on the node and the
  pull is slow or blocked. Use `SWARMCLI_REVEAL_IMAGE` with an image
  already cached on the node, or pre-pull.
- The node has no scheduling capacity. Inspect with
  `docker service ps swarmcli-reveal-<name>-<ts>`.
- The secret is not mounted at `/run/secrets/<name>`. This shouldn't
  happen for stock Docker, but if you see it, file an issue with the
  secret's metadata.

**Reveal returns garbled binary output.**
The secret is binary, not text. The view shows the raw bytes; if it
parses cleanly as base64 it also shows a decoded form. Use the raw value
for binary secrets.

**`Forbidden` (403) when invoking reveal.**
The current user is not an admin (reveal-secret creates a temporary
service, which is a protected mutation). Switch to an admin context.

## Port-forward

**"Port-forward requires a valid Business Edition license."**
The license is missing or expired. Open `:license` and press `<s>` to
enter your key.

**"local port 8080 is already in use; pick another."**
Something else on your machine is bound to that port. Choose a different
local port, or pass `0` to let the OS pick an ephemeral one — the chosen
port will be displayed in the dialog and the list view.

**"local ports below 1024 are not supported; pick 1024–65535."**
Privileged-port binding requires root. Use a port ≥ 1024.

**"forward closed: target port not reachable."**
The container is up but nothing inside it is listening on the requested
port. Check the service's exposed ports — typo, wrong port number, or
the service has not finished starting. The forward closes the local
listener as soon as the agent reports `dial: connection refused`.

**"forward closed: agent disconnected."**
The on-node agent is unreachable: the agent service has crashed or
restarted, or its network path broke. Run `:bootstrap --check` to verify
the infrastructure stack is healthy. The forward does **not**
auto-reconnect — reopen it once the agent is back.

**"forward target task is no longer running."**
The container backing the forward has restarted, scaled away, or moved
to a different node. Reopen the forward — it will pick whichever replica
is alive when you reopen, which may now live on a different node.

**"forward on protected stack is not permitted" (admin gets a 403).**
Port-forwarding into the bootstrap stack itself (default
`swarmcli-infra`) is blocked for everyone — admin too. This is by design;
there is no override.

**"Permission denied: forwarding to <stack> requires admin role."**
Non-admin user attempting to forward to a target gated to admin only.
Switch to an admin context, or have the relevant target made
non-protected if appropriate.

**The forward suddenly closes after ~30 minutes of inactivity.**
That's the idle-timeout. Override with
`SWARMCLI_FORWARD_IDLE_TIMEOUT=2h` (or any Go duration up to 24 h)
before launching SwarmCLI.

**Port-forward never works on a Docker Desktop swarm, including WSL2.**
This one is a property of the environment, not a misconfiguration, and no
amount of re-bootstrapping fixes it.

Port-forward works by entering the target container's network namespace
through the host's procfs, which the agent receives as
`/proc:/hostproc:ro`. Docker Desktop runs its daemon inside a VM and
stages every bind mount through it, so that mount arrives with a
rewritten source:

```
"Source": "/run/desktop/mnt/host/wsl/docker-desktop-bind-mounts/Ubuntu/71ad1df…"
```

`/hostproc` is then not the procfs whose process IDs the daemon reports.
The agent inspects a running container, gets a live PID, and opening
`/hostproc/<pid>/ns/net` fails with `ENOENT`. It cannot be worked around:
the daemon reports PIDs from the VM's namespace, and Swarm services have
no `pid: host`.

Two different problems produce that same `ENOENT`, with opposite
remedies — no procfs mounted at all (a stale stack template, fixed by
`:bootstrap --upgrade`), versus a procfs that is mounted but lacks the
PID (this case, or a `hidepid` mount). Agent v1.6.0-rc1 and later tell
them apart; older agents always blamed the mount and sent people to
re-bootstrap a perfectly correct swarm.

SwarmCLI will not offer an upgrade for it. A bind mount whose source the
daemon has rewritten is recognised as an environment override rather
than drift, so `:bootstrap --upgrade` leaves it alone and the infra
nudge ignores it — otherwise every upgrade would restart the agent and
converge on nothing.

**Everything else is unaffected.** Shell, logs, volumes, health and pull
progress do not need the host procfs. Only port-forward does. To use
port-forward, run against a swarm whose nodes run `dockerd` natively on
Linux.

## RBAC users

**Onboarding URL returns "token expired" or "not found".**
Onboarding tokens are single-use and time-limited (default 24 h). Generate
a fresh one with `swcproxy user regenerate-token <name>`.

**`docker context import` succeeds but `docker --context <name>-managed ps`
returns `tls: bad certificate`.**
The cert in the bundle was signed by the bootstrap CA, but the client is
validating against a different CA — usually because a `--force`
re-bootstrap rotated the CA between issuance and use. Re-onboard the
user to get a cert signed by the new CA.

**A deleted user can still connect for a while.**
This shouldn't happen — the rbac-proxy looks up the cert CN on every
request and a deleted user's record is gone immediately. If you see
otherwise, restart the rbac-proxy service to flush any caches:
`docker service update --force swarmcli-infra_rbac-proxy`.

## Managed context disappeared

**`docker context use foo-managed` says the context does not exist.**
Docker contexts live under `~/.docker/contexts/`; deletion or a clean of
that directory removes them. Re-create by re-running `:bootstrap` (which
will refuse if the stack is already running — see the teardown commands
in [Bootstrap — Re-bootstrap](bootstrap.md#re-bootstrap-and-teardown)) or
by manually re-importing if you have the original cert files saved.

## Installation

**`brew install swarmcli-be` fails, or `scoop install swarmcli-be` cannot find
the package.** The `swarmcli-be` names were retired on 2026-09-11; install
`swarmcli`, which is the same build. See [Installation → The retired
`swarmcli-be` names](installation.md#the-retired-swarmcli-be-names).

**`scoop install swarmcli` cannot find the package.**
Add the bucket first: `scoop bucket add eldara https://github.com/Eldara-Tech/scoop-bucket`.

**Docker container starts but immediately exits.**
The TUI requires a TTY. Pass `-it` to `docker run` and ensure your
terminal is interactive.

## Asking for help

If none of the above matches, capture:

1. The startup line (`swarmcli 2>&1 | head -n 1`) — confirms version and
   edition.
2. `:bootstrap --check` output (if the issue is bootstrap- or
   proxy-related).
3. The relevant log file:
   - Production builds: `~/.local/state/swarmcli/app.log` (JSON).
   - Dev builds: `~/.local/state/swarmcli/app-debug.log` (pretty).

…and open a [issue](https://github.com/Eldara-Tech/swarmcli/issues)
or contact the support channel that came with your license.

## Charts

**`docker stack rm` removed the services, but swarmcli still lists the release.**
Expected. The chart engine stores each revision as a Docker config named
`swarmcli.release.<release>.v<n>`, labelled `com.swarmcli.type=release` — not
`com.docker.stack.namespace` — so a stack removal never touches them. A
torn-down application can therefore still report as synced.

Remove the release through swarmcli rather than through `docker stack rm`, or
delete the configs by label:

```bash
docker config ls --filter label=com.swarmcli.type=release
```

Tearing the whole swarm down and re-initialising also clears them, which is what
a test environment should do between runs.
