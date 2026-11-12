// Package telegram is a deliberately small Bot API client: long polling plus
// the handful of methods the bridge calls. Long polling is outbound-only, which
// is the whole reason it works on a server with no public IP.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	base     string
	fileBase string
	http     *http.Client
}

func New(token string) *Client {
	return &Client{
		base:     "https://api.telegram.org/bot" + token + "/",
		fileBase: "https://api.telegram.org/file/bot" + token + "/",
		// Must outlive the long-poll timeout passed to getUpdates.
		http: &http.Client{Timeout: 90 * time.Second, Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 15 * time.Second}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			// A half-dead pooled connection hangs until the client timeout,
			// so don't keep idle ones around for long.
			IdleConnTimeout:     20 * time.Second,
			MaxIdleConnsPerHost: 2,
		}},
	}
}

// ResetConnections drops pooled connections so the next request dials fresh.
func (c *Client) ResetConnections() { c.http.CloseIdleConnections() }

// APIError is a Bot API "ok": false response.
type APIError struct {
	Code        int
	Description string
	RetryAfter  int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram: %d %s", e.Code, e.Description)
}

// IsNotModified is the harmless error an edit returns when the text is unchanged.
func IsNotModified(err error) bool {
	var e *APIError
	return errors.As(err, &e) && strings.Contains(e.Description, "message is not modified")
}

// IsParseError means Telegram rejected the HTML; callers resend as plain text.
func IsParseError(err error) bool {
	var e *APIError
	return errors.As(err, &e) && strings.Contains(e.Description, "can't parse entities")
}

type response struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (c *Client) call(ctx context.Context, method string, params, out any) error {
	for attempt := 0; ; attempt++ {
		err := c.callOnce(ctx, method, params, out)
		var apiErr *APIError
		// Flood control: wait as told and try once more, rather than drop a reply.
		if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 && attempt == 0 && apiErr.RetryAfter <= 30 {
			select {
			case <-time.After(time.Duration(apiErr.RetryAfter) * time.Second):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return err
	}
}

func (c *Client) callOnce(ctx context.Context, method string, params, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// The URL carries the bot token; never let it reach a log line.
		var urlErr interface{ Unwrap() error }
		if errors.As(err, &urlErr) {
			return fmt.Errorf("telegram %s: %w", method, urlErr.Unwrap())
		}
		return fmt.Errorf("telegram %s: request failed", method)
	}
	defer resp.Body.Close()

	var r response
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("telegram %s: bad response (HTTP %d): %w", method, resp.StatusCode, err)
	}
	if !r.OK {
		e := &APIError{Code: r.ErrorCode, Description: r.Description}
		if r.Parameters != nil {
			e.RetryAfter = r.Parameters.RetryAfter
		}
		return e
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func (c *Client) GetMe(ctx context.Context) (User, error) {
	var u User
	err := c.call(ctx, "getMe", struct{}{}, &u)
	return u, err
}

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	var us []Update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         timeoutSec,
		"allowed_updates": []string{"message", "callback_query", "my_chat_member"},
	}, &us)
	return us, err
}

func (c *Client) SendMessage(ctx context.Context, p SendMessageParams) (Message, error) {
	var m Message
	err := c.call(ctx, "sendMessage", p, &m)
	return m, err
}

func (c *Client) EditMessageText(ctx context.Context, p EditMessageTextParams) error {
	err := c.call(ctx, "editMessageText", p, nil)
	if IsNotModified(err) {
		return nil
	}
	return err
}

func (c *Client) DeleteMessage(ctx context.Context, chatID int64, messageID int) error {
	return c.call(ctx, "deleteMessage", map[string]any{"chat_id": chatID, "message_id": messageID}, nil)
}

func (c *Client) AnswerCallbackQuery(ctx context.Context, id, text string) error {
	return c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil)
}

func (c *Client) GetChatMember(ctx context.Context, chatID, userID int64) (ChatMember, error) {
	var m ChatMember
	err := c.call(ctx, "getChatMember", map[string]any{"chat_id": chatID, "user_id": userID}, &m)
	return m, err
}

func (c *Client) LeaveChat(ctx context.Context, chatID int64) error {
	return c.call(ctx, "leaveChat", map[string]any{"chat_id": chatID}, nil)
}

func (c *Client) SendChatAction(ctx context.Context, chatID int64, thread int, action string) error {
	p := map[string]any{"chat_id": chatID, "action": action}
	if thread != 0 {
		p["message_thread_id"] = thread
	}
	return c.call(ctx, "sendChatAction", p, nil)
}

// SetReaction puts one emoji reaction on a message; Telegram accepts only its
// own fixed set of emoji.
func (c *Client) SetReaction(ctx context.Context, chatID int64, messageID int, emoji string) error {
	return c.call(ctx, "setMessageReaction", map[string]any{
		"chat_id": chatID, "message_id": messageID,
		"reaction": []map[string]string{{"type": "emoji", "emoji": emoji}},
	}, nil)
}

// ClearReaction removes the bot's reaction. Telegram does not tell bots about
// deleted messages, but it does refuse to touch one: IsGone on the error is the
// only way to learn that a message is no longer there.
func (c *Client) ClearReaction(ctx context.Context, chatID int64, messageID int) error {
	return c.call(ctx, "setMessageReaction", map[string]any{
		"chat_id": chatID, "message_id": messageID, "reaction": []map[string]string{},
	}, nil)
}

// IsGone means the message was deleted (or never existed).
func IsGone(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Code == 400 &&
		(strings.Contains(e.Description, "not found") || strings.Contains(e.Description, "MESSAGE_ID_INVALID"))
}

// PinMessage pins a message quietly (no notification for the chat).
func (c *Client) PinMessage(ctx context.Context, chatID int64, messageID int) error {
	return c.call(ctx, "pinChatMessage", map[string]any{
		"chat_id": chatID, "message_id": messageID, "disable_notification": true,
	}, nil)
}

// Poll is what the agent asks for with the `poll` helper.
type Poll struct {
	Question  string   `json:"question"`
	Options   []string `json:"options"`
	Anonymous bool     `json:"anonymous"`
	Multiple  bool     `json:"multiple"`
}

func (c *Client) SendPoll(ctx context.Context, chatID int64, thread, replyTo int, p Poll) error {
	opts := make([]map[string]string, len(p.Options))
	for i, o := range p.Options {
		opts[i] = map[string]string{"text": o}
	}
	params := map[string]any{
		"chat_id": chatID, "question": p.Question, "options": opts,
		"is_anonymous": p.Anonymous, "allows_multiple_answers": p.Multiple,
	}
	if thread != 0 {
		params["message_thread_id"] = thread
	}
	if replyTo != 0 {
		params["reply_parameters"] = ReplyParameters{MessageID: replyTo, AllowSendingWithoutReply: true}
	}
	return c.call(ctx, "sendPoll", params, nil)
}

// CreateTopic makes a new forum topic and returns its thread id.
func (c *Client) CreateTopic(ctx context.Context, chatID int64, name string) (int, error) {
	var out struct {
		ThreadID int `json:"message_thread_id"`
	}
	err := c.call(ctx, "createForumTopic", map[string]any{"chat_id": chatID, "name": name}, &out)
	return out.ThreadID, err
}

func (c *Client) SetMyCommands(ctx context.Context, cmds []BotCommand) error {
	return c.call(ctx, "setMyCommands", map[string]any{"commands": cmds}, nil)
}

// MaxDownload is the Bot API's limit on files a bot can download.
const MaxDownload = 20 << 20

// Download saves an attachment to dst. Files over MaxDownload can't be
// fetched through the Bot API at all.
func (c *Client) Download(ctx context.Context, fileID, dst string) error {
	var f File
	if err := c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &f); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.fileBase+f.FilePath, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram download: request failed") // the URL carries the token
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram download: HTTP %d", resp.StatusCode)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(resp.Body, MaxDownload+1)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// MaxUpload is the Bot API's limit on files a bot can send.
const MaxUpload = 50 << 20

// SendFile uploads a local file into a chat (topic), as a photo when photo is
// set (shown inline, recompressed) or as a document (kept byte for byte).
func (c *Client) SendFile(ctx context.Context, chatID int64, thread, replyTo int, path, caption string, photo bool) error {
	method, field := "sendDocument", "document"
	if photo {
		method, field = "sendPhoto", "photo"
	}
	return c.upload(ctx, method, field, chatID, thread, replyTo, path, caption, nil)
}

// SendVoice uploads an OGG/Opus file as a voice message.
func (c *Client) SendVoice(ctx context.Context, chatID int64, thread, replyTo int, path string) error {
	return c.upload(ctx, "sendVoice", "voice", chatID, thread, replyTo, path, "", nil)
}

// SendVideo uploads a video that plays right in the chat.
// width, height and duration (seconds) tell Telegram the real shape of the
// picture; without them a vertical video can show up as a square.
func (c *Client) SendVideo(ctx context.Context, chatID int64, thread, replyTo int, path string, width, height, duration int) error {
	extra := map[string]string{"supports_streaming": "true"}
	if width > 0 && height > 0 {
		extra["width"], extra["height"] = strconv.Itoa(width), strconv.Itoa(height)
	}
	if duration > 0 {
		extra["duration"] = strconv.Itoa(duration)
	}
	return c.upload(ctx, "sendVideo", "video", chatID, thread, replyTo, path, "", extra)
}

func (c *Client) upload(ctx context.Context, method, field string, chatID int64, thread, replyTo int, path, caption string, extra map[string]string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	pr, pw := io.Pipe()
	w := multipart.NewWriter(pw)
	go func() {
		fields := map[string]string{"chat_id": strconv.FormatInt(chatID, 10), "caption": caption}
		for k, v := range extra {
			fields[k] = v
		}
		if thread != 0 {
			fields["message_thread_id"] = strconv.Itoa(thread)
		}
		if replyTo != 0 {
			fields["reply_parameters"] = fmt.Sprintf(`{"message_id":%d,"allow_sending_without_reply":true}`, replyTo)
		}
		for k, v := range fields {
			if v != "" {
				_ = w.WriteField(k, v)
			}
		}
		part, err := w.CreateFormFile(field, filepath.Base(path))
		if err == nil {
			_, err = io.Copy(part, f)
		}
		if err == nil {
			err = w.Close()
		}
		pw.CloseWithError(err)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+method, pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	upload := &http.Client{Timeout: 10 * time.Minute}
	resp, err := upload.Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s: request failed", method) // the URL carries the token
	}
	defer resp.Body.Close()
	var r response
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("telegram %s: bad response (HTTP %d)", method, resp.StatusCode)
	}
	if !r.OK {
		return &APIError{Code: r.ErrorCode, Description: r.Description}
	}
	return nil
}
