<!--
SPDX-License-Identifier: Apache-2.0
Copyright © 2026 Eldara Tech
-->

# The SwarmCLI app

The SwarmCLI app for iPhone and Android shows a swarm's health, its services and
their logs, and restarts, scales and rolls back services. It is free. More about it,
with screenshots, is at <https://swarmcli.io/app>.

Press `c` in SwarmCLI, or type `:connect`, for a QR code that gets the app. Views
where `c` already does something (Clone in configs; Create in contexts, networks
and volumes) keep it, and `:connect` works there.

The app has a built-in demo swarm, so it can be tried without connecting anything.

## What connecting needs

The app talks to your swarm's rbac-proxy, the same endpoint a `<user>-managed`
context uses, over mutual TLS: the app checks the proxy's certificate and the proxy
checks the phone's. Eldara runs no relay. So connecting a phone needs:

- the rbac-proxy, which [`:bootstrap`](bootstrap.md) installs;
- a license on the swarm to add the phone's user — any license, including the
  [free one](license.md#the-free-tier);
- a network path from the phone to the proxy's port, `2376` by default (see below).

Restart, scale and roll back also need a valid license on the swarm. Without one —
including after a license lapses — the app keeps showing health, services, nodes
and logs, and says what an admin can do.

## Connecting a phone

1. Press `c` in SwarmCLI as an admin. The Connect tab suggests a device name such
   as `alice-phone` and a role (see [Which role](#which-role-to-give-a-phone)), and
   shows the address the phone must reach.
2. Press Enter. SwarmCLI adds a user for the phone on the proxy and shows a one-time
   QR code. Nothing is created until you press Enter.
3. In the app, tap **Scan QR code**. The code carries the onboarding URL and a pin of
   the proxy's server key; the app accepts the proxy only if its key matches the pin,
   so nobody in the middle can hand the phone a different certificate authority.

The code works once and expires after 24 hours by default
(`PROXY_ONBOARDING_TOKEN_TTL`); SwarmCLI shows when. If the phone has the app
installed, its camera can open the code too; without the app, the camera cannot,
so get the app first (the popup's **Get the app** tab).

Users created in `:users` work as well: after creating one, press `m` for its QR
code. If the terminal is too small for a code, SwarmCLI says so rather than draw
part of it.

The app keeps the certificate and key in the Keychain on iOS or the Keystore on
Android, and asks for Face ID, a fingerprint or the passcode before it shows your
swarms.

## Reaching the proxy

**A private network (recommended).** Put the phone and the swarm's managers on the
same private network — Tailscale, WireGuard or a company VPN — and keep `:2376`
closed to the internet. The onboarding link has to name an address the phone can
reach over that network, so set `PROXY_EXTERNAL_URL` to it: a MagicDNS name, a
WireGuard IP, or an internal DNS name the proxy's server certificate covers. An
address such as `localhost` names the machine running SwarmCLI and cannot work from
a phone; the Connect tab warns about it.

**Exposing `:2376` to the internet.** Every data request needs a client certificate
issued by the swarm's own CA, and without one the proxy answers 401. If you do this:

- expose only `:2376`, never the Docker socket or `:2375`;
- make sure the proxy's server certificate names the public hostname;
- firewall the port to the address ranges your phones use, where practical;
- know what is open without a certificate: the onboarding endpoint accepts anyone
  holding a valid one-time token, which expires and works once;
- watch the proxy's audit log for denied requests.

A TLS-terminating load balancer in front of the proxy changes the certificate the
phone sees, and the app then refuses to enrol: the pin is the proxy's own key.

## One user per phone

Give every phone its own proxy user rather than reusing someone's laptop user:

- **Revoking is per user.** Disabling or deleting the user in `:users` (or
  `swcproxy user rm`) cuts that phone off at once. There is no certificate
  revocation list, so the user is how a device is revoked; a shared user means a
  lost phone takes the laptop's access with it.
- **The audit log names the user**, so it shows which device made a change.

## Which role to give a phone

| Role | The app can | Use it for |
|---|---|---|
| `responder` | see health, services, nodes, tasks and logs; restart, scale and roll back services | phones of people expected to fix things — the Connect tab's default |
| `viewer` | see health, services, nodes, tasks and logs | on-call awareness |
| `operator` | the same from the app, but the certificate can also create services and exec; a service can mount the host, so it controls the nodes | not offered for phones; avoid |
| `admin` | everything | avoid on phones |

`responder` is a built-in role of rbac-proxy releases that include it (see
[RBAC](rbac.md#roles)); on an older proxy the Connect tab offers `viewer` only. The app reads its permissions from the
proxy and hides what it may not do, and the proxy enforces them regardless.

## Removing a phone

Disable or delete its user in `:users`. Removing the swarm in the app deletes the
stored certificate and key from the phone, but deleting the user is what revokes
access.
