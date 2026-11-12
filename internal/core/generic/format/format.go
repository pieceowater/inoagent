// Package format turns Claude's Markdown into what Telegram can show: its
// small HTML subset, in chunks under the message size limit, with known
// secrets scrubbed out.
package format

import (
	"html"
	"regexp"
	"strings"
)

// ChunkSize leaves headroom under Telegram's 4096-character limit: the limit
// counts the text after entities are parsed, and escaping never makes the
// visible text longer, so Markdown chunks of this size always fit.
const ChunkSize = 3800

// Redactor replaces known secret values (tokens from the environment) with a
// placeholder in everything the bot sends — a last line of defence if a
// command ever prints one.
type Redactor struct{ secrets []string }

func NewRedactor(values ...string) *Redactor {
	r := &Redactor{}
	for _, v := range values {
		if len(v) >= 8 {
			r.secrets = append(r.secrets, v)
		}
	}
	return r
}

func (r *Redactor) Redact(s string) string {
	for _, v := range r.secrets {
		s = strings.ReplaceAll(s, v, "[скрыто]")
	}
	return s
}

var (
	inlineCode = regexp.MustCompile("`([^`\n]+)`")
	bold       = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	link       = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+)\)`)
	heading    = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
	langName   = regexp.MustCompile(`^[\w+-]+$`)
)

// HTML converts the Markdown Claude writes into Telegram HTML. It handles the
// parts that matter in a chat — code blocks, inline code, bold, links,
// headings — and escapes everything else, so the result always parses.
// Italics are left alone on purpose: underscores in file and container names
// would turn into stray emphasis.
func HTML(md string) string {
	var out []string
	var code []string // lines of the open code block
	open := ""        // its opening tag; empty outside a block
	closeBlock := func() {
		out = append(out, open+html.EscapeString(strings.Join(code, "\n"))+"</code></pre>")
		code, open = nil, ""
	}
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```") && open == "":
			open = "<pre><code>"
			if lang := strings.TrimSpace(trimmed[3:]); lang != "" && langName.MatchString(lang) {
				open = `<pre><code class="language-` + lang + `">`
			}
		case strings.HasPrefix(trimmed, "```"):
			closeBlock()
		case open != "":
			code = append(code, line)
		default:
			out = append(out, inline(line))
		}
	}
	if open != "" {
		closeBlock()
	}
	return strings.Join(out, "\n")
}

func inline(line string) string {
	if m := heading.FindStringSubmatch(line); m != nil {
		return "<b>" + inline(m[1]) + "</b>"
	}
	// Pull inline code out first so nothing inside it gets formatted.
	var codes []string
	line = inlineCode.ReplaceAllStringFunc(line, func(s string) string {
		codes = append(codes, s[1:len(s)-1])
		return "\x00" + string(rune('A'+len(codes)-1)) + "\x00"
	})
	line = html.EscapeString(line)
	line = bold.ReplaceAllString(line, "<b>$1</b>")
	line = link.ReplaceAllString(line, `<a href="$2">$1</a>`)
	for i, c := range codes {
		line = strings.Replace(line, "\x00"+string(rune('A'+i))+"\x00", "<code>"+html.EscapeString(c)+"</code>", 1)
	}
	return line
}

// Split cuts Markdown into chunks of at most size runes, preferring paragraph
// and line breaks, and closes/reopens a code fence that a cut lands inside, so
// every chunk converts to valid HTML on its own.
func Split(md string, size int) []string {
	md = strings.TrimSpace(md)
	if md == "" {
		return nil
	}
	var chunks []string
	var cur strings.Builder
	curLen := 0
	prefix := 0 // length of the reopened fence a fresh chunk starts with
	fence := "" // the open fence line, if the current chunk is inside one

	flush := func() {
		s := cur.String()
		if fence != "" {
			s += "\n```"
		}
		if strings.TrimSpace(s) != "" {
			chunks = append(chunks, strings.TrimRight(s, "\n"))
		}
		cur.Reset()
		curLen = 0
		if fence != "" {
			cur.WriteString(fence + "\n")
			curLen = len([]rune(fence)) + 1
		}
		prefix = curLen
	}

	for _, line := range strings.Split(md, "\n") {
		// A single line longer than a chunk is hard-wrapped.
		for r := []rune(line); len(r) > 0; {
			room := size - curLen - 4 // keep space for a closing fence
			if room <= 0 && curLen > prefix {
				flush()
				continue
			}
			room = max(room, 1)
			if len(r) > room && curLen > prefix {
				flush()
				continue
			}
			n := min(len(r), room)
			cur.WriteString(string(r[:n]))
			curLen += n
			r = r[n:]
			if len(r) > 0 {
				flush()
			}
		}
		cur.WriteString("\n")
		curLen++
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "```") {
			if fence == "" {
				fence = t
			} else {
				fence = ""
			}
		}
	}
	s := cur.String()
	if fence != "" {
		s += "```"
	}
	if strings.TrimSpace(s) != "" {
		chunks = append(chunks, strings.TrimRight(s, "\n"))
	}
	return chunks
}
