// Package asr turns voice messages into text with the local Whisper service
// (the `whisper` container in docker-compose.yml): nothing leaves the server.
package asr

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	URL      string // e.g. http://127.0.0.1:9077; empty disables transcription
	Language string // e.g. "ru"; empty lets Whisper detect it
	http     *http.Client
}

func New(baseURL, language string) *Client {
	// The first request after start loads the model; long voice messages on
	// a CPU take a while too.
	return &Client{URL: strings.TrimRight(baseURL, "/"), Language: language, http: &http.Client{Timeout: 10 * time.Minute}}
}

// Transcribe sends an audio file (any format ffmpeg reads: Telegram's
// ogg/opus voice notes included) and returns the text.
func (c *Client) Transcribe(ctx context.Context, path string) (string, error) {
	if c.URL == "" {
		return "", fmt.Errorf("распознавание речи не настроено")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("audio_file", filepath.Base(path))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}

	q := url.Values{"task": {"transcribe"}, "output": {"txt"}, "encode": {"true"}, "vad_filter": {"true"}}
	if c.Language != "" {
		q.Set("language", c.Language)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/asr?"+q.Encode(), &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("whisper недоступен: %w", err)
	}
	defer resp.Body.Close()
	text, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("whisper: HTTP %d", resp.StatusCode)
	}
	return strings.TrimSpace(string(text)), nil
}
