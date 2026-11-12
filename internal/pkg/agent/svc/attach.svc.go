package svc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"app/internal/core/telegram"
)

// Attachments are downloaded to the state volume, not the vault: photos and
// files people send don't belong on GitHub. Claude opens them from there —
// its Read tool sees images and PDFs natively. Voice is transcribed by the
// local Whisper service first, so the agent gets words, not audio.
const (
	uploadsKeep = 7 * 24 * time.Hour
	draftsKeep  = 30 * 24 * time.Hour
)

type attachment struct {
	kind       string // as the agent is told: "фото", "голосовое", …
	file       *telegram.File
	name       string
	transcribe bool
}

func attachmentsOf(m *telegram.Message) []attachment {
	var as []attachment
	if n := len(m.Photo); n > 0 {
		as = append(as, attachment{kind: "фото", file: &m.Photo[n-1], name: "photo.jpg"})
	}
	if f := m.Document; f != nil {
		as = append(as, attachment{kind: "файл", file: f, name: orDefault(f.FileName, "file")})
	}
	if f := m.Voice; f != nil {
		as = append(as, attachment{kind: "голосовое", file: f, name: "voice.ogg", transcribe: true})
	}
	if f := m.VideoNote; f != nil {
		as = append(as, attachment{kind: "видео-кружок", file: f, name: "video_note.mp4", transcribe: true})
	}
	if f := m.Audio; f != nil {
		as = append(as, attachment{kind: "аудиофайл", file: f, name: orDefault(f.FileName, "audio")})
	}
	if f := m.Video; f != nil {
		as = append(as, attachment{kind: "видео", file: f, name: orDefault(f.FileName, "video.mp4")})
	}
	return as
}

// describeAttachments downloads what the message (and the one it replies to)
// carries and returns the lines that tell the agent about it.
func (s *AgentService) describeAttachments(ctx context.Context, msgs ...*telegram.Message) string {
	var lines []string
	for i, m := range msgs {
		sources := []*telegram.Message{m}
		if i == 0 {
			sources = []*telegram.Message{m.ReplyToMessage, m}
		}
		for _, src := range sources {
			if src == nil {
				continue
			}
			for _, a := range attachmentsOf(src) {
				lines = append(lines, s.describe(ctx, src, a))
			}
		}
	}
	return strings.Join(lines, "\n")
}

func (s *AgentService) describe(ctx context.Context, src *telegram.Message, a attachment) string {
	if a.file.FileSize > telegram.MaxDownload {
		return fmt.Sprintf("(%s «%s», %d МБ: больше 20 МБ, бот такие скачать не может)", a.kind, a.name, a.file.FileSize>>20)
	}
	dir := filepath.Join(s.cfg.StateDir, "uploads", time.Now().Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Sprintf("(%s: не смог сохранить: %v)", a.kind, err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%d-%d-%s", src.Chat.ID*-1, src.MessageID, safeName(a.name)))
	if err := s.msg.Client().Download(ctx, a.file.FileID, path); err != nil {
		s.log.Warn("attachment download failed", "kind", a.kind, "err", err)
		return fmt.Sprintf("(%s: не смог скачать)", a.kind)
	}
	line := fmt.Sprintf("(%s: %s", a.kind, path)
	if a.file.MimeType != "" {
		line += ", " + a.file.MimeType
	}
	switch {
	case a.transcribe:
		text, err := s.asr.Transcribe(ctx, path)
		if err != nil {
			s.log.Warn("transcription failed", "err", err)
			return line + "; расшифровать не вышло: " + err.Error() + ")"
		}
		return line + ")\nРасшифровка: «" + text + "»"
	case a.kind == "фото":
		return line + " — посмотри через Read)"
	}
	return line + ")"
}

// PruneUploads drops downloaded attachments older than a week, and the
// agent's working drafts (sources of files it sent, kept for follow-up edits)
// untouched for a month.
func (s *AgentService) PruneUploads() {
	root := filepath.Join(s.cfg.StateDir, "uploads")
	days, _ := os.ReadDir(root)
	cutoff := time.Now().Add(-uploadsKeep).Format("2006-01-02")
	for _, d := range days {
		if d.IsDir() && d.Name() < cutoff {
			_ = os.RemoveAll(filepath.Join(root, d.Name()))
		}
	}
	drafts := filepath.Join(s.cfg.StateDir, "drafts")
	entries, _ := os.ReadDir(drafts)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > draftsKeep {
			_ = os.RemoveAll(filepath.Join(drafts, e.Name()))
		}
	}
}

var unsafeChars = regexp.MustCompile(`[^\p{L}\p{N}._-]+`)

func safeName(name string) string {
	name = unsafeChars.ReplaceAllString(filepath.Base(name), "_")
	if r := []rune(name); len(r) > 80 {
		name = string(r[len(r)-80:])
	}
	return name
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
