package svc

import (
	"context"
	"strings"
	"time"

	"app/internal/core/claude"
	"app/internal/core/generic/format"
	"app/internal/core/telegram"
)

const (
	// streamEvery is the pause between edits: groups allow about 20 messages
	// and edits a minute.
	streamEvery = 3 * time.Second
	firstMin    = 120 // runes of text before the first message is sent
	// streamChunk is how much text one live message holds before the next
	// one is started; HTML escapes make the sent text longer than this.
	streamChunk = 3000
)

// newMarker is put by the agent on its own line between answers to different
// requests of one run: the next answer goes to a new message.
const newMarker = "[[НОВОЕ]]"

// StopData is the callback data of the live message's stop button.
const StopData = "m:stop"

var stopMarkup = &telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{{
	{Text: "⏹ Стоп", CallbackData: StopData},
}}}

// stream shows the answer as it is written: the first words arrive as a
// message, and the message is edited as the text grows. Text from later
// turns of the same run (after tool calls) is appended to it. While a tool
// runs nothing is shown; a stop button is there until the run is over.
type stream struct {
	s       *AgentService
	to      *telegram.Message
	text    []rune // everything written so far
	from    int    // where the current message's part of text begins
	sep     bool   // a new turn began: put a blank line before the next fragment
	msg     telegram.Message
	sent    int // runes of text the current message already shows
	last    time.Time
	final   bool // finish was called: send whatever is left
	settled bool // finish or close was called: no buttons any more
}

func newStream(s *AgentService, to *telegram.Message) *stream { return &stream{s: s, to: to} }

func (st *stream) started() bool { return st.msg.MessageID != 0 || st.from > 0 }

func (st *stream) feed(ctx context.Context, ev claude.Event) {
	switch {
	case ev.NewTurn:
		st.flush(ctx)
		st.sep = len(st.text) > 0
	case ev.Delta != "":
		if st.sep {
			st.text = append(st.text, '\n', '\n')
			st.sep = false
		}
		st.text = append(st.text, []rune(ev.Delta)...)
		st.split(ctx)
		st.maybeSend(ctx)
	}
}

// split ends the current message at every newMarker: what came before is
// final, what follows starts a message of its own.
func (st *stream) split(ctx context.Context) {
	if st.settled {
		return
	}
	mark := []rune(newMarker)
	for {
		part := string(st.text[st.from:])
		i := strings.Index(part, newMarker)
		if i < 0 {
			return
		}
		n := len([]rune(part[:i]))
		head := strings.TrimSpace(string(st.text[st.from : st.from+n]))
		if head != "" {
			if st.msg.MessageID == 0 {
				st.s.msg.SendHTML(ctx, st.to, st.render(head, false), nil)
			} else {
				st.s.msg.EditHTML(ctx, st.msg, st.render(head, false), nil)
			}
		} else if st.msg.MessageID != 0 {
			st.s.msg.Delete(ctx, st.msg)
		}
		st.from += n + len(mark)
		st.msg, st.sent = telegram.Message{}, 0
	}
}

// hideMarker cuts a half-typed newMarker off the end of a text.
func hideMarker(part string) string {
	r, m := []rune(part), []rune(newMarker)
	for k := min(len(m)-1, len(r)); k > 0; k-- {
		if string(r[len(r)-k:]) == string(m[:k]) {
			return string(r[:len(r)-k])
		}
	}
	return part
}

// maybeSend sends or edits when enough time has passed.
func (st *stream) maybeSend(ctx context.Context) {
	if time.Since(st.last) < streamEvery && st.msg.MessageID != 0 {
		return
	}
	st.flush(ctx)
}

// render is the HTML of the current message part; more adds "..." to say
// that the message is still being written.
func (st *stream) render(md string, more bool) string {
	out := format.HTML(md)
	if more {
		out += "..."
	}
	return out
}

// flush makes the live message show everything written so far.
func (st *stream) flush(ctx context.Context) {
	if st.settled {
		return
	}
	part := hideMarker(string(st.text[st.from:]))
	if strings.TrimSpace(part) == "" {
		return
	}
	// Don't show "NO_REPLY" (the agent's way to stay quiet) while it is typed.
	if t := strings.TrimSpace(part); t != "" && st.msg.MessageID == 0 && strings.HasPrefix(silentAnswer, t) {
		return
	}
	if len(st.text)-st.from > streamChunk && st.msg.MessageID != 0 {
		// Full: finish this message, the rest goes to a new one.
		cut := st.from + streamChunk
		st.s.msg.EditHTML(ctx, st.msg, st.render(string(st.text[st.from:cut]), false), nil)
		st.msg, st.from, st.sent = telegram.Message{}, cut, 0
		part = string(st.text[st.from:])
		if strings.TrimSpace(part) == "" {
			return
		}
	}
	if len(st.text)-st.from == st.sent && st.msg.MessageID != 0 {
		return
	}
	if st.msg.MessageID == 0 && !st.final && len([]rune(strings.TrimSpace(part))) < firstMin {
		return // wait until there is something worth showing
	}
	st.last = time.Now()
	if st.msg.MessageID == 0 {
		sent, err := st.s.msg.SendHTML(ctx, st.to, st.render(part, true), stopMarkup)
		if err == nil {
			st.msg = sent
			st.sent = len(st.text) - st.from
		}
		return
	}
	st.s.msg.EditHTML(ctx, st.msg, st.render(part, true), stopMarkup)
	st.sent = len(st.text) - st.from
}

// trimMarker drops a marker the agent put at the end of its answer.
func (st *stream) trimMarker(marker string) {
	s := strings.TrimRight(string(st.text), " \n")
	if strings.HasSuffix(s, marker) {
		s = strings.TrimRight(strings.TrimSuffix(s, marker), " \n")
		st.text = []rune(s)
		st.sent = -1 // force the edit
	}
}

// finish shows the final text, takes the buttons off and returns the last
// message of the answer.
func (st *stream) finish(ctx context.Context, marker string) telegram.Message {
	if marker != "" {
		st.trimMarker(marker)
	}
	st.final = true
	st.split(ctx)
	st.flush(ctx)
	st.settled = true
	if st.msg.MessageID != 0 {
		st.s.msg.EditHTML(ctx, st.msg, st.render(string(st.text[st.from:]), false), nil)
	}
	return st.msg
}

// close takes the buttons off a message left behind by a run that failed.
func (st *stream) close(ctx context.Context) {
	if st.settled || st.msg.MessageID == 0 {
		return
	}
	st.settled = true
	st.s.msg.EditHTML(ctx, st.msg, st.render(string(st.text[st.from:]), false), nil)
}

// retract removes what was streamed when the run ended up with nothing to say.
func (st *stream) retract(ctx context.Context) {
	st.settled = true
	if st.msg.MessageID == 0 {
		return
	}
	part := strings.TrimSpace(string(st.text[st.from:]))
	if body := strings.TrimSpace(strings.TrimSuffix(part, silentAnswer)); body != "" {
		st.s.msg.EditHTML(ctx, st.msg, st.render(body, false), nil)
		return
	}
	st.s.msg.Delete(ctx, st.msg)
}
