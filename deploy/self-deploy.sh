#!/usr/bin/env bash
# Rebuild and restart Inoagent from its own clone (/opt/inoagent/repo), as
# root on the host — started by the `self-deploy` helper through systemd-run,
# so it outlives the container it replaces.
#
# It copies the same files the Ansible role copies, builds the image and
# recreates the container. A failed build leaves the running container
# untouched. Either way the outcome lands in data/state/deploy-result.json,
# which the running bot reports to the topic that asked.
set -uo pipefail

DIR=${INOAGENT_DIR:-/opt/inoagent}
REPO=$DIR/repo
STATE=$DIR/data/state
LOG=$STATE/self-deploy.log

# One deploy at a time: a second one waits for the first to finish, otherwise
# two builds race over the same container and both report failure.
exec 9>"$DIR/.self-deploy.lock"
flock 9

commit=$(git -C "$REPO" -c safe.directory='*' log -1 --format='%h %s' 2>/dev/null || echo "?")

(
  set -e
  cd "$DIR"
  rm -rf cmd internal deploy
  cp -a "$REPO"/cmd "$REPO"/internal "$REPO"/deploy "$REPO"/go.mod "$REPO"/go.sum \
        "$REPO"/Dockerfile "$REPO"/.dockerignore "$REPO"/docker-compose.yml "$DIR"/
  docker compose build
  docker compose up -d
  # "up -d" returns as soon as the container is created. The new bot is only
  # alive once its healthcheck passes (it has reached Telegram); until then
  # the old one may still be running, and reporting success now would be a lie.
  echo "waiting for the new container to become healthy"
  health=""
  for _ in $(seq 1 36); do
    sleep 5
    health=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' inoagent 2>/dev/null || echo missing)
    [ "$health" = healthy ] && break
  done
  if [ "$health" != healthy ]; then
    echo "новый контейнер не стал healthy за 3 минуты (статус: $health). Лог:"
    docker logs --tail 30 inoagent 2>&1 | cut -c1-300
    exit 1
  fi
) >"$LOG" 2>&1
status=$?

python3 - "$STATE/deploy-result.json" "$status" "$commit" "$LOG" <<'PY'
import json, sys
path, status, commit, log = sys.argv[1:]
with open(log, errors="replace") as f:
    tail = "".join(f.readlines()[-30:])
with open(path, "w") as f:
    json.dump({"ok": status == "0", "commit": commit, "log": tail}, f)
PY
chown 1000:1000 "$STATE/deploy-result.json" "$LOG"
