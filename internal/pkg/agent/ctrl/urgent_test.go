package ctrl

import "testing"

func TestIsUrgent(t *testing.T) {
	for text, want := range map[string]bool{
		"срочно глянь диск":           true,
		"Это критично, падает прод":   true,
		"третично сделай":             true,
		"нужно ASAP":                  true,
		"глянь диск":                  false,
		"несрочно, когда будет время": false,
	} {
		if got := isUrgent(text); got != want {
			t.Errorf("isUrgent(%q) = %v, want %v", text, got, want)
		}
	}
}
