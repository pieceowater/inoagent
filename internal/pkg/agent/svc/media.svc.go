package svc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"app/internal/core/telegram"
)

// A message that is nothing but a link to Instagram, TikTok or YouTube gets
// the media itself in reply, without any commentary: yt-dlp downloads it
// (gallery-dl for photo posts), the bot uploads it. No Claude run is spent.

var mediaHosts = []string{"instagram.com", "tiktok.com", "youtube.com", "youtu.be"}

const (
	mediaTimeout  = 4 * time.Minute
	maxMediaFiles = 10
	cookiesFile   = "cookies.txt" // optional, in the state dir, Netscape format
)

var urlOnly = regexp.MustCompile(`^https?://\S+$`)

// MediaURL returns the link when the whole message is one link to a site we can fetch from.
func MediaURL(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if !urlOnly.MatchString(text) {
		return "", false
	}
	u, err := url.Parse(text)
	if err != nil {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range mediaHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return text, true
		}
	}
	return "", false
}

// FetchMedia downloads the link and sends the result to m's topic. It blocks,
// so callers run it in a goroutine.
func (s *AgentService) FetchMedia(m *telegram.Message, link string) {
	ctx, cancel := context.WithTimeout(s.ctx, mediaTimeout)
	defer cancel()
	select {
	case s.mediaSem <- struct{}{}:
		defer func() { <-s.mediaSem }()
	case <-ctx.Done():
		return
	}
	typing, stopTyping := context.WithCancel(ctx)
	defer stopTyping()
	go func() {
		for {
			_ = s.msg.Client().SendChatAction(typing, m.Chat.ID, m.Thread(), "upload_video")
			select {
			case <-typing.Done():
				return
			case <-time.After(4500 * time.Millisecond):
			}
		}
	}()

	dir, err := os.MkdirTemp(filepath.Join(s.cfg.StateDir), "media-")
	if err != nil {
		s.msg.Reply(ctx, m, "Не вышло скачать: "+err.Error())
		return
	}
	defer os.RemoveAll(dir)

	files, err := s.downloadMedia(ctx, link, dir)
	if err != nil {
		s.log.Warn("media download failed", "err", err)
		s.msg.Reply(ctx, m, "Не вышло скачать: "+mediaReason(err))
		return
	}
	for _, path := range files {
		if err := s.sendMedia(ctx, m, path); err != nil {
			s.log.Warn("media upload failed", "name", filepath.Base(path), "err", err)
			s.msg.Reply(ctx, m, "Скачал, но не смог отправить: "+err.Error())
			return
		}
	}
}

// probeVideo reads the size the video is shown at (a stored rotation of 90
// or 270 degrees swaps width and height) and its length. Zeros if ffprobe fails.
func probeVideo(ctx context.Context, path string) (w, h, dur int) {
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height:stream_side_data=rotation:stream_tags=rotate:format=duration",
		"-of", "json", path).Output()
	if err != nil {
		return 0, 0, 0
	}
	return parseProbe(out)
}

func parseProbe(out []byte) (w, h, dur int) {
	var p struct {
		Streams []struct {
			Width, Height int
			SideData      []struct {
				Rotation float64 `json:"rotation"`
			} `json:"side_data_list"`
			Tags struct {
				Rotate string `json:"rotate"`
			} `json:"tags"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if json.Unmarshal(out, &p) != nil || len(p.Streams) == 0 {
		return 0, 0, 0
	}
	st := p.Streams[0]
	w, h = st.Width, st.Height
	rot := 0.0
	for _, sd := range st.SideData {
		if sd.Rotation != 0 {
			rot = sd.Rotation
		}
	}
	if r, err := strconv.ParseFloat(st.Tags.Rotate, 64); err == nil && rot == 0 {
		rot = r
	}
	if r := int(math.Abs(rot)) % 180; r == 90 {
		w, h = h, w
	}
	if d, err := strconv.ParseFloat(p.Format.Duration, 64); err == nil {
		dur = int(d + 0.5)
	}
	return w, h, dur
}

func (s *AgentService) downloadMedia(ctx context.Context, link, dir string) ([]string, error) {
	cookies := filepath.Join(s.cfg.StateDir, cookiesFile)
	_, haveCookies := os.Stat(cookies)
	insta := strings.Contains(link, "instagram.com")

	// Best quality that fits Telegram's 50 MB: try 1080 first, then lower.
	var err error
	for _, res := range []string{"1080", "720", "480"} {
		args := []string{"--no-warnings", "--restrict-filenames", "--js-runtimes", "node",
			"-o", filepath.Join(dir, "%(autonumber)03d.%(ext)s"),
			"-S", "vcodec:h264,res:" + res + ",acodec:m4a", "--merge-output-format", "mp4",
			"--max-filesize", "49M", "--socket-timeout", "30", "--retries", "3",
			"--playlist-items", fmt.Sprintf("1-%d", maxMediaFiles)}
		if !insta {
			args = append(args, "--no-playlist")
		}
		if haveCookies == nil {
			args = append(args, "--cookies", cookies)
		}
		err = run(ctx, "yt-dlp", append(args, link)...)
		if files := mediaFiles(dir); len(files) > 0 {
			return files, nil
		}
		if err != nil && !strings.Contains(strings.ToLower(err.Error()), "max-filesize") && !strings.Contains(strings.ToLower(err.Error()), "larger than") {
			break // not a size problem: lower quality will not help
		}
	}
	if insta || strings.Contains(link, "tiktok.com") {
		// Photo posts: yt-dlp finds no video there.
		g := []string{"-q", "-d", dir, "--range", fmt.Sprintf("1-%d", maxMediaFiles)}
		if haveCookies == nil {
			g = append(g, "--cookies", cookies)
		}
		if gerr := run(ctx, "gallery-dl", append(g, link)...); gerr == nil {
			if files := mediaFiles(dir); len(files) > 0 {
				return files, nil
			}
		}
	}
	if err == nil {
		err = fmt.Errorf("ничего не скачалось")
	}
	return nil, err
}

func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s: %s", name, lastLine(stderr.String()))
	}
	return nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// mediaFiles lists what was downloaded, in order, skipping yt-dlp leftovers.
func mediaFiles(dir string) []string {
	var files []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".part", ".ytdl", ".json", ".txt", ".vtt", ".srt":
			return nil
		}
		files = append(files, path)
		return nil
	})
	sort.Strings(files)
	if len(files) > maxMediaFiles {
		files = files[:maxMediaFiles]
	}
	return files
}

func (s *AgentService) sendMedia(ctx context.Context, m *telegram.Message, path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Size() > telegram.MaxUpload {
		return fmt.Errorf("файл больше 50 МБ, Telegram такой от бота не примет")
	}
	c := s.msg.Client()
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".mov", ".webm", ".mkv", ".m4v":
		w, h, d := probeVideo(ctx, path)
		return c.SendVideo(ctx, m.Chat.ID, m.Thread(), m.MessageID, path, w, h, d)
	case ".jpg", ".jpeg", ".png", ".webp":
		return c.SendFile(ctx, m.Chat.ID, m.Thread(), m.MessageID, path, "", st.Size() < 5<<20)
	}
	return c.SendFile(ctx, m.Chat.ID, m.Thread(), m.MessageID, path, "", false)
}

// mediaReason is the short human version of a download error.
func mediaReason(err error) string {
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "login") || strings.Contains(low, "cookies") || strings.Contains(low, "rate-limit") || strings.Contains(low, "restricted"):
		return "нужен вход в аккаунт (куки): положи cookies.txt в state и повтори."
	case strings.Contains(low, "larger than max-filesize") || strings.Contains(low, "max-filesize"):
		return "файл больше 50 МБ, Telegram не примет."
	case err == context.DeadlineExceeded || strings.Contains(low, "deadline"):
		return "слишком долго, остановил."
	}
	if r := []rune(msg); len(r) > 200 {
		msg = string(r[:200]) + "…"
	}
	return msg
}
