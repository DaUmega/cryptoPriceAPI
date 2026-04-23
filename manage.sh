#!/usr/bin/env bash
set -euo pipefail

IMAGE="cryptoapi"
CONTAINER="cryptoapi"
PORT="${CRYPTOAPI_PORT:-8080}"
LOG_LINES="${LOG_LINES:-50}"

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[1;33m'
CYAN=$'\033[0;36m'
BOLD=$'\033[1m'
NC=$'\033[0m'

usage() {
  cat <<EOF
${BOLD}Crypto API — Docker manager${NC}

Usage: ./manage.sh <command> [options]

Commands:
  ${CYAN}build${NC}          Build the Docker image
  ${CYAN}start${NC}          Build (if needed) and start the container
  ${CYAN}stop${NC}           Stop the running container
  ${CYAN}restart${NC}        Stop and start the container
  ${CYAN}status${NC}         Show container state and exchange health
  ${CYAN}logs${NC} [-f]      Print recent logs (add -f to follow)
  ${CYAN}shell${NC}          Open a sh session inside the container
  ${CYAN}test${NC}           Run API endpoint tests
  ${CYAN}update${NC}         Rebuild the image and restart the container
  ${CYAN}purge${NC}          Stop, remove container and image
  ${CYAN}help${NC}           Show this message

Environment variables:
  CRYPTOAPI_PORT   Host port to bind (default: 8080)
  LOG_LINES   Lines shown by 'logs' without -f (default: 50)
EOF
}

# ── helpers ──────────────────────────────────────────────────────────────────

api() {
  curl -sf "http://localhost:${PORT}${1}" 2>/dev/null
}

require_running() {
  if ! docker inspect --format '{{.State.Running}}' "$CONTAINER" 2>/dev/null | grep -q true; then
    echo -e "${RED}Container '$CONTAINER' is not running.${NC}"
    exit 1
  fi
}

# ── commands ─────────────────────────────────────────────────────────────────

cmd_build() {
  echo -e "${CYAN}Building image '${IMAGE}'…${NC}"
  docker build -t "$IMAGE" .
  echo -e "${GREEN}Build complete.${NC}"
}

cmd_start() {
  if docker inspect --format '{{.State.Running}}' "$CONTAINER" 2>/dev/null | grep -q true; then
    echo -e "${YELLOW}Container '$CONTAINER' is already running.${NC}"
    return
  fi

  if ! docker image inspect "$IMAGE" &>/dev/null; then
    cmd_build
  fi

  docker rm -f "$CONTAINER" 2>/dev/null || true

  echo -e "${CYAN}Starting container '${CONTAINER}' on port ${PORT}…${NC}"
  docker run -d \
    --name "$CONTAINER" \
    --restart unless-stopped \
    -p "${PORT}:${PORT}" \
    -e ADDR=":${PORT}" \
    "$IMAGE"

  echo -e "${GREEN}Started. API: http://localhost:${PORT}${NC}"
  echo    "  Waiting for exchange connections…"
  sleep 8
  cmd_status
}

cmd_stop() {
  echo -e "${CYAN}Stopping '$CONTAINER'…${NC}"
  docker stop "$CONTAINER" 2>/dev/null && echo -e "${GREEN}Stopped.${NC}" || echo -e "${YELLOW}Not running.${NC}"
}

cmd_restart() {
  cmd_stop || true
  sleep 1
  cmd_start
}

cmd_status() {
  echo -e "${BOLD}── Container ──────────────────────────────────────${NC}"
  if docker inspect --format '{{.State.Running}}' "$CONTAINER" 2>/dev/null | grep -q true; then
    UP=$(docker inspect --format '{{.State.StartedAt}}' "$CONTAINER")
    echo -e "  State   : ${GREEN}running${NC}"
    echo    "  Started : $UP"
    echo    "  Port    : ${PORT}"
  else
    echo -e "  State   : ${RED}stopped${NC}"
    return
  fi

  echo ""
  echo -e "${BOLD}── API health ─────────────────────────────────────${NC}"
  HEALTH=$(api /health) || { echo -e "  ${RED}API unreachable${NC}"; return; }
  echo "  $HEALTH"

  echo ""
  echo -e "${BOLD}── Exchange status ────────────────────────────────${NC}"
  STATUS=$(api /status) || { echo -e "  ${RED}Could not reach /status${NC}"; return; }

  echo "$STATUS" | python3 -c "
import json, sys
d = json.load(sys.stdin)
print(f\"  Uptime  : {d['uptime']}\")
for ex in d['exchanges']:
    sym = '✓' if ex['status'] == 'connected' else '✗'
    color = '\033[0;32m' if ex['status'] == 'connected' else '\033[0;31m'
    nc = '\033[0m'
    err = f\"  ← {ex['last_error']}\" if ex.get('last_error') else ''
    print(f\"  {color}{sym}{nc} {ex['name']:<12} {ex['status']}{err}\")
" 2>/dev/null || echo "$STATUS"
}

cmd_logs() {
  if [[ "${1:-}" == "-f" ]]; then
    docker logs -f "$CONTAINER"
  else
    docker logs --tail "$LOG_LINES" "$CONTAINER"
  fi
}

cmd_shell() {
  require_running
  echo -e "${YELLOW}Note: runtime image is 'scratch'. Opening a debug container with the same binary.${NC}"
  docker run --rm -it \
    --network "container:${CONTAINER}" \
    --entrypoint /bin/sh \
    alpine \
    -c "apk add -q curl && sh"
}

_run_test() {
  local label="$1" path="$2" want_code="${3:-200}" assert="${4:-}"
  local http_code body
  http_code=$(curl -s -o /tmp/_capi_test -w "%{http_code}" --max-time 15 "http://localhost:${PORT}${path}" 2>/dev/null)
  body=$(cat /tmp/_capi_test 2>/dev/null)
  if [[ "$http_code" != "$want_code" ]]; then
    echo -e "  ${RED}✗${NC} $label  [HTTP $http_code, want $want_code]"
    _TEST_FAIL=$((_TEST_FAIL+1))
    return
  fi
  if [[ -n "$assert" ]]; then
    if ! echo "$body" | python3 -c "$assert" 2>/dev/null; then
      echo -e "  ${RED}✗${NC} $label  [assertion failed: $body]"
      _TEST_FAIL=$((_TEST_FAIL+1))
      return
    fi
  fi
  echo -e "  ${GREEN}✓${NC} $label"
  _TEST_PASS=$((_TEST_PASS+1))
}

cmd_test() {
  require_running
  echo -e "${BOLD}── API endpoint tests ──────────────────────────────────${NC}"
  _TEST_PASS=0 _TEST_FAIL=0

  _run_test "GET /health" "/health" 200 \
    'import json,sys; d=json.load(sys.stdin); assert d.get("status")=="ok", d'

  _run_test "GET /status" "/status" 200 \
    'import json,sys; d=json.load(sys.stdin); assert "exchanges" in d and "uptime" in d, d'

  # First BTC request may take a few seconds while exchanges subscribe and deliver tickers.
  _run_test "GET /price/BTC (all 6 sources)" "/price/BTC" 200 \
    'import json,sys; d=json.load(sys.stdin); srcs=[s["exchange"] for s in d.get("sources",[])]; assert set(["binance","kraken","coinbase","bybit","okx","kucoin"]).issubset(set(srcs)), f"sources={srcs}"'

  _run_test "GET /price/BADCOIN999 → 404" "/price/BADCOIN999" 404

  rm -f /tmp/_capi_test
  echo ""
  if [[ $_TEST_FAIL -eq 0 ]]; then
    echo -e "  ${BOLD}${GREEN}All ${_TEST_PASS} tests passed.${NC}"
  else
    echo -e "  ${GREEN}${_TEST_PASS} passed${NC}  ${RED}${BOLD}${_TEST_FAIL} failed${NC}"
    return 1
  fi
}

cmd_update() {
  echo -e "${CYAN}Rebuilding image…${NC}"
  cmd_build
  echo -e "${CYAN}Restarting container…${NC}"
  docker stop "$CONTAINER" 2>/dev/null || true
  docker rm "$CONTAINER" 2>/dev/null || true
  cmd_start
}

cmd_purge() {
  echo -e "${RED}This will remove the container and image. Continue? [y/N]${NC}"
  read -r ans
  if [[ "$ans" =~ ^[Yy]$ ]]; then
    docker stop "$CONTAINER" 2>/dev/null || true
    docker rm "$CONTAINER" 2>/dev/null || true
    docker rmi "$IMAGE" 2>/dev/null || true
    echo -e "${GREEN}Purged.${NC}"
  else
    echo "Aborted."
  fi
}

# ── dispatch ─────────────────────────────────────────────────────────────────

COMMAND="${1:-help}"
shift || true

case "$COMMAND" in
  build)   cmd_build ;;
  start)   cmd_start ;;
  stop)    cmd_stop ;;
  restart) cmd_restart ;;
  status)  cmd_status ;;
  logs)    cmd_logs "${1:-}" ;;
  shell)   cmd_shell ;;
  test)    cmd_test ;;
  update)  cmd_update ;;
  purge)   cmd_purge ;;
  help|--help|-h) usage ;;
  *)
    echo -e "${RED}Unknown command: $COMMAND${NC}"
    usage
    exit 1
    ;;
esac
