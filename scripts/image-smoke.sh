#!/usr/bin/env bash
# Copyright 2026 3eAI Labs Ltd
# SPDX-License-Identifier: AGPL-3.0-only
#
# image-smoke.sh — start a built image next to a NATS server and check that it comes up.
#
#   1. Docker reports the container healthy (the image's HEALTHCHECK on :8080/metrics)
#   2. the admin UI is served on :8081
#   3. the process does not run as root
#   4. the gateway's licence texts are in the image
#
# The gateway shares the NATS container's network namespace, so the nats.url in
# configs/config.yaml (127.0.0.1:4222) reaches it without a config override.
#
# Usage:  scripts/image-smoke.sh [image]   (default diameter-ha-gateway:dev, as `make image` tags it)

set -euo pipefail

IMAGE="${1:-diameter-ha-gateway:dev}"
NATS_IMAGE="nats:2.10-alpine"
HEALTH_TIMEOUT=120   # seconds; the first health check runs one interval (30s) after start

NATS="dhg-smoke-nats-$$"
GW="dhg-smoke-gw-$$"

cleanup() { docker rm -f "$GW" "$NATS" >/dev/null 2>&1 || true; }
trap cleanup EXIT

fail() {
  echo "image-smoke: FAIL: $*" >&2
  echo "--- gateway logs (last 40 lines) ---" >&2
  docker logs --tail 40 "$GW" >&2 2>&1 || true
  exit 1
}

docker run -d --name "$NATS" "$NATS_IMAGE" >/dev/null
docker run -d --name "$GW" --network "container:$NATS" "$IMAGE" >/dev/null

status=starting
for ((i = 0; i < HEALTH_TIMEOUT; i += 2)); do
  status="$(docker inspect "$GW" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}')"
  running="$(docker inspect "$GW" --format '{{.State.Running}}')"
  [[ "$running" == true ]] || fail "container exited"
  [[ "$status" == starting ]] || break
  sleep 2
done
[[ "$status" == healthy ]] || fail "health is '$status' after ${HEALTH_TIMEOUT}s"

docker exec "$GW" wget -qO- http://localhost:8081/ | grep -q '<title>Diameter Gateway Admin</title>' \
  || fail "admin UI not served on :8081"

uid="$(docker exec "$GW" id -u)"
[[ "$uid" != 0 ]] || fail "process runs as root"

docker exec "$GW" test -s /usr/share/licenses/diameter-ha-gateway/LICENSE \
  || fail "LICENSE missing from /usr/share/licenses/diameter-ha-gateway"

echo "image-smoke: OK ($IMAGE: healthy, admin UI served, uid $uid, licence texts present)"
