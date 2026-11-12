#!/bin/sh
# make quick-deploy — ship this checkout's code to the server and rebuild
# only the bot's container: a minute instead of a full `make deploy`. It does
# not touch .env, keys, the agent's clone or the Whisper container, so use
# `make deploy` after changing anything under ansible/.
set -eu
cd "$(dirname "$0")/.."
HOST=${HOST:-$(scripts/host.sh)}
DIR=${INOAGENT_DIR:-/opt/inoagent}
[ -n "$HOST" ] || { echo "no server: run make init first" >&2; exit 1; }

COPYFILE_DISABLE=1 tar --no-xattrs -czf - cmd internal deploy go.mod go.sum Dockerfile .dockerignore docker-compose.yml 2>/dev/null |
  ssh "$HOST" "cd $DIR && sudo rm -rf cmd internal deploy && sudo tar xzf - 2>/dev/null && sudo docker compose up -d --build --no-deps inoagent"
ssh "$HOST" "docker ps --filter name=inoagent --format '{{.Names}}\t{{.Status}}'"
