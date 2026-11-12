package svc

import (
	"fmt"
	"strings"
	"time"

	"app/internal/core/telegram"
	"app/internal/pkg/agent/ent"
)

// Messages people send to each other are not for the agent to answer, but
// they are part of the conversation: they are kept per topic and handed over
// with the next request that is meant for the agent.
const (
	maxOverheard = 30
	overheardTTL = 24 * time.Hour
)

type overheard struct {
	at   time.Time
	who  string
	text string
}

// Overhear remembers a message that was not addressed to the agent.
func (s *AgentService) Overhear(m *telegram.Message, text string) {
	if text == "" && m.HasAttachment() {
		text = "(вложение)"
	}
	if text == "" || m.From == nil {
		return
	}
	key := ent.KeyOf(m)
	s.heardMu.Lock()
	defer s.heardMu.Unlock()
	list := append(s.heard[key], overheard{at: time.Now(), who: m.From.DisplayName(), text: truncate(text, 500)})
	if len(list) > maxOverheard {
		list = list[len(list)-maxOverheard:]
	}
	s.heard[key] = list
}

// takeOverheard hands over what was said in the topic since the last request
// and forgets it.
func (s *AgentService) takeOverheard(key ent.TopicKey) string {
	s.heardMu.Lock()
	list := s.heard[key]
	delete(s.heard, key)
	s.heardMu.Unlock()
	return formatOverheard(list, time.Now())
}

func formatOverheard(list []overheard, now time.Time) string {
	var b strings.Builder
	for _, o := range list {
		if now.Sub(o.at) > overheardTTL {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", o.who, o.text)
	}
	if b.Len() == 0 {
		return ""
	}
	return "(пока тебя не звали, в чате писали между собой — это контекст, а не просьбы к тебе:\n" + b.String() + ")\n\n"
}
