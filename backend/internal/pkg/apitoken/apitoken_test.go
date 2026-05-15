package apitoken

import (
	"strings"
	"testing"
)

func TestGenerateAndHash(t *testing.T) {
	tok, hash, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.HasPrefix(tok, Prefix) {
		t.Fatalf("missing prefix: %q", tok)
	}
	if !IsWellFormed(tok) {
		t.Fatalf("IsWellFormed false for generated token %q", tok)
	}
	if Hash(tok) != hash {
		t.Fatalf("hash mismatch: %q vs %q", Hash(tok), hash)
	}
}

func TestIsWellFormed(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"", false},
		{"abc", false},
		{"ztk_short_000000", false},
		{"ztk_AAAAAAAAAAAAAAAAAAAAAA_zzzzzz", false}, // bad checksum
	}
	for _, c := range cases {
		if IsWellFormed(c.in) != c.ok {
			t.Errorf("IsWellFormed(%q) = %v, want %v", c.in, !c.ok, c.ok)
		}
	}
}

func TestGeneratedTokensAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := range 64 {
		tok, _, err := Generate()
		if err != nil {
			t.Fatal(err)
		}
		if seen[tok] {
			t.Fatalf("collision after %d iters", i)
		}
		seen[tok] = true
		_ = i
	}
}
