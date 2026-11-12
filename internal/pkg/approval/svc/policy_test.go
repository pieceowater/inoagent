package svc

import (
	"app/internal/pkg/approval/ent"

	"encoding/json"
	"testing"
)

func TestShell(t *testing.T) {
	cases := []struct {
		cmd  string
		want ent.Level
	}{
		// Looking around: runs straight away.
		{"docker ps", ent.Allow},
		{"host 'docker ps --format \"{{.Names}}\"'", ent.Allow},
		{"host 'df -h / && free -h'", ent.Allow},
		{"host 'sudo journalctl -u docker --since -1h | tail -n 50'", ent.Allow},
		{"host 'docker logs --tail 100 jellyfin 2>&1 | grep -i error'", ent.Allow},
		{"host 'docker compose -f /srv/media/docker-compose.yml ps'", ent.Allow},
		{"host 'systemctl status tailscaled'", ent.Allow},
		{"git -C /workspace log --oneline -5", ent.Allow},
		{"cat /workspace/notes/server.md", ent.Allow},
		{"find / -name '*.log' -size +100M 2>/dev/null", ent.Allow},
		{"curl -s http://127.0.0.1:8096/health", ent.Allow},
		{"ls *.md", ent.Allow},
		{"command -v docker", ent.Allow},

		// Changes something: needs a button.
		{"host 'docker restart jellyfin'", ent.Ask},
		{"host 'cd ~/media && docker compose up -d'", ent.Ask},
		{"echo hi > /tmp/x", ent.Ask},
		{"host 'sudo apt-get install -y htop'", ent.Ask},
		{"sed -i s/a/b/ f", ent.Ask},
		{"curl -X POST http://x", ent.Ask},
		{"find . -exec cat {} \\;", ent.Ask},
		{"echo $CLAUDE_CODE_OAUTH_TOKEN", ent.Ask},
		{"printenv", ent.Ask},
		{"cat /secrets/ssh/id_ed25519", ent.Ask},
		{"host 'cat ~/media/.env'", ent.Ask},
		{"ls $(echo /)", ent.Ask},
		{"LD_PRELOAD=x ls", ent.Ask},
		{"python3 -c 'print(1)'", ent.Ask},
		{"command rm x", ent.Danger},
		{"some-unknown-tool --flag", ent.Ask},
		{"systemctl restart jellyfin", ent.Ask},

		// Destroys or can lock us out: red button.
		{"rm -rf /tmp/x", ent.Danger},
		{"host 'sudo rm -rf /var/lib/docker'", ent.Danger},
		{"docker ps && rm x", ent.Danger},
		{"host 'docker system prune -af'", ent.Danger},
		{"host 'cd ~/media && docker compose down -v'", ent.Danger},
		{"host 'sudo reboot'", ent.Danger},
		{"host 'sudo ufw enable'", ent.Danger},
		{"host 'sudo systemctl stop ssh'", ent.Danger},
		{"host 'tailscale down'", ent.Danger},
		{"bash -c 'rm -rf /'", ent.Danger},
		{"find / -name x -delete", ent.Danger},
		{"echo x > /dev/sda", ent.Danger},
		{"ls | xargs rm", ent.Danger},
		{"git push --force", ent.Danger},
		{"host 'sudo -n mkfs.ext4 /dev/sdb1'", ent.Danger},
	}
	for _, c := range cases {
		if got := Shell(c.cmd); got.Level != c.want {
			t.Errorf("Shell(%q) = %v (%s), want %v", c.cmd, got.Level, got.Reason, c.want)
		}
	}
}

func TestClassifyFiles(t *testing.T) {
	p := Policy{Workspace: "/workspace"}
	in := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	cases := []struct {
		tool  string
		input json.RawMessage
		want  ent.Level
	}{
		{"Read", in(map[string]string{"file_path": "/etc/hosts"}), ent.Allow},
		{"Read", in(map[string]string{"file_path": "/secrets/ssh/id_ed25519"}), ent.Ask},
		{"Read", in(map[string]string{"file_path": "/proc/1/environ"}), ent.Ask},
		{"Write", in(map[string]string{"file_path": "/workspace/notes/a.md"}), ent.Allow},
		{"Edit", in(map[string]string{"file_path": "/workspace/CLAUDE.md"}), ent.Ask},
		{"Write", in(map[string]string{"file_path": "/workspace/../etc/passwd"}), ent.Ask},
		{"Write", in(map[string]string{"file_path": "/workspace2/x"}), ent.Ask},
		{"Write", in(map[string]string{"file_path": "/etc/x"}), ent.Ask},
		{"WebSearch", in(map[string]string{"query": "x"}), ent.Allow},
		{"mcp__something__do", in(map[string]string{}), ent.Ask},
	}
	for _, c := range cases {
		if got := p.Classify(c.tool, c.input); got.Level != c.want {
			t.Errorf("Classify(%s, %s) = %v (%s), want %v", c.tool, c.input, got.Level, got.Reason, c.want)
		}
	}
}
