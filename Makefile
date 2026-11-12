# Application name and build directory
APP_NAME = inoagent
BUILD_DIR = bin
MAIN_FILE = cmd/server/main.go

# The server, for the remote targets: user@host of the first entry under
# [inoagent] in ansible/inventory/hosts.ini (written by `make init`).
HOST ?= $(shell scripts/host.sh)
ANSIBLE_ARGS ?=

.PHONY: all init quick-deploy build run test vet clean deploy deps setup-token logs status shell restart

all: build

# Install your own bot on your own server, start to finish (interactive).
init:
	@scripts/init.sh

# Build the three binaries
build:
	@mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(APP_NAME) $(MAIN_FILE)
	go build -o $(BUILD_DIR)/$(APP_NAME)-hook ./cmd/hook
	go build -o $(BUILD_DIR)/$(APP_NAME)-healthcheck ./cmd/healthcheck

# Run the server locally (reads .env from the repo root)
run: build
	./$(BUILD_DIR)/$(APP_NAME)

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf $(BUILD_DIR)

# Ansible collections the playbook needs
deps:
	ansible-galaxy collection install -r ansible/requirements.yml

# Build and (re)start the stack on the server
deploy:
	cd ansible && ansible-playbook site.yml $(ANSIBLE_ARGS)

# Code-only redeploy of the bot's container, skipping Ansible (see the script)
quick-deploy:
	@HOST=$(HOST) scripts/quick-deploy.sh

# Log in to Claude with the subscription and print a long-lived token; put it
# into ansible/host_vars/<host>/secrets.yml as inoagent_claude_oauth_token.
# Runs here, not on the server: the browser opens by itself, and the token is
# not tied to the machine it was made on.
setup-token:
	npx -y @anthropic-ai/claude-code@$$(sed -n 's/^ARG CLAUDE_CODE_VERSION=//p' Dockerfile) setup-token

logs:
	ssh $(HOST) docker logs -f --tail 200 inoagent

status:
	ssh $(HOST) "docker ps --filter name=inoagent --format '{{.Names}}\t{{.Status}}'"

shell:
	ssh -t $(HOST) docker exec -it inoagent bash

restart:
	ssh $(HOST) docker restart inoagent
