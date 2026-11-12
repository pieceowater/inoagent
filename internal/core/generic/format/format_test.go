package format

import (
	"strings"
	"testing"
)

func TestHTML(t *testing.T) {
	cases := map[string]string{
		"**жирный** и `docker ps`":          "<b>жирный</b> и <code>docker ps</code>",
		"a < b & c":                         "a &lt; b &amp; c",
		"`<tag>` here":                      "<code>&lt;tag&gt;</code> here",
		"## Итог":                           "<b>Итог</b>",
		"[jellyfin](http://127.0.0.1:8096)": `<a href="http://127.0.0.1:8096">jellyfin</a>`,
		"snake_case_name":                   "snake_case_name",
		"```bash\nrm -rf <x>\n```":          "<pre><code class=\"language-bash\">rm -rf &lt;x&gt;</code></pre>",
		"```\nopen":                         "<pre><code>open</code></pre>",
	}
	for in, want := range cases {
		if got := HTML(in); got != want {
			t.Errorf("HTML(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplit(t *testing.T) {
	long := "```\n" + strings.Repeat("line of log output\n", 400) + "```\nafter"
	chunks := Split(long, 1000)
	if len(chunks) < 2 {
		t.Fatalf("want several chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if n := len([]rune(c)); n > 1000 {
			t.Errorf("chunk %d is %d runes", i, n)
		}
		if strings.Count(c, "```")%2 != 0 {
			t.Errorf("chunk %d has an unbalanced fence:\n%s", i, c)
		}
	}
	// One enormous line inside a fence must not loop forever.
	huge := "```\n" + strings.Repeat("x", 5000) + "\n```"
	for i, c := range Split(huge, 1000) {
		if n := len([]rune(c)); n > 1000 {
			t.Errorf("huge chunk %d is %d runes", i, n)
		}
	}
	if got := Split("  ", 100); got != nil {
		t.Errorf("blank input: %q", got)
	}
}

func TestRedact(t *testing.T) {
	r := NewRedactor("sk-ant-oat01-SECRETSECRET", "", "short")
	if got := r.Redact("token=sk-ant-oat01-SECRETSECRET short"); got != "token=[скрыто] short" {
		t.Errorf("got %q", got)
	}
}
