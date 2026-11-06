package textutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateKeepsRunes(t *testing.T) {
	s := "ab错误"
	for n := 0; n <= len(s)+1; n++ {
		got := Truncate(s, n)
		if !utf8.ValidString(got) || len(got) > n || !strings.HasPrefix(s, got) {
			t.Errorf("Truncate(%q, %d) = %q", s, n, got)
		}
	}
	if Truncate(s, 4) != "ab" || Truncate(s, 5) != "ab错" {
		t.Errorf("unexpected cut: %q %q", Truncate(s, 4), Truncate(s, 5))
	}
}

func TestClean(t *testing.T) {
	if got := Clean("ok\xffbad\x00nul"); got != "ok�badnul" {
		t.Errorf("Clean = %q", got)
	}
	m := CleanValue(map[string]any{"k\x00": []any{"v\xff", 1}}).(map[string]any)
	if v, ok := m["k"].([]any); !ok || v[0] != "v�" {
		t.Errorf("CleanValue = %#v", m)
	}
}
