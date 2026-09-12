<!--
SPDX-License-Identifier: Apache-2.0
Copyright © 2026 Eldara Tech
-->

# Bootstrap

The `:bootstrap` command turns a vanilla Docker Swarm into a managed Swarm in
one shot: it generates an mTLS PKI, provisions Docker secrets, deploys the
SwarmCLI infrastructure stack (rbac-proxy + agent + agent-manager), and
registers a Docker context that talks to the cluster through the proxy.

After bootstrap, the rbac-proxy stands between you and the Docker Engine API.
Identity is established by client certificate; authorization is enforced by
role. This is what unlocks Business Edition's per-user shell and reveal-secret
features — see [`features.md`](features.md) for what they do, and
[`rbac.md`](rbac.md) for how to add additional users.

## What gets deployed

```mermaid
flowchart TB
  subgraph host["Your machine"]
    cli["swarmcli (BE binary)"]
    ctx["Docker context:<br/>&lt;name&gt;-managed"]
    certs["~/.config/swarmcli/certs/<br/>swarmcli-infra/{ca,cert,key}.pem"]
    cli --> ctx
    ctx -. uses .-> certs
  end

  subgraph mgr["Swarm manager node"]
    proxy["rbac-proxy<br/>:2376 mTLS"]
    am["agent-manager"]
    ren["licence-renewer<br/>swarmcli license sync"]
    sock1[("/var/run/docker.sock")]
    proxy --> sock1
    am --> sock1
    ren --> sock1
  end

  ren -- "HTTPS" --> svc["swarmcli.io<br/>license service"]

  subgraph node["Each Swarm node"]
    ag["agent (global)"]
    sock2[("/var/run/docker.sock")]
    ag --> sock2
  end

  ctx -- "tcp://host:2376<br/>(mTLS)" --> proxy
  proxy --> am
  am -. "discovers" .-> ag
```

The bootstrap command creates the following artifacts:

**Stack** `swarmcli-infra`, four services:

- `rbac-proxy` — mTLS-fronted Docker API proxy. Listens on the chosen port
  (default `2376`). One replica, pinned to a manager node. Uses a SQLite
  store on the `proxy-data` volume for users and roles.
- `agent` — runs on every node (`mode: global`). Used by Business Edition
  features that need per-node access (interactive shell, secret reveal).
- `agent-manager` — one replica on a manager. Companion to `agent`: the
  rbac-proxy connects here on port `8080` to locate and reach per-node
  agents over the overlay.
- `licence-renewer` — one replica on a manager, running this same swarmcli
  image as `license sync --interval 6h`. It keeps the swarm's license current
  while nobody has a TUI open: it collects a token we re-signed — a renewal, a
  plan change, a rolled [free-tier](license.md#the-free-tier) term — and, on a
  [managed](license.md#managed-licenses-activation-is-a-second-step) license,
  renews the activation lease. Omitted by `--no-renewer`, and omitted by a
  development build, which publishes no image to deploy.

Six hours is a wake-up rather than a request rate: the client's own gates decide
whether anything is actually asked, and most passes ask nothing. A failed pass
logs and waits rather than exiting, because a service that exited on a transient
outage would be restarted into a crash loop.

**Networks.** `swarmcli-agent-net` — internal overlay shared by the proxy, the
agent and the agent-manager, and protected against accidental deletion from the
TUI. When the renewer is deployed it gets a second overlay of its own,
`swarmcli-renewal-net`, which is deliberately **not** internal: it is the only
network in the stack with a route off the cluster, and the renewer is the only
service on it, so the egress is confined to the one container that needs it.

**Docker secrets** (prefixed with the stack name, default `swarmcli-infra_`):

| Secret | Contents |
|---|---|
| `_ca-cert` | CA certificate (PEM) |
| `_ca-key` | CA private key (PEM) |
| `_server-cert` | Proxy server certificate (PEM) |
| `_server-key` | Proxy server private key (PEM) |
| `_admin-token` | Bearer token for the proxy's management API |

The admin token is delivered as a secret (not an environment variable) so it
cannot be read with `docker service inspect`.

**Local files** under `~/.config/swarmcli/certs/swarmcli-infra/` (mode `0600`,
directory `0700`):

- `ca.pem` — copy of the CA cert
- `cert.pem` — admin client certificate
- `key.pem` — admin client private key

**Docker context** named `<current-context>-managed` (e.g.
`default-managed`), pointing at `tcp://<host>:<port>` and configured with the
three cert files above.

## Prerequisites

- A Docker host with Swarm mode active (`docker info | grep -i swarm`).
- The current Docker context must connect to a Swarm manager — bootstrap
  needs to deploy a stack and create secrets.
- Free TCP port (default `2376`) on the manager you choose for the proxy.
- The current context must **not** itself be a managed context. Bootstrap
  refuses to run through the proxy. Switch back with `:contexts` first.
- Cryptographic curves used: ECDSA P-256 for all certificates. The CA is
  valid for 10 years; the server and client certs are valid for 1 year and
  must be re-issued (re-bootstrap or re-onboard the affected user) before
  expiry.

## Walkthrough

```text
1.  Open a SwarmCLI session pointing at your Swarm:
      swarmcli

2.  Run bootstrap:
      :bootstrap

3.  Confirm the host and port:
      Proxy host: <auto-detected manager IP>
      Proxy port: 2376

    Press Tab to switch fields, Enter to start, Esc to cancel.

4.  Wait for the run to complete. The view reports success or the
    underlying error.

5.  Nothing to do. The summary stays on screen while SwarmCLI waits for
    the rbac-proxy it just deployed to answer, then switches to the new
    context by itself and reloads the cluster through it.

    Esc stops the wait and leaves you on the original context; you can
    switch later with :contexts. See "After bootstrap" for the cases
    where the switch does not happen on its own.
```

To skip the interactive prompt, pass `--port`:

```text
:bootstrap --port 2376
```

## CLI flags

| Flag | Effect |
|---|---|
| `--check` | Report which infra services are running. Always allowed, including from a managed context. |
| `--upgrade` | Images-only re-deploy of the infra services to the compat-pinned tags. Touches nothing else — no TLS, secrets, admin token, context or RBAC DB — so it cannot sever identity. Rolling restart of the services; allowed from a managed context (the proxy enforces the admin role). If the stack predates app-layer mTLS, `--upgrade` refuses and points you at `--migrate`. |
| `--migrate` | Migrate a pre-mTLS stack (encrypted `agent-net`) to application-layer mTLS. **Non-destructive** — preserves the CA, admin token, managed context and RBAC DB. **Run from the original (non-managed) context** — see [Migration](migration.md). |
| `--port N` | Use port `N` (1–65535). Skips the interactive prompt. |
| `--host H` | Pre-fill or override the proxy host. Pre-fills the field in the interactive prompt; combined with `--port`, runs unattended. See [Host autodetection](#host-autodetection). |
| `--force` | Redeploy even if the stack is already running. **Not recommended on a live cluster** — see [Re-bootstrap](#re-bootstrap-and-teardown). |
| `--no-renewer` | Omit the `licence-renewer` service — the only one that talks outside the cluster. For air-gapped swarms, and for anyone running `swarmcli license sync` from their own scheduler. |

### The `Auto-renewal:` warning on `:license`

A swarm bootstrapped before the renewer existed cannot gain one from
`:bootstrap --upgrade`, which is images-only. Nothing renews the license while
swarmcli is closed, and until this line existed that happened in silence — so
`:license` reports it:

```
Auto-renewal: no licence-renewer service on this swarm
  Nothing renews the license while swarmcli is closed, so this license's term
  stops being rolled forward.
```

The consequence named depends on the license: a managed one loses its
activation renewal, a static one — every [free-tier](license.md#the-free-tier)
key among them — loses the annual roll of its term, which is the only thing
keeping it alive. The fix is `:bootstrap --force` from the original
(non-managed) context, which redeploys the stack and keeps the CA, users,
contexts and RBAC data; or run `swarmcli license sync` on your own schedule and
leave the stack alone. The line does not appear when there is nothing to say —
a perpetual key, `SWARMCLI_DISABLE_LICENSE_RENEWAL`, or a build with no renewer
image.

## Host autodetection

`:bootstrap` derives the proxy host from the first manager node's advertised
`Status.Addr` (the address the node uses to talk to the rest of the swarm).
You can see the same value with `docker node ls` and `docker node inspect`.
For most production setups this is the right answer: it is the IP that
other nodes use to reach the manager, and it is reachable from your
workstation as long as routing and firewalling between you and the cluster
are in place. If no manager address can be determined, bootstrap falls back
to `127.0.0.1`.

Override with `--host` (or by editing the value in the prompt) when:

- The manager is behind NAT or a load balancer, and you need to point at the
  externally reachable address instead of the in-cluster IP.
- The manager has multiple network interfaces and the auto-detected one is
  not the one you want clients to use.
- You are running against a containerised dev environment where the
  in-cluster IP is not routable from your host.

The host you choose ends up in two places:

- The `tcp://<host>:<port>` endpoint of the new Docker context — i.e. the
  address every TUI session uses to reach the proxy.
- The Subject Alternative Name on the server certificate. If the value
  parses as an IP it becomes an IP SAN; otherwise it becomes a DNS SAN.
  Either way, changing the host later means the cert no longer matches and
  clients will refuse to connect — you would need to re-bootstrap (or
  reissue the server cert).

## After bootstrap

Verify the deployment:

```bash
docker service ls --filter label=com.docker.stack.namespace=swarmcli-infra
```

You should see `swarmcli-infra_rbac-proxy`, `_agent`, and `_agent-manager`,
all with their replicas converged.

Inside the TUI, `:bootstrap --check` does the same check and reports each
component's status.

The new context is also visible via:

```bash
docker context ls
```

SwarmCLI switches to it for you. A freshly deployed proxy is not reachable
the moment the stack is deployed, so the success screen stays up while the
new context is polled, and the switch happens once it answers — normally a
few seconds. From then on every Docker API call goes through the rbac-proxy
and is authenticated as `admin`; the client certificate is picked up from
the context, with no environment variables to set.

The switch does not happen on its own in three cases, each of which says so
on the success screen and leaves you where you were:

- **`DOCKER_CONTEXT` is set.** That variable takes precedence over the
  active context, so switching would have no effect on the running session.
  Unset it and use `:contexts`, or restart with the managed context named.
- **The proxy never answers.** Something is wrong with the deployment or
  the host is not reachable from your workstation — see
  [Troubleshooting](troubleshooting.md#bootstrap).
- **You pressed Esc** during the wait.

`:contexts` remains the way to switch by hand, at any time.

To add additional users with their own certificates and roles, see
[`rbac.md`](rbac.md).

## Re-bootstrap and teardown

Bootstrap is idempotent in the safe direction: if the stack is already
running, the command refuses to redeploy and prints a hint about `--force`.

`--force` is destructive and should be reserved for development and CI
environments. It deletes the existing TLS/admin-token secrets before
recreating them, which:

- Invalidates the existing admin client certificate (you lose access to the
  managed context until you re-import it).
- Invalidates every user certificate previously issued by the proxy
  (everyone needs to be re-onboarded).
- Drops nothing in the SQLite store unless you also remove the `proxy-data`
  volume — the existing user/role records survive but their certs no longer
  validate against the new CA.

If you genuinely want to start over on a live cluster, the explicit path is:

```bash
docker stack rm swarmcli-infra
docker volume rm swarmcli-infra_proxy-data    # only if you also want to drop users/roles
docker context rm <original>-managed
rm -rf ~/.config/swarmcli/certs/swarmcli-infra
```

…then run `:bootstrap` again. Doing it this way keeps the destructive steps
visible and reviewable, and avoids partial states.

To remove the infra entirely, run the same four commands and stop there.

## Failure handling

`:bootstrap` performs the steps in order:

1. Refresh the swarm snapshot and detect existing infra.
2. Generate the admin token.
3. Generate the TLS bundle (CA + server cert + admin client cert).
4. Create the five Docker secrets.
5. Render and deploy the stack.
6. Write the local cert files.
7. Create the Docker context.

If any step after secret creation fails, bootstrap rolls back in reverse
order: it removes the context, removes the stack, deletes the secrets it
created, and removes the local cert directory. A failed run leaves your
cluster in the same state it was in before — no orphaned secrets, no
half-deployed stack, no stray context.

The error message identifies the failing step (snapshot refresh, secret
creation, stack deploy, context creation, …). The most common causes are
the proxy port being already in use on the manager, insufficient permissions
on the Docker socket, and trying to bootstrap from a context that is itself
already a managed context (caught by the guard before any work is done).
