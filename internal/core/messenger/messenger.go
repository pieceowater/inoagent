// Package messenger is how every module talks back to Telegram: replies land
// in the topic of the message they answer, Markdown is converted to Telegram
// HTML, long answers are split, and known secrets are scrubbed out of
// everything on the way.
package messenger

import (
	"context"
	"html"
	"log/slog"
	"strings"

	"app/internal/core/generic/format"
	"app/internal/core/telegram"
)

type Messenger struct {
	tg     *telegram.Client
	redact *format.Redactor
	log    *slog.Logger
}

func New(tg *telegram.Client, redact *format.Redactor, log *slog.Logger) *Messenger {
	return &Messenger{tg: tg, redact: redact, log: log}
}

// Client is the raw Bot API client, for the calls that aren't messages.
func (m *Messenger) Client() *telegram.Client { return m.tg }

// SendHTML sends an already-built HTML message into the topic of to, as a
// reply to it.
func (m *Messenger) SendHTML(ctx context.Context, to *telegram.Message, htmlText string, markup *telegram.InlineKeyboardMarkup) (telegram.Message, error) {
	p := telegram.SendMessageParams{
		ChatID:          to.Chat.ID,
		MessageThreadID: to.Thread(),
		Text:            m.redact.Redact(htmlText),
		ParseMode:       "HTML",
		ReplyParameters: &telegram.ReplyParameters{MessageID: to.MessageID, AllowSendingWithoutReply: true},
		LinkPreview:     &telegram.LinkPreviewOptions{IsDisabled: true},
	}
	if markup != nil {
		p.ReplyMarkup = markup
	}
	return m.send(ctx, to, p, htmlText)
}

// SendKeyboard sends a message that also sets the chat's button keyboard.
func (m *Messenger) SendKeyboard(ctx context.Context, to *telegram.Message, htmlText string, kb *telegram.ReplyKeyboardMarkup) (telegram.Message, error) {
	p := telegram.SendMessageParams{
		ChatID:          to.Chat.ID,
		MessageThreadID: to.Thread(),
		Text:            m.redact.Redact(htmlText),
		ParseMode:       "HTML",
		ReplyMarkup:     kb,
	}
	if to.MessageID != 0 {
		p.ReplyParameters = &telegram.ReplyParameters{MessageID: to.MessageID, AllowSendingWithoutReply: true}
	}
	return m.send(ctx, to, p, htmlText)
}

func (m *Messenger) send(ctx context.Context, to *telegram.Message, p telegram.SendMessageParams, htmlText string) (telegram.Message, error) {
	sent, err := m.tg.SendMessage(ctx, p)
	if telegram.IsParseError(err) {
		// The HTML is built to always parse; if Telegram disagrees, the
		// words still matter more than the formatting.
		p.ParseMode = ""
		p.Text = m.redact.Redact(stripTags(htmlText))
		sent, err = m.tg.SendMessage(ctx, p)
	}
	if err != nil {
		m.log.Error("sendMessage failed", "chat_id", to.Chat.ID, "err", err)
	}
	return sent, err
}

// EditHTML replaces the text (and buttons) of a message the bot sent.
func (m *Messenger) EditHTML(ctx context.Context, msg telegram.Message, htmlText string, markup *telegram.InlineKeyboardMarkup) {
	if msg.MessageID == 0 {
		return
	}
	err := m.tg.EditMessageText(ctx, telegram.EditMessageTextParams{
		ChatID:      msg.Chat.ID,
		MessageID:   msg.MessageID,
		Text:        m.redact.Redact(htmlText),
		ParseMode:   "HTML",
		ReplyMarkup: markup,
	})
	if err != nil {
		m.log.Warn("editMessageText failed", "err", err)
	}
}

func (m *Messenger) Delete(ctx context.Context, msg telegram.Message) {
	if msg.MessageID != 0 {
		_ = m.tg.DeleteMessage(ctx, msg.Chat.ID, msg.MessageID)
	}
}

// Reply sends a short Markdown text.
func (m *Messenger) Reply(ctx context.Context, to *telegram.Message, md string) {
	_, _ = m.SendHTML(ctx, to, format.HTML(md), nil)
}

// Deliver sends a full agent answer, split into as many messages as it takes,
// and returns the last one sent (zero if nothing went out).
func (m *Messenger) Deliver(ctx context.Context, to *telegram.Message, md string) telegram.Message {
	chunks := format.Split(m.redact.Redact(md), format.ChunkSize)
	if len(chunks) == 0 {
		chunks = []string{"(пустой ответ)"}
	}
	var last telegram.Message
	for _, c := range chunks {
		sent, err := m.SendHTML(ctx, to, format.HTML(c), nil)
		if err != nil {
			return last
		}
		last = sent
	}
	return last
}

func stripTags(s string) string {
	var out strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			out.WriteRune(r)
		}
	}
	return html.UnescapeString(out.String())
}
