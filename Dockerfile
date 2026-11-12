# syntax=docker/dockerfile:1.7
FROM golang:1.27-alpine AS builder

# Copy go.mod and go.sum for dependency caching
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Three small binaries: the server, Claude Code's PreToolUse hook, and the
# container healthcheck.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o bin/inoagent ./cmd/server \
    && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o bin/inoagent-hook ./cmd/hook \
    && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o bin/inoagent-healthcheck ./cmd/healthcheck

# Final image: Node for the Claude Code CLI, plus the tools the agent's shell
# needs (nsenter + sudo for the `host` wrapper, git and ssh for its vault and
# its own repo, Go to build and test itself before a self-deploy).
FROM node:24-alpine

# Pinned: an unrelated rebuild must not swap the CLI for a new release (the
# hook contract and stream-json format are what the server depends on). Bump
# it here, rebuild, and check one message end to end.
ARG CLAUDE_CODE_VERSION=2.1.312

RUN apk add --no-cache bash git openssh-client ca-certificates curl jq ripgrep \
        procps coreutils findutils grep sed less tini libgcc libstdc++ make tzdata \
        util-linux-misc sudo \
    && npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION} \
    && npm cache clean --force

# Office work: people ask for presentations, Word and Excel files, PDFs and
# homework, and get finished files in the chat. LibreOffice converts and
# renders (the agent looks at a PDF/PNG of what it made before sending it, and
# reads old .doc/.xls), pandoc moves text between formats, the Python and
# Node libraries write the files; the fonts cover Cyrillic. Its own layer, so
# a bump of the CLI above doesn't re-download all of this.
RUN apk add --no-cache libreoffice-writer libreoffice-calc libreoffice-impress \
        pandoc-cli poppler-utils qpdf font-dejavu font-liberation font-noto \
        python3 py3-pip py3-openpyxl py3-pandas py3-matplotlib py3-lxml py3-pillow py3-reportlab \
    && pip install --no-cache-dir --break-system-packages \
        python-docx==1.2.0 python-pptx==1.0.2 XlsxWriter==3.3.1 pypdf==6.21.4 pdfplumber==0.11.10 \
    && npm install -g pptxgenjs@4.0.1 docx@9.15.0 \
    && npm cache clean --force

# Links to reels, TikToks and YouTube are turned into media in the chat:
# yt-dlp (with its YouTube JS helper, run by the node above) downloads,
# gallery-dl covers photo posts, ffmpeg merges and converts. Unpinned on
# purpose: these break when the sites change, and a rebuild brings the fix.
RUN apk add --no-cache ffmpeg \
    && pip install --no-cache-dir --break-system-packages "yt-dlp[default]" gallery-dl

# Voice replies: a local Piper voice (no network, no cost) run through
# onnxruntime, with espeak-ng for phonemes; `voice-say` writes an OGG/Opus
# file to the outbox and the bot sends it as a voice message.
RUN apk add --no-cache py3-onnxruntime py3-numpy espeak-ng \
    && mkdir -p /opt/voice \
    && B=https://huggingface.co/rhasspy/piper-voices/resolve/main/ru/ru_RU/ruslan/medium \
    && curl -fsSL --retry 8 --retry-all-errors --retry-delay 5 -o /opt/voice/ru.onnx $B/ru_RU-ruslan-medium.onnx \
    && curl -fsSL --retry 8 --retry-all-errors --retry-delay 5 -o /opt/voice/ru.onnx.json $B/ru_RU-ruslan-medium.onnx.json

# Cloud tooling for the projects the agent maintains (EKS, ArgoCD state repos,
# Terraform, GitHub): credentials are not in the image — they come from the
# owner's inoagent_secret_env (.env) or live in /state/cloud (see rules.md). Terraform isn't packaged by Alpine, so it's the pinned
# HashiCorp binary.
ARG TERRAFORM_VERSION=1.17.1
RUN apk add --no-cache aws-cli kubectl kustomize helm github-cli \
    && curl -fsSL -o /tmp/tf.zip https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_amd64.zip \
    && unzip -q /tmp/tf.zip terraform -d /usr/local/bin && rm /tmp/tf.zip

# Browser checks of live sites (screenshots, axe): system Chromium driven by
# playwright-core, which downloads no browser of its own.
RUN apk add --no-cache chromium \
    && npm install -g playwright-core@1.56.1 @axe-core/playwright@4.10.2 \
    && npm cache clean --force

COPY --from=builder /usr/local/go /usr/local/go
COPY --from=builder /app/bin/ /usr/local/bin/
COPY deploy/host deploy/self-deploy deploy/send-file deploy/react deploy/remind deploy/pin deploy/poll deploy/new-topic deploy/fetch-media deploy/ctx deploy/voice-say /usr/local/bin/
# Managed settings can't be overridden from the workspace or ~/.claude: this
# is where the approval hook is wired in for good.
COPY deploy/managed-settings.json /etc/claude-code/managed-settings.json

RUN mkdir -p /workspace /state /repo /home/node/.claude \
    && chown node:node /workspace /state /repo /home/node/.claude \
    && echo 'node ALL=(root) NOPASSWD: /usr/bin/nsenter' > /etc/sudoers.d/inoagent \
    && chmod 0440 /etc/sudoers.d/inoagent

# The node image's own user (uid 1000); the bind-mounted volumes are owned by
# the same uid on the host.
USER node
ENV HOME=/home/node \
    CLAUDE_CONFIG_DIR=/home/node/.claude \
    SHELL=/bin/bash \
    PATH=/usr/local/go/bin:/home/node/go/bin:$PATH \
    GOTOOLCHAIN=local \
    PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 \
    CHROMIUM_PATH=/usr/bin/chromium-browser \
    AWS_CONFIG_FILE=/state/cloud/aws/config \
    AWS_SHARED_CREDENTIALS_FILE=/state/cloud/aws/credentials \
    KUBECONFIG=/state/cloud/kube/config \
    GH_CONFIG_DIR=/state/cloud/gh \
    NODE_PATH=/usr/local/lib/node_modules \
    USE_BUILTIN_RIPGREP=0 \
    INOAGENT_WORKSPACE=/workspace \
    INOAGENT_STATE_DIR=/state \
    INOAGENT_HOOK_SOCK=/tmp/inoagent-hook.sock
WORKDIR /workspace

HEALTHCHECK --interval=1m --timeout=10s --start-period=1m --retries=3 CMD ["inoagent-healthcheck"]
ENTRYPOINT ["/sbin/tini", "-s", "--", "inoagent"]
