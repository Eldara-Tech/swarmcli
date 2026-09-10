#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright © 2026 Eldara Tech

set -euo pipefail

# === Config ================================================================
COMPOSE_FILE="test-setup/docker-compose.yml"
MANAGER_HOST="tcp://localhost:22375"
KEEP="${KEEP:-0}"
FOLLOW="${FOLLOW:-0}"
SERVICE="${SERVICE:-}"
# How big a swarm to start, counted the way the Docker API counts it: the
# manager plus every worker, which is also the number a licence's node
# allowance is compared against. 3 is the topology this file has always
# brought up (1 manager + 2 workers), so leaving NODES unset changes nothing.
# `worker` is a single scaled compose service, so any N >= 1 works.
NODES="${NODES:-3}"
DOCKER_COMPOSE="docker compose -f $COMPOSE_FILE"
CONTEXT_NAME="swarmcli"

# === Colors ================================================================
RESET="\033[0m"
BOLD="\033[1m"
DIM="\033[2m"
RED="\033[31m"
GREEN="\033[32m"
YELLOW="\033[33m"
BLUE="\033[34m"
MAGENTA="\033[35m"
CYAN="\033[36m"

timestamp() {
  date -u +"%Y-%m-%dT%H:%M:%SZ"
}

# === Helpers ===============================================================
info() { echo -e "${CYAN}[$(timestamp)] [INFO]${RESET} $*"; }
ok()   { echo -e "${GREEN}[$(timestamp)] [OK]${RESET}   $*"; }
warn() { echo -e "${YELLOW}[$(timestamp)] [WARN]${RESET} $*"; }
err()  { echo -e "${RED}[$(timestamp)] [ERR]${RESET}  $*" >&2; }

run_or_warn() { "$@" || warn "Command failed: $*"; }

# Wait until the manager DinD exposes its Docker API on tcp://localhost:22375
wait_for_manager() {
  info "⏳ Waiting for DinD manager to be ready on ${MANAGER_HOST}..."
  local retries=30
  local wait_sec=2
  local i
  for i in $(seq 1 $retries); do
    if curl -fsS "${MANAGER_HOST}" >/dev/null 2>&1 || docker -H "$MANAGER_HOST" info >/dev/null 2>&1; then
      ok "Manager is ready!"
      return
    fi
    sleep "$wait_sec"
  done
  err "Manager did not become ready after $((retries * wait_sec)) seconds."
  exit 1
}

# Wait until every worker DinD node has joined the swarm and reports Ready.
#
# Each `worker` replica joins the swarm itself, and that join is asynchronous:
# before this gate, `up` returned as soon as the manager API was reachable, so
# `deploy` (worker image distribution) and the integration tests frequently ran
# against an effectively single-node swarm, so worker-placement tests had
# nothing to exercise. Blocking here makes the swarm match the topology that
# was asked for, so those tests are real, not skipped.
#
# Expected worker count is NODES minus the manager — the same number cmd_up
# scales the `worker` service to, so the gate and the topology cannot drift.
# `SKIP_WORKER_WAIT=1` bypasses the gate for deliberately single-node local
# runs, and `EXPECT_WORKERS` pins it for a trimmed topology. A worker that
# never joins becomes a loud, diagnosable failure here rather than a silent
# single-node degrade later.
wait_for_workers() {
  if [ "${SKIP_WORKER_WAIT:-0}" = "1" ]; then
    warn "SKIP_WORKER_WAIT=1: not waiting for worker nodes (single-node run)"
    return
  fi
  local expected
  expected="${EXPECT_WORKERS:-$((NODES - 1))}"
  if [ "$expected" -eq 0 ]; then
    return
  fi
  info "⏳ Waiting for ${expected} worker node(s) to join the swarm..."
  local retries=60
  local wait_sec=2
  local i ready
  for i in $(seq 1 $retries); do
    # Workers are nodes with an empty ManagerStatus; count those that are Ready.
    ready=$(docker -H "$MANAGER_HOST" node ls \
      --format '{{.Status}}|{{.ManagerStatus}}' 2>/dev/null \
      | grep -c '^Ready|$' || true)
    if [ "${ready:-0}" -ge "$expected" ]; then
      ok "All ${expected} worker node(s) joined and Ready."
      return
    fi
    sleep "$wait_sec"
  done
  err "Only ${ready:-0}/${expected} worker node(s) Ready after $((retries * wait_sec))s. Swarm state:"
  docker -H "$MANAGER_HOST" node ls >&2 || true
  # Each replica joins the swarm itself, so the reason it did not is in its
  # own log and nowhere else — `node ls` above can only report the absence.
  err "Worker logs:"
  $DOCKER_COMPOSE logs --tail=200 worker >&2 || true
  exit 1
}

# Ensure the context exists and points to a live daemon
ensure_context() {
  wait_for_manager
  if docker context inspect "$CONTEXT_NAME" >/dev/null 2>&1; then
    info "Checking if context '$CONTEXT_NAME' is alive..."
    if ! docker --context "$CONTEXT_NAME" info >/dev/null 2>&1; then
      warn "Context '$CONTEXT_NAME' points to a non-running daemon. Recreating..."
      docker context rm -f "$CONTEXT_NAME"
      docker context create "$CONTEXT_NAME" --docker "host=$MANAGER_HOST"
    fi
  else
    info "Creating Docker context '$CONTEXT_NAME'..."
    docker context create "$CONTEXT_NAME" --docker "host=$MANAGER_HOST"
  fi
}

# === Before cmd_up() =======================================================
cleanup_port() {
  # Remove any old container using 22375 to avoid "port already allocated"
  if docker ps -q --filter "publish=22375" | grep . >/dev/null; then
    info "🧹 Removing old container(s) using port 22375..."
    docker ps -q --filter "publish=22375" | xargs -r docker rm -f
    sleep 2
  fi
}

# === Commands ==============================================================

cmd_up() {
  if ! [[ "$NODES" =~ ^[1-9][0-9]*$ ]]; then
    err "NODES must be a positive integer (got '$NODES'). It counts the whole"
    err "swarm, manager included, so NODES=1 is a manager on its own."
    exit 1
  fi

  cleanup_port  # <<< ensure old manager gone

  info "🚀 Starting Swarm environment: ${NODES} node(s) (1 manager + $((NODES - 1)) worker(s))..."
  # --remove-orphans: this project's service names are not stable across
  # checkouts — the fixed worker1/worker2 pair became one scaled `worker`.
  # Containers of a service this file no longer declares are left running
  # otherwise, and they keep holding the `shared` volume, which is what makes
  # a later `down -v` report it as still in use.
  $DOCKER_COMPOSE up -d --remove-orphans --scale "worker=$((NODES - 1))"
  $DOCKER_COMPOSE ps

  info "🔧 Ensuring Docker context..."
  ensure_context

  # Block until workers have actually joined, so `deploy` distributes images
  # to them and the swarm matches its declared multinode topology before any
  # tests run.
  wait_for_workers

  ok "Swarm multinode environment is up."
}

cmd_deploy() {
  info "📦 Deploying test stack..."
  docker --context "$CONTEXT_NAME" stack deploy -c test-setup/test-stack.yml demo
  info "⏳ Waiting for services to start..."
  sleep 20
  docker --context "$CONTEXT_NAME" stack ls
  docker --context "$CONTEXT_NAME" service ls
  ok "Test stack deployed successfully."
}

cmd_test() {
  info "🧪 Running Go integration tests..."

  local test_name="${1:-}"   # optional single test
  local format="testname"    # default local format

  # CI-specific settings
  local junit_file=""
  if [[ "${CI:-0}" -eq 1 ]]; then
    format="github-actions"
    junit_file="/tmp/test-report.xml"
  elif [[ "${VERBOSE:-0}" -eq 1 ]]; then
    format="standard-verbose"
  fi

  local args=("--format=$format")

  # Add JUnit file only if set
  [[ -n "$junit_file" ]] && args+=("--junitfile=$junit_file")

  # Always include integration build tag. -timeout matches the locked-swarm and
  # ssh-context CI jobs: these tests deploy several releases each, and go test's
  # 10m default is a number nobody chose for them.
  local go_test_cmd=("-tags=integration" "-v" "-timeout=15m")

  # views/stacks carries an integration-tagged test alongside its unit tests:
  # the view's internals are unexported, so covering the deploy seam against a
  # real swarm has to happen in-package. Naming the package also re-runs its
  # (fast) unit tests, which is cheaper than widening this to ./... .
  local packages=("./integration-tests/..." "./views/stacks/...")

  if [[ -n "$test_name" ]]; then
    info "🎯 Running single test: $test_name"
    go_test_cmd+=("-run" "$test_name" "${packages[@]}")
  else
    info "🧩 Running all integration tests"
    go_test_cmd+=("${packages[@]}")
  fi

  # Combine into gotestsum command
  local full_cmd=("gotestsum" "${args[@]}" "--" "${go_test_cmd[@]}")

  # Print full command for transparency
  echo -e "\n📜 Executing command:\n${full_cmd[*]}\n"

  # Run gotestsum using the Docker context
  DOCKER_CONTEXT="$CONTEXT_NAME" "${full_cmd[@]}"
}

cmd_down() {
  info "🧹 Tearing down Swarm environment..."

  # Ensure context exists
  if ! docker context inspect "$CONTEXT_NAME" >/dev/null 2>&1; then
    info "Creating Docker context '$CONTEXT_NAME' for teardown..."
    docker context create "$CONTEXT_NAME" --docker "host=$MANAGER_HOST"
  fi

  # Remove the stack (ignore errors if it doesn't exist)
  run_or_warn docker --context "$CONTEXT_NAME" stack rm demo

  # Bring down compose services and volumes. --remove-orphans for the same
  # reason cmd_up passes it: a container left over from an earlier service
  # layout still references `shared`, and the volume removal fails with
  # "Resource is still in use" while the teardown reports success.
  run_or_warn $DOCKER_COMPOSE down -v --remove-orphans

  ok "Swarm environment torn down."
}

cmd_clean() {
  info "🧼 Cleaning up contexts and resources..."
  run_or_warn docker context rm -f "$CONTEXT_NAME" node1 node2
  ok "Clean up complete."
}

cmd_integration() {
  cmd_up
  cmd_deploy
  cmd_test

  if [[ "$KEEP" -eq 1 ]]; then
    warn "KEEP=1 set — leaving environment running for inspection."
  else
    cmd_down
    cmd_clean
  fi
}

# === Dispatcher ============================================================
case "${1:-}" in
  up|deploy|logs|down|clean|integration)
    cmd_"$1"
    ;;
  test)
    # Pass optional single test name to cmd_test
    cmd_test "${2:-}"
    ;;
  *)
    echo -e "${BOLD}Usage:${RESET} $0 {up|deploy|test|logs|down|clean|integration} [test_name]"
    exit 1
    ;;
esac
