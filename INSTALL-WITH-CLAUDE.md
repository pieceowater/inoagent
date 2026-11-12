# Установка через Claude Code

Этот файл — инструкция для **Claude Code на компьютере владельца**. Человек скачал репу и сказал
«установи на мой сервер». Всё ставится с локального компьютера по SSH, на сервере ничего не нужно
делать руками. Подробности про сам проект — в [README.md](README.md).

## Что спросить у человека

1. **Сервер:** IP или имя, пользователь SSH. Ubuntu или Debian, вход по ключу, `sudo` без пароля
   (Docker и docker compose ставятся сами, Docker Desktop не нужен). От 2 vCPU, 4 ГБ RAM, 40 ГБ диска.
   Если ключа нет: `ssh-copy-id user@host`; если sudo с паролем — добавить
   `echo 'user ALL=(ALL) NOPASSWD:ALL' | sudo tee /etc/sudoers.d/user` на сервере.
2. **GitHub:** репа должна быть **его собственным fork** (у него права admin), `gh auth login` выполнен.
   Если нет: `gh repo fork <эта репа> --clone`, дальше работать в своём клоне. Или дать токен GitHub
   (scope `repo`) — `export GH_TOKEN=…`. Токен не писать в файлы и не коммитить.
3. **Telegram-бот:** создать у @BotFather (`/newbot`), прислать токен.
4. **Подписка Claude Pro или Max:** токен получить командой `npx @anthropic-ai/claude-code setup-token`
   (откроется браузер, в конце напечатается `sk-ant-oat01-…`). Это делает сам человек, ты токен не видишь,
   пока он его не вставит. Сервер должен быть в стране, где Claude доступен (не Россия, не Китай).

## Что сделать

На компьютере нужны `ansible`, `git`, `curl`, `ssh`, Node.js, `gh` (`brew install ansible gh node`).

Проще всего — штатный установщик, он всё делает и пишет gitignored-конфиг:

```bash
make init
```

Он интерактивный: подставляй ответы в порядке вопросов (адрес сервера, пользователь, токен бота,
токен Claude). Если в твоей оболочке нельзя отвечать на вопросы, повтори его шаги руками:
`ansible/inventory/hosts.ini` (по образцу `hosts.ini.example`), `ansible/host_vars/<сервер>/vars.yml`
(копия `host_vars/example/vars.yml`, поставь часовой пояс) и `secrets.yml` (по образцу example),
затем `cd ansible && ansible-galaxy collection install -r requirements.yml && ansible-playbook site.yml`
Первый раз идёт 5–10 минут.

Последний шаг делает человек: создать группу в Telegram, включить «Темы», добавить бота админом
и написать там любое сообщение. После этого `make init` сам впишет id группы и владельца
и включит бота (вручную: впиши `inoagent_telegram_chat_id` и `inoagent_telegram_approver_ids`
в `vars.yml` и снова `make deploy`).

## Проверка

- `make status` и `make logs` — контейнеры `inoagent` и `inoagent-whisper` работают, ошибок нет.
- Человек пишет боту в группе — он отвечает. `/menu` показывает кнопки.
- Заметки агента (Obsidian) лежат в приватной репе `<fork>.vault`: `git clone git@github.com:<fork>.vault.git`.

## Правила

- Секреты (`secrets.yml`, `.env`, токены, ключи в `secrets/ssh`) не коммитятся и не показываются в чате.
- Менять код бота можно в своём fork; `make deploy` выкатывает правки.
- Не запускай `docker compose down -v` и не удаляй `/opt/inoagent/data` на сервере: там память бота.
