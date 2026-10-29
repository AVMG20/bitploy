package tokens

import (
	"strings"
	"testing"
)

func TestNewTokenFormat(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		v, prefix, hash := New()
		if !strings.HasPrefix(v, "bp_") {
			t.Fatalf("token %q has no bp_ prefix", v)
		}
		if len(v) < 40 {
			t.Fatalf("token %q is too short for 32 random bytes", v)
		}
		if !WellFormed(v) {
			t.Fatalf("token %q is not WellFormed", v)
		}
		if prefix != v[:8] || len(prefix) != 8 {
			t.Fatalf("prefix %q is not the first 8 chars of %q", prefix, v)
		}
		if hash != Hash(v) || len(hash) != 64 || strings.Contains(hash, v) {
			t.Fatalf("hash %q looks wrong", hash)
		}
		if seen[v] {
			t.Fatal("duplicate token generated")
		}
		seen[v] = true
	}
}

func TestWellFormed(t *testing.T) {
	good, _, _ := New()
	tests := []struct {
		in   string
		want bool
	}{
		{good, true},
		{"", false},
		{"bp_", false},
		{"bp_short", false},
		{"xx_" + good[3:], false},
		{good + "!", false},
		{good + " ", false},
		{strings.Repeat("a", 60), false},
		{"bp_" + strings.Repeat("a", 200), false},
	}
	for _, tt := range tests {
		if got := WellFormed(tt.in); got != tt.want {
			t.Errorf("WellFormed(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestHashIsStableAndDistinct(t *testing.T) {
	a, _, _ := New()
	b, _, _ := New()
	if h1, h2 := Hash(a), Hash(a); h1 != h2 {
		t.Fatal("hash is not deterministic")
	}
	if Hash(a) == Hash(b) {
		t.Fatal("different tokens share a hash")
	}
}
