package agentloop

import (
	"testing"
	"unicode/utf8"
)

func TestTruncate(t *testing.T) {
	t.Run("short string is returned unchanged", func(t *testing.T) {
		if got := Truncate("hello", 10); got != "hello" {
			t.Errorf("Truncate(hello,10) = %q, want hello", got)
		}
	})
	t.Run("ascii is clipped with ellipsis", func(t *testing.T) {
		if got := Truncate("abcdefgh", 3); got != "abc…" {
			t.Errorf("got %q, want abc…", got)
		}
	})
	t.Run("max<=0 returns empty", func(t *testing.T) {
		if got := Truncate("abc", 0); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
	t.Run("never splits a multibyte rune mid-byte", func(t *testing.T) {
		// A string of 4-byte emoji: any byte-slice cut not on a rune boundary
		// would produce invalid UTF-8. Test every cut length around the boundary.
		s := "🚂🚃🚄🚅🚆🚇"
		for max := 1; max <= 8; max++ {
			got := Truncate(s, max)
			if !utf8.ValidString(got) {
				t.Errorf("Truncate(%q, %d) = %q produced invalid UTF-8", s, max, got)
			}
		}
	})
	t.Run("mixed ascii and CJK stays valid at the cut", func(t *testing.T) {
		s := "commit 你好世界 done"
		for max := 1; max <= len(s); max++ {
			if got := Truncate(s, max); !utf8.ValidString(got) {
				t.Errorf("Truncate at max=%d produced invalid UTF-8: %q", max, got)
			}
		}
	})
}
