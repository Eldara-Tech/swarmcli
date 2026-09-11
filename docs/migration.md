<!--
SPDX-License-Identifier: Apache-2.0
Copyright © 2026 Eldara Tech
-->

# Migrating to application-layer mTLS

Releases up to and including **v1.6.0** put the internal `swarmcli-agent-net`
overlay in **encrypted** mode. Some clusters permit the standard overlay ports
to worker nodes but block the lower-level transport an encrypted overlay
relies on, which silently breaks connectivity to tasks on those nodes — so
**shells (and port-forwards) into tasks on worker nodes time out** while
manager-node tasks work.

The fix moves confidentiality and authentication onto **application-layer
mutual TLS** for the internal services, so `swarmcli-agent-net` can be a plain
overlay. A Docker overlay's `encrypted` option cannot be changed in place, so
moving to the new model is a one-time migration that recreates the overlay — it
is **not** an images-only `:bootstrap --upgrade`.

## The one command

After upgrading the binary (see [Installation →
Upgrade](installation.md#upgrade)):

```
:bootstrap --migrate
```

`:bootstrap --migrate` shows a confirmation summarising exactly what is kept
and what is recreated, then performs the migration on accept. Port and proxy
node are detected from the running stack — you don't pass any other flags.

> **Run this from the original (non-managed) context** — direct daemon access on
> a swarm manager, *not* the `<name>-managed` context that talks through the
> rbac-proxy. Migrate tears down and recreates the rbac-proxy and
> `swarmcli-agent-net`, and a managed session connects *through* that proxy, which
> refuses to delete its own protected stack. Run from a managed context (or with
> `DOCKER_HOST`/`DOCKER_CONTEXT` pointing at the proxy), `:bootstrap --migrate`
> stops immediately with a switch-context message and makes **no** changes.

> `:bootstrap --upgrade` (images-only) deliberately **refuses** a pre-mTLS
> stack and points you here: an images-only update cannot change the overlay.

## It is non-destructive

The migration **preserves your identity and data** — there is no
re-onboarding and no certificate redistribution:

| Preserved (untouched) | Recreated |
|---|---|
| User CA and all issued user certificates | `swarmcli-agent-net` (now a plain overlay) |
| Admin token | The internal mTLS certificates |
| Your managed Docker context | The infra services (rolling redeploy) |
| The RBAC user database | |

The only interruption is the few seconds the stack takes to reconverge: active
shell sessions and port-forwards drop and must be reopened. Existing user
Docker contexts keep working afterwards — their certificates are unchanged.

## Order of operations

1. **Upgrade the binary first** so it deploys the matched, mTLS-capable agent
   and rbac-proxy images:
   ```
   brew upgrade swarmcli             # Homebrew
   scoop update swarmcli              # Scoop
   docker pull eldaratech/swarmcli:latest      # Docker
   ```
2. From the **original (non-managed) context** on a swarm manager (see the
   callout above for why), run `:bootstrap --migrate` and accept the
   confirmation.
3. Switch back to your managed context with `:contexts` if needed — it was
   preserved, so nothing else changes.

Migration is opt-in: an un-migrated stack keeps working for manager-node
shells. Worker-node shells on affected clusters keep failing until you
migrate.

## Verifying

- `:bootstrap --check` reports the deployed component versions.
- A shell (`x`) into a task on a **worker** node now connects instead of timing
  out with `waiting for attach: … i/o timeout`.

If a worker-node shell still reports that the stack uses the *legacy encrypted
agent-net overlay*, the migration has not been run yet — run `:bootstrap
--migrate`.
