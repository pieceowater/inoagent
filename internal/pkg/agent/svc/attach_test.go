package svc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"app/internal/core/telegram"
)

func TestAttachmentsOf(t *testing.T) {
	m := &telegram.Message{
		Photo: []telegram.File{{FileID: "small"}, {FileID: "big"}},
		Voice: &telegram.File{FileID: "v"},
	}
	as := attachmentsOf(m)
	if len(as) != 2 || as[0].file.FileID != "big" || !as[1].transcribe {
		t.Fatalf("got %+v", as)
	}
	if len(attachmentsOf(&telegram.Message{Text: "hi"})) != 0 {
		t.Fatal("plain text has no attachments")
	}
}

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"отчёт за май.pdf": "отчёт_за_май.pdf",
		"../../etc/passwd": "passwd",
		"a b/c;rm -rf.txt": "c_rm_-rf.txt",
	}
	for in, want := range cases {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPrompt(t *testing.T) {
	m := &telegram.Message{From: &telegram.User{ID: 1, FirstName: "Юра"}}
	got := Prompt(m, "", "(фото: /state/uploads/x.jpg — посмотри через Read)")
	want := "[Сообщение от Юра, user_id 1]\n(фото: /state/uploads/x.jpg — посмотри через Read)"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("INOAGENT_REPO", "acme/bot")
	t.Setenv("INOAGENT_VAULT_REPO", "acme/bot.vault")
	p := SystemPrompt(dir)
	if strings.Contains(p, "{{") || !strings.Contains(p, "github.com/acme/bot") || !strings.Contains(p, "`acme/bot.vault`") {
		t.Fatal("placeholders not filled in")
	}
	if !strings.Contains(p, defaultPersona) {
		t.Fatal("default persona missing")
	}
	if err := os.WriteFile(filepath.Join(dir, "persona.md"), []byte("говори как пират"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := SystemPrompt(dir); !strings.HasSuffix(p, "говори как пират") || strings.Contains(p, defaultPersona) {
		t.Fatal("the vault's persona must replace the default")
	}
}
