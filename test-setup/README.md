# 🐳 Local Docker Swarm Test Environment

Spin up a 1× manager + N× worker Docker‑in‑Docker (DinD) Swarm locally, expose the manager’s Docker API on **localhost:22375**, and interact with it via a Docker context.

`worker` is a single scaled service, so the size of the swarm is a knob: `NODES=<n> bash test-setup/testenv.sh up` starts an n‑node swarm (the manager counts), and `docker compose up --scale worker=<n-1>` does the same by hand. The default is 3 — 1 manager + 2 workers.

> ⚠️ Security note: this setup binds the Docker API without TLS for local testing only. Do **not** expose it on an untrusted network.

---

## Prerequisites

- Docker Engine (with the **docker compose** plugin)
- Linux/macOS/WSL recommended
- Ports **22375/tcp** on localhost must be free

---

## Quick start

```bash
# From the repo folder that contains docker-compose.yml
docker compose up --build                    # 1 manager + 1 worker
docker compose up --build --scale worker=4   # 1 manager + 4 workers
```
You may get errors like
```
worker-2   | WARNING: ca-cert-placeholder.pem does not contain exactly one certificate or CRL: skipping
worker-1   | WARNING: ca-cert-corporate-proxy.pem does not contain exactly one certificate or CRL: skipping
manager-1  | WARNING: ca-cert-corporate-proxy.pem does not contain exactly one certificate or CRL: skipping
worker-2   | WARNING: ca-cert-corporate-proxy.pem does not contain exactly one certificate or CRL: skipping
worker-2   | time="2025-12-12T08:56:10.038970524Z" level=info msg="Starting up"
worker-1   | time="2025-12-12T08:56:10.038970875Z" level=info msg="Starting up"
manager-1  | time="2025-12-12T08:56:10.039031366Z" level=info msg="Starting up"
worker-2   | failed to start daemon, ensure docker is not running or delete /var/run/docker.pid: process with PID 1 is still running
manager-1  | failed to start daemon, ensure docker is not running or delete /var/run/docker.pid: process with PID 1 is still running
worker-1   | failed to start daemon, ensure docker is not running or delete /var/run/docker.pid: process with PID 1 is still running
worker-2 exited with code 1
worker-1 exited with code 1
manager-1 exited with code 1
dependency failed to start: container test-setup-manager-1 exited (1)
```

Then run

```bash
docker compose down -v
docker compose up --build --remove-orphans
```

Wait ~10–20 seconds for the Swarm to bootstrap (manager + workers join).

Check containers:
```bash
docker compose ps manager worker
```

Create a Docker context pointing to the manager API on 22375:
* If you're using devcontainer:
  ```bash
  docker context create swarmcli --description "Test SwarmCLI" --docker "host=tcp://host.docker.internal:22375"
  ```

* Otherwise:
  ```bash
  docker context create swarmcli --description "Test SwarmCLI" --docker "host=tcp://localhost:22375"
  ```


Use the new context:
```bash
docker --context swarmcli info | sed -n '/Swarm:/,/ClusterID/p'
docker --context swarmcli node ls
```

You should see **Swarm: active** and as many nodes as you scaled `worker` to, plus the manager.

---

## Verify from inside the manager (no hardcoded container names)

Container names get a project prefix (e.g. `test-manager-1`). Always resolve the container dynamically:

```bash
# Exec into the manager container in a robust way
docker exec -it $(docker compose ps -q manager) sh -lc 'docker info | sed -n "/Swarm:/,/ClusterID/p"'
docker exec -it $(docker compose ps -q manager) docker node ls
```

Alternatively, use the compose-native form (avoids dealing with names/IDs):
```bash
docker compose exec manager docker info | sed -n '/Swarm:/,/ClusterID/p'
docker compose exec manager docker node ls
```

> Note: The earlier example that used `docker exec -it test-manager-1 ...` can fail if your Compose project name is not `test`. Use the dynamic form above instead.

---

## Deploy a quick test service (optional)

```bash
docker --context swarmcli service create --name whoami --publish 8080:80 traefik/whoami:v1.10
curl -fsS http://localhost:8080
docker --context swarmcli service rm whoami
```

## Deploy a quick test stack (optional)

```bash
docker --context swarmcli stack deploy -c test-setup/test-stack.yml demo
```

---

## Tear down

```bash
# Stop and remove containers, networks, and volumes created by the stack
docker compose down -v
```

> 💡 **Tip**: If you encounter "process with PID 1 is still running" errors when starting, clean up completely and rebuild:
> ```bash
> docker compose down -v  # Remove containers and volumes
> docker compose up --build
> ```

### If port 22375 still appears to be listening

Very rarely the DinD dockerd inside the manager can linger. If `ss` shows the port still open:

```bash
sudo ss -ltnp | grep :22375 || sudo lsof -iTCP:22375 -sTCP:LISTEN -P -n
```

Fix by restarting your host Docker service (this won’t affect your system beyond restarting Docker):

```bash
# Linux (systemd)
sudo systemctl restart docker

# macOS / Docker Desktop: quit and relaunch Docker Desktop
# Windows / Docker Desktop: quit and relaunch Docker Desktop
```

After restart, re‑run:
```bash
docker compose up -d
```

---

## Tips

- To force a clean project prefix for container names, run:
  ```bash
  docker compose --project-name test-setup up -d
  ```
  Then your containers will be `test-manager-1`, `test-worker-1`, `test-worker-2`, etc.

- To point the CLI directly without creating a context (temporary):
  ```bash
  export DOCKER_HOST=tcp://localhost:22375
  docker info
  ```

---

## What’s included

- **manager**: DinD with Docker API published on **22375/tcp** (localhost)
- **worker**: DinD worker, scaled to as many replicas as you ask for. Each replica waits for the join token and joins the Swarm itself, which is why there is no `worker*-join` sidecar and no per-worker named volume — replicas of one service would share it, and two dockerds on one graph directory is a corrupted store
- **swarm-init**: one-shot helper to initialize the manager and generate a join token

---

## Known warnings

You’ll see deprecation warnings about binding the Docker API without TLS. This is expected for this local testbed. For any real environment, enable TLS with client verification.
