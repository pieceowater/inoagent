package svc

import (
	"app/internal/pkg/approval/ent"

	"path"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Shell classifies a Bash command line. It parses the line properly (pipes,
// &&, subshells, $(...), redirections) and takes the worst verdict of every
// command inside it, so `docker ps && rm -rf x` is as dangerous as its worst part.
func Shell(cmd string) ent.Decision {
	return shell(cmd, 0)
}

// Recursion guard for `host '...'` and `bash -c '...'`, which carry a whole
// command line inside one argument.
const maxDepth = 3

func shell(cmd string, depth int) ent.Decision {
	if depth > maxDepth {
		return ent.Decision{Level: ent.Ask, Reason: "слишком глубокая вложенность команд"}
	}
	if strings.TrimSpace(cmd) == "" {
		return allow
	}
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(cmd), "")
	if err != nil {
		return ent.Decision{Level: ent.Ask, Reason: "не удалось разобрать команду"}
	}
	d := allow
	syntax.Walk(f, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.CallExpr:
			d = ent.Worse(d, call(n, depth))
		case *syntax.Redirect:
			d = ent.Worse(d, redirect(n))
		case *syntax.FuncDecl, *syntax.CoprocClause:
			d = ent.Worse(d, ent.Decision{Level: ent.Ask, Reason: "объявление функции"})
		case *syntax.DeclClause:
			d = ent.Worse(d, ent.Decision{Level: ent.Ask, Reason: n.Variant.Value})
		}
		return true
	})
	return d
}

func redirect(r *syntax.Redirect) ent.Decision {
	switch r.Op {
	case syntax.RdrIn, syntax.DplIn, syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
		return allow
	}
	target := r.Word.Lit()
	switch target {
	case "/dev/null", "/dev/stdout", "/dev/stderr", "1", "2", "-":
		return allow
	}
	if strings.HasPrefix(target, "/dev/") {
		return ent.Decision{Level: ent.Danger, Reason: "запись прямо в устройство " + target}
	}
	return ent.Decision{Level: ent.Ask, Reason: "запись в файл"}
}

// Variables that only change how output looks.
var harmlessAssigns = map[string]bool{
	"PAGER": true, "SYSTEMD_PAGER": true, "LC_ALL": true, "LANG": true,
	"TERM": true, "COLUMNS": true, "NO_COLOR": true, "TZ": true,
}

func call(c *syntax.CallExpr, depth int) ent.Decision {
	d := allow
	for _, a := range c.Assigns {
		if a.Name == nil || !harmlessAssigns[a.Name.Value] {
			d = ent.Worse(d, ent.Decision{Level: ent.Ask, Reason: "установка переменной окружения"})
		}
	}
	words := make([]string, 0, len(c.Args))
	for _, w := range c.Args {
		s, ok := literal(w)
		if !ok {
			// $VAR, $(...), globs in an unknown position: the real command is
			// only known at run time, so it can't be proven harmless. $VAR is
			// also how an environment secret would leak.
			return ent.Worse(d, ent.Decision{Level: ent.Ask, Reason: "команда с подстановкой"})
		}
		words = append(words, s)
	}
	if len(words) == 0 {
		return d
	}
	return ent.Worse(d, command(words, depth))
}

// literal returns the word's value if it contains no expansions at all.
func literal(w *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, p := range w.Parts {
		switch p := p.(type) {
		case *syntax.Lit:
			// A glob stays literal: fine as an argument (ls *.log), and as a
			// command name it fails the read-only lookup anyway.
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			if p.Dollar {
				return "", false
			}
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, q := range p.Parts {
				l, ok := q.(*syntax.Lit)
				if !ok {
					return "", false
				}
				b.WriteString(l.Value)
			}
		default:
			return "", false
		}
	}
	return b.String(), true
}

func command(words []string, depth int) ent.Decision {
	name := path.Base(words[0])
	args := words[1:]

	for _, a := range args {
		if sensitive(a) {
			return ent.Decision{Level: ent.Ask, Reason: "доступ к секретам"}
		}
	}

	switch name {
	case "host":
		// Our own wrapper: ssh to the server. ssh joins its arguments with
		// spaces and hands the result to the remote shell, so that joined
		// string is exactly what will run — classify it as such.
		if len(args) == 0 {
			return ent.Decision{Level: ent.Ask, Reason: "интерактивный вход на хост"}
		}
		return shell(strings.Join(args, " "), depth+1)
	case "bash", "sh", "dash", "zsh":
		if len(args) == 2 && args[0] == "-c" {
			return shell(args[1], depth+1)
		}
		return ent.Decision{Level: ent.Ask, Reason: "запуск скрипта"}
	case "sudo":
		rest := args
		for len(rest) > 0 && rest[0] == "-n" {
			rest = rest[1:]
		}
		if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
			return ent.Decision{Level: ent.Ask, Reason: "sudo с параметрами"}
		}
		return command(rest, depth)
	case "command":
		if has(args, "-v", "-V") {
			return allow
		}
		return command(args, depth)
	case "timeout":
		if len(args) >= 2 && !strings.HasPrefix(args[0], "-") {
			return command(args[1:], depth)
		}
		return ent.Decision{Level: ent.Ask, Reason: "timeout с параметрами"}
	case "nice", "time", "nohup", "stdbuf", "ionice":
		rest := args
		for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
			rest = rest[1:]
		}
		if len(rest) == 0 {
			return allow
		}
		return command(rest, depth)
	case "xargs":
		// xargs runs its own command: judge that one.
		rest := args
		for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
			if rest[0] == "-n" || rest[0] == "-I" || rest[0] == "-P" || rest[0] == "-d" || rest[0] == "-L" {
				rest = rest[1:]
			}
			if len(rest) > 0 {
				rest = rest[1:]
			}
		}
		if len(rest) == 0 {
			return allow // xargs alone just echoes
		}
		return command(rest, depth)
	}

	if d, ok := dangerous(name, args); ok {
		return d
	}
	if readOnly(name, args) {
		return allow
	}
	return ent.Decision{Level: ent.Ask, Reason: "меняет систему: " + name}
}

func has(args []string, flags ...string) bool {
	for _, a := range args {
		for _, f := range flags {
			if a == f {
				return true
			}
			// Bundled short flags: -rf contains -r and -f.
			if len(f) == 2 && f[0] == '-' && f[1] != '-' && len(a) > 2 && a[0] == '-' && a[1] != '-' && strings.ContainsRune(a[1:], rune(f[1])) {
				return true
			}
			if strings.HasPrefix(f, "--") && strings.HasPrefix(a, f+"=") {
				return true
			}
		}
	}
	return false
}

// positional returns the arguments that are not flags.
func positional(args []string) []string {
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}
	return out
}

func first(args []string) string {
	p := positional(args)
	if len(p) == 0 {
		return ""
	}
	return p[0]
}

func oneOf(s string, set ...string) bool {
	for _, x := range set {
		if s == x {
			return true
		}
	}
	return false
}

// Units whose loss takes the server (or our way into it) down with them.
var criticalUnits = []string{"ssh", "sshd", "docker", "containerd", "tailscaled", "systemd-networkd", "NetworkManager"}

func critical(args []string) bool {
	for _, a := range positional(args) {
		for _, u := range criticalUnits {
			if strings.TrimSuffix(strings.TrimSuffix(a, ".service"), ".socket") == u {
				return true
			}
		}
	}
	return false
}

func dangerous(name string, args []string) (ent.Decision, bool) {
	d := func(reason string) (ent.Decision, bool) { return ent.Decision{Level: ent.Danger, Reason: reason}, true }
	switch name {
	case "rm", "shred", "wipefs", "fdisk", "sfdisk", "parted", "gdisk", "dd", "mkswap", "truncate",
		"lvremove", "vgremove", "pvremove", "lvreduce", "zpool", "zfs":
		return d("удаление или перезапись данных: " + name)
	case "shutdown", "reboot", "poweroff", "halt", "init", "telinit":
		return d("выключение или перезагрузка сервера")
	case "userdel", "deluser", "passwd", "chpasswd", "usermod", "groupdel", "visudo":
		return d("изменение пользователей и доступа")
	case "iptables", "ip6tables", "nft", "ufw", "firewall-cmd":
		if name == "ufw" && first(args) == "status" {
			return ent.Decision{}, false
		}
		return d("изменение файрвола: можно потерять доступ к серверу")
	case "chmod", "chown", "chgrp":
		if has(args, "-R", "--recursive") {
			return d("рекурсивная смена прав")
		}
	case "find":
		if has(args, "-delete") {
			return d("find -delete")
		}
	case "crontab":
		if has(args, "-r") {
			return d("удаление crontab")
		}
	case "apt", "apt-get", "dpkg", "snap":
		if oneOf(first(args), "remove", "purge", "autoremove") || has(args, "--purge", "-r", "-P") && name == "dpkg" {
			return d("удаление пакетов")
		}
	case "systemctl":
		sub := first(args)
		if oneOf(sub, "reboot", "poweroff", "halt", "kexec", "rescue", "emergency", "isolate", "soft-reboot") {
			return d("выключение или перезагрузка сервера")
		}
		if oneOf(sub, "stop", "disable", "mask", "kill", "restart") && critical(args) {
			return d("остановка критичного сервиса: можно потерять доступ")
		}
	case "tailscale":
		if oneOf(first(args), "down", "logout", "up", "set", "switch") {
			return d("изменение Tailscale: можно потерять доступ к серверу")
		}
	case "git":
		sub := first(args)
		if sub == "push" && has(args, "-f", "--force", "--force-with-lease", "--delete") ||
			sub == "reset" && has(args, "--hard") ||
			sub == "clean" && has(args, "-f") ||
			sub == "branch" && has(args, "-D") {
			return d("необратимая операция git")
		}
	case "docker":
		return dockerDanger(args)
	case "mkfs":
		return d("форматирование диска")
	}
	if strings.HasPrefix(name, "mkfs.") {
		return d("форматирование диска")
	}
	return ent.Decision{}, false
}

func dockerDanger(args []string) (ent.Decision, bool) {
	d := func(reason string) (ent.Decision, bool) { return ent.Decision{Level: ent.Danger, Reason: reason}, true }
	pos := positional(args)
	if len(pos) == 0 {
		return ent.Decision{}, false
	}
	sub, rest := pos[0], pos[1:]
	second := ""
	if len(rest) > 0 {
		second = rest[0]
	}
	switch sub {
	case "rm", "rmi", "kill":
		return d("docker " + sub + ": удаление контейнеров или образов")
	case "system", "image", "container", "volume", "network", "builder", "buildx":
		if oneOf(second, "prune", "rm", "remove") {
			return d("docker " + sub + " " + second + ": удаление данных docker")
		}
	case "compose":
		if second == "down" && has(args, "-v", "--volumes", "--rmi") || second == "rm" {
			return d("docker compose с удалением томов или контейнеров")
		}
	}
	return ent.Decision{}, false
}

// Commands that only look. Each entry either accepts any arguments or checks
// that the ones given don't turn it into a writer.
var plainReadOnly = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "grep": true, "egrep": true, "fgrep": true,
	"rg": true, "wc": true, "uniq": true, "cut": true, "tr": true, "column": true, "diff": true,
	"cmp": true, "stat": true, "file": true, "du": true, "df": true, "free": true, "uptime": true,
	"ps": true, "pgrep": true, "whoami": true, "id": true, "groups": true, "hostname": true,
	"uname": true, "date": true, "pwd": true, "which": true, "type": true, "echo": true,
	"printf": true, "true": true, "false": true, "test": true, "[": true, "nproc": true,
	"lscpu": true, "lsblk": true, "lsof": true, "lsusb": true, "lspci": true, "ss": true,
	"netstat": true, "ping": true, "dig": true, "nslookup": true, "getent": true,
	"basename": true, "dirname": true, "realpath": true, "readlink": true, "tree": true,
	"md5sum": true, "sha1sum": true, "sha256sum": true, "base64": true, "jq": true, "yq": true,
	"sensors": true, "iostat": true, "vmstat": true, "mpstat": true, "w": true, "who": true,
	"last": true, "zcat": true, "zgrep": true, "xxd": true, "hexdump": true, "strings": true,
	"nvidia-smi": true, "lsmod": true, "findmnt": true, "blkid": true, "seq": true, "sleep": true, "bc": true, "expr": true,
	"cd": true, "nl": true, "fold": true, "rev": true, "tac": true, "comm": true,
	"join": true, "paste": true, "numfmt": true, "lsattr": true, "ldd": true, "ip": true,
	"systemd-analyze": true, "loginctl": true, "timedatectl": true, "hostnamectl": true,
	"smartctl": true, "iotop": true, "top": true, "dmesg": true, "journalctl": true,
	"systemctl": true, "docker": true, "git": true, "tailscale": true, "sed": true, "sort": true,
	"find": true, "curl": true, "apt": true, "apt-cache": true, "dpkg": true, "snap": true,
	"ufw": true, "crontab": true,
}

func readOnly(name string, args []string) bool {
	if !plainReadOnly[name] {
		return false
	}
	sub := first(args)
	switch name {
	case "sort":
		return !has(args, "-o", "--output")
	case "sed":
		return !has(args, "-i", "--in-place")
	case "find":
		return !has(args, "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprint0", "-fprintf", "-fls", "-delete")
	case "curl":
		// Plain GETs to look at an API or a health endpoint.
		return !has(args, "-X", "--request", "-d", "--data", "--data-raw", "--data-binary",
			"--data-urlencode", "-F", "--form", "-T", "--upload-file", "-o", "--output", "-O",
			"--remote-name", "-K", "--config", "--json")
	case "ip":
		return !has(args, "add", "del", "delete", "set", "flush", "change", "replace", "append")
	case "dmesg":
		return !has(args, "-c", "-C", "--clear", "-D", "-E", "-n")
	case "top":
		return has(args, "-b")
	case "iotop":
		return has(args, "-b")
	case "smartctl":
		return !has(args, "-s", "-t", "-X", "-o", "-S")
	case "journalctl":
		for _, a := range args {
			if strings.HasPrefix(a, "--vacuum") || oneOf(a, "--rotate", "--flush", "--relinquish-var", "--sync", "--setup-keys", "--update-catalog") {
				return false
			}
		}
		return true
	case "systemctl":
		return sub == "" || oneOf(sub, "status", "show", "cat", "list-units", "list-unit-files",
			"list-timers", "list-sockets", "list-dependencies", "list-jobs", "is-active",
			"is-enabled", "is-failed", "is-system-running", "get-default")
	case "loginctl":
		return sub == "" || strings.HasPrefix(sub, "list-") || strings.HasPrefix(sub, "show-")
	case "timedatectl", "hostnamectl":
		return sub == "" || sub == "status" || sub == "show"
	case "tailscale":
		return oneOf(sub, "status", "ip", "ping", "netcheck", "version", "whois", "dns")
	case "ufw":
		return sub == "status"
	case "crontab":
		return has(args, "-l")
	case "apt", "apt-cache":
		return oneOf(sub, "list", "show", "policy", "search", "depends", "rdepends", "showpkg")
	case "snap":
		return oneOf(sub, "list", "info", "services", "changes")
	case "dpkg":
		return has(args, "-l", "-L", "-s", "-S", "--list", "--listfiles", "--status", "--search", "--get-selections")
	case "git":
		// `git -C <dir> status`: the directory is not the subcommand.
		for len(args) >= 2 && args[0] == "-C" {
			args = args[2:]
		}
		sub = first(args)
		switch sub {
		case "status", "log", "diff", "show", "rev-parse", "describe", "ls-files", "blame",
			"shortlog", "reflog", "ls-remote", "grep",
			// Not read-only, but a local commit is the agent keeping its
			// notes, and it is undone with one more commit.
			"add", "commit":
			return true
		case "branch":
			return !has(args, "-d", "-D", "-m", "-M", "-c", "-C", "--delete", "--move")
		case "remote":
			p := positional(args)
			return len(p) == 1 || len(p) == 2 && p[1] == "show"
		case "tag":
			return len(positional(args)) == 1 || has(args, "-l", "--list")
		}
		return false
	case "docker":
		return dockerReadOnly(args)
	}
	return true
}

func dockerReadOnly(args []string) bool {
	pos := positional(args)
	if len(pos) == 0 {
		return true // `docker --version`
	}
	sub := pos[0]
	second := ""
	if len(pos) > 1 {
		second = pos[1]
	}
	switch sub {
	case "ps", "logs", "inspect", "images", "version", "info", "top", "port", "diff", "history", "search":
		return true
	case "stats":
		return true
	case "container", "image", "volume", "network", "context", "plugin":
		return oneOf(second, "ls", "list", "inspect", "logs", "top", "port", "history", "stats", "diff")
	case "system":
		return oneOf(second, "df", "info")
	case "compose":
		// The real subcommand is the first positional after `compose` that
		// isn't a value for -f/-p/--project-directory.
		sub := composeSub(args)
		return oneOf(sub, "ps", "logs", "config", "ls", "images", "top", "version", "port")
	}
	return false
}

func composeSub(args []string) string {
	seenCompose := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !seenCompose {
			if a == "compose" {
				seenCompose = true
			}
			continue
		}
		if oneOf(a, "-f", "--file", "-p", "--project-name", "--project-directory", "--env-file", "--profile") {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return ""
}
