package svc

import "testing"

func TestHideMarker(t *testing.T) {
	for in, want := range map[string]string{
		"готово\n[[НО": "готово\n",
		"готово [[":    "готово ",
		"готово":       "готово",
		"a [x":         "a [x",
	} {
		if got := hideMarker(in); got != want {
			t.Errorf("hideMarker(%q) = %q, want %q", in, got, want)
		}
	}
}
