package svc

import "testing"

func TestMediaURL(t *testing.T) {
	for in, want := range map[string]bool{
		"https://www.instagram.com/p/DfAv617tUxP/?exln=MWQ3OGUxY2JpNHoxOQ==": true,
		" https://www.instagram.com/reel/abc/ ":                              true,
		"https://vm.tiktok.com/ZMabc/":                                       true,
		"https://www.tiktok.com/@u/video/123":                                true,
		"https://youtu.be/abc123":                                            true,
		"https://www.youtube.com/shorts/abc":                                 true,
		"https://music.youtube.com/watch?v=1":                                true,
		"глянь https://youtu.be/abc":                                         false,
		"https://youtu.be/abc ну как":                                        false,
		"https://example.com/youtube.com":                                    false,
		"https://notyoutube.com/x":                                           false,
		"youtu.be/abc":                                                       false,
	} {
		if _, got := MediaURL(in); got != want {
			t.Errorf("MediaURL(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseProbe(t *testing.T) {
	tests := []struct {
		name      string
		json      string
		w, h, dur int
	}{
		{"plain vertical", `{"streams":[{"width":720,"height":1280}],"format":{"duration":"12.4"}}`, 720, 1280, 12},
		{"rotated side data", `{"streams":[{"width":1280,"height":720,"side_data_list":[{"rotation":-90}]}],"format":{"duration":"3.6"}}`, 720, 1280, 4},
		{"rotate tag", `{"streams":[{"width":1920,"height":1080,"tags":{"rotate":"90"}}],"format":{}}`, 1080, 1920, 0},
		{"junk", `nope`, 0, 0, 0},
	}
	for _, tt := range tests {
		w, h, d := parseProbe([]byte(tt.json))
		if w != tt.w || h != tt.h || d != tt.dur {
			t.Errorf("%s: got %d %d %d, want %d %d %d", tt.name, w, h, d, tt.w, tt.h, tt.dur)
		}
	}
}
