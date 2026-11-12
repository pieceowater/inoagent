#!/usr/bin/env bash
# make init — install your own Inoagent on your own server, from scratch.
#
# Asks for the server, the bot token and the Claude token, writes the
# gitignored config (ansible/inventory/hosts.ini, ansible/host_vars/<server>/),
# deploys, then waits for you to add the bot to your Telegram group and fills
# in the group and owner ids by itself. Safe to run again: it offers to keep
# what is already configured.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
ANSIBLE=$ROOT/ansible
INVENTORY=$ANSIBLE/inventory/hosts.ini

say()  { printf '\n\033[1m%s\033[0m\n' "$*"; }
ask()  { local q=$1 def=${2:-} a; read -r -p "$q${def:+ [$def]}: " a; echo "${a:-$def}"; }
askS() { local q=$1 a; read -r -s -p "$q: " a; echo >&2; echo "$a"; }
die()  { printf '\n\033[31m%s\033[0m\n' "$*" >&2; exit 1; }

# --- what this machine needs ------------------------------------------------
say "1/6 Проверяю, что есть на этом компьютере"
for bin in ansible-playbook ansible-galaxy ssh git curl; do
  command -v "$bin" >/dev/null || die "Нужен $bin (ansible: brew install ansible / pipx install ansible)."
done
ansible-galaxy collection install -r "$ANSIBLE/requirements.yml" >/dev/null

origin=$(git -C "$ROOT" remote get-url origin 2>/dev/null || true)
repo=$(printf '%s' "$origin" | sed -E 's#^(git@github\.com:|https://github\.com/)##; s#\.git$##')
[ -n "$repo" ] || die "У этого чекаута нет origin на GitHub. Сделай fork репы и склонируй свой fork."
echo "Твоя репа: $repo (бот будет пушить сюда свои изменения), заметки: $repo.vault"

register_keys=true
if ! command -v gh >/dev/null || ! gh auth status >/dev/null 2>&1; then
  echo "gh не установлен или не залогинен: deploy keys и репу для заметок придётся добавить руками."
  echo "Проще: brew install gh && gh auth login — и запусти make init ещё раз."
  [ "$(ask 'Продолжить без gh? (y/n)' n)" = y ] || exit 1
  register_keys=false
elif ! gh repo view "$repo" --json viewerPermission -q .viewerPermission | grep -q ADMIN; then
  die "У тебя нет прав админа на $repo — бот не сможет пушить туда. Нужен твой собственный fork."
fi

# --- the server ---------------------------------------------------------------
say "2/6 Сервер (Ubuntu/Debian, вход по SSH-ключу, sudo без пароля)"
host=""; user=""
if [ -f "$INVENTORY" ]; then
  current=$("$ROOT/scripts/host.sh")
  [ -n "$current" ] && [ "$(ask "Уже настроен $current. Оставить? (y/n)" y)" = y ] && {
    host=${current#*@}; user=${current%@*}; }
fi
if [ -z "$host" ]; then
  host=$(ask "Адрес сервера (IP или имя)")
  user=$(ask "Пользователь SSH" "$(whoami)")
fi
[ -n "$host" ] || die "Нужен адрес сервера."
ssh -o BatchMode=yes -o ConnectTimeout=10 "$user@$host" true \
  || die "Не могу зайти по SSH как $user@$host без пароля. Добавь ключ: ssh-copy-id $user@$host"
ssh -o BatchMode=yes "$user@$host" sudo -n true \
  || die "У $user на сервере нет sudo без пароля. Добавь: echo '$user ALL=(ALL) NOPASSWD:ALL' | sudo tee /etc/sudoers.d/$user"
tz=$(ssh -o BatchMode=yes "$user@$host" 'timedatectl show -p Timezone --value 2>/dev/null || cat /etc/timezone 2>/dev/null' || true)

mkdir -p "$(dirname "$INVENTORY")"
printf '[inoagent]\n%s ansible_user=%s\n' "$host" "$user" > "$INVENTORY"
HV=$ANSIBLE/host_vars/$host
mkdir -p "$HV"

# --- tokens -------------------------------------------------------------------
say "3/6 Токены"
secrets=$HV/secrets.yml
bot_token=""; claude_token=""
if [ -f "$secrets" ]; then
  bot_token=$(sed -n 's/^inoagent_telegram_bot_token: *"\(.*\)"/\1/p' "$secrets")
  claude_token=$(sed -n 's/^inoagent_claude_oauth_token: *"\(.*\)"/\1/p' "$secrets")
fi
while :; do
  if [ -z "$bot_token" ]; then
    echo "Создай бота у @BotFather (/newbot) и вставь его токен."
    bot_token=$(askS "Токен бота")
  fi
  me=$(curl -fsS "https://api.telegram.org/bot$bot_token/getMe" 2>/dev/null || true)
  if printf '%s' "$me" | grep -q '"ok":true'; then
    bot_name=$(printf '%s' "$me" | sed -n 's/.*"username":"\([^"]*\)".*/\1/p')
    echo "Бот: @$bot_name"
    break
  fi
  echo "Telegram не принял этот токен, ещё раз."; bot_token=""
done
if [ -z "$claude_token" ]; then
  echo "Нужен токен подписки Claude (Pro или Max). Сейчас откроется браузер для входа;"
  echo "в конце в терминале появится токен sk-ant-oat01-… — скопируй его."
  if command -v npx >/dev/null; then
    (cd "$ROOT" && make -s setup-token) || true
  else
    echo "Нет npx (Node.js). Поставь Node или получи токен на другой машине: npx @anthropic-ai/claude-code setup-token"
  fi
  claude_token=$(askS "Токен Claude (sk-ant-oat01-…)")
fi
umask 077
cat > "$secrets" <<EOF
---
# Written by make init. Never commit this file.
inoagent_telegram_bot_token: "$bot_token"
inoagent_claude_oauth_token: "$claude_token"
EOF

vars=$HV/vars.yml
if [ ! -f "$vars" ]; then
  sed "s#^inoagent_timezone: .*#inoagent_timezone: ${tz:-UTC}#" "$ANSIBLE/host_vars/example/vars.yml" > "$vars"
fi
if [ "$register_keys" = false ]; then
  grep -q '^inoagent_github_register_key:' "$vars" || echo "inoagent_github_register_key: false" >> "$vars"
fi

deploy() { (cd "$ANSIBLE" && ansible-playbook site.yml); }

# --- first deploy, in setup mode ---------------------------------------------
chat_id=$(sed -n 's/^inoagent_telegram_chat_id: *\(-\{0,1\}[0-9]*\).*/\1/p' "$vars")
if [ "${chat_id:-0}" = 0 ]; then
  say "4/6 Ставлю на сервер (первый раз — минут 5–10: Docker, образы, модель для голосовых)"
  deploy

  say "5/6 Группа в Telegram"
  cat <<EOF
  1. Создай группу, в её настройках включи «Темы» (Topics).
  2. Добавь туда @$bot_name и сделай его админом — так он видит все сообщения.
  3. Напиши в группе что угодно. Жду…
EOF
  since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  line=""
  for _ in $(seq 1 120); do
    line=$(ssh -o BatchMode=yes "$user@$host" \
      "docker logs --since $since inoagent 2>&1 | grep 'setup mode: message' | grep '\"chat_type\":\"supergroup\"' | tail -1" || true)
    [ -n "$line" ] && break
    sleep 5
  done
  [ -n "$line" ] || die "За 10 минут не увидел сообщения из группы. Проверь шаги выше и запусти make init ещё раз."
  chat_id=$(printf '%s' "$line" | sed -n 's/.*"chat_id":\(-[0-9]*\).*/\1/p')
  owner_id=$(printf '%s' "$line" | sed -n 's/.*"user_id":\([0-9]*\).*/\1/p')
  echo "Группа: $chat_id, ты: $owner_id"
  sed -i.bak -e "s/^inoagent_telegram_chat_id: .*/inoagent_telegram_chat_id: $chat_id/" \
             -e "s/^inoagent_telegram_approver_ids: .*/inoagent_telegram_approver_ids: [$owner_id]/" "$vars"
  rm -f "$vars.bak"
fi

say "6/6 Включаю бота в группе"
deploy

cat <<EOF

Готово. Напиши @$bot_name в группе — он ответит.
  /menu      кнопки рядом со скрепкой
  make logs  логи бота
Заметки агента (Obsidian vault): git clone git@github.com:$repo.vault.git
Свой стиль общения бота — файл persona.md в корне vault (бот подхватит сам).
EOF
