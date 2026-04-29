package shortener_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/vancanhuit/url-shortener/internal/shortener"
)

func TestNewGenerator_RejectsLengthsOutOfRange(t *testing.T) {
	t.Parallel()
	for _, n := range []int{0, 1, shortener.MinLength - 1, shortener.MaxLength + 1, 1024} {
		if _, err := shortener.NewGenerator(n); !errors.Is(err, shortener.ErrLengthOutOfRange) {
			t.Errorf("NewGenerator(%d): want ErrLengthOutOfRange, got %v", n, err)
		}
	}
}

func TestNewGenerator_AcceptsLengthsInRange(t *testing.T) {
	t.Parallel()
	for _, n := range []int{shortener.MinLength, shortener.DefaultLength, shortener.MaxLength} {
		g, err := shortener.NewGenerator(n)
		if err != nil {
			t.Fatalf("NewGenerator(%d): %v", n, err)
		}
		if g.Length() != n {
			t.Errorf("NewGenerator(%d): Length() = %d, want %d", n, g.Length(), n)
		}
	}
}

func TestGenerate_ProducesCodesOfTheRequestedLengthAndAlphabet(t *testing.T) {
	t.Parallel()
	g, err := shortener.NewGenerator(shortener.DefaultLength)
	if err != nil {
		t.Fatalf("NewGenerator: %v", err)
	}
	for i := 0; i < 256; i++ {
		code, err := g.Generate()
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if len(code) != shortener.DefaultLength {
			t.Errorf("len(%q) = %d, want %d", code, len(code), shortener.DefaultLength)
		}
		for _, r := range code {
			if !strings.ContainsRune(shortener.Alphabet, r) {
				t.Errorf("code %q contains rune %q outside Alphabet", code, r)
			}
		}
	}
}

func TestGenerate_ProducesDistinctCodesAcrossManyDraws(t *testing.T) {
	t.Parallel()
	// 7^62 keyspace makes a 1024-sample collision astronomically unlikely;
	// this guards against an obvious bug like every code being identical.
	g, err := shortener.NewGenerator(shortener.DefaultLength)
	if err != nil {
		t.Fatalf("NewGenerator: %v", err)
	}
	const draws = 1024
	seen := make(map[string]struct{}, draws)
	for i := 0; i < draws; i++ {
		code, err := g.Generate()
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if _, dup := seen[code]; dup {
			t.Fatalf("collision after %d draws: %q", i+1, code)
		}
		seen[code] = struct{}{}
	}
}

func TestValidCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		code string
		want bool
	}{
		{"empty", "", false},
		{"too_short", strings.Repeat("a", shortener.MinLength-1), false},
		{"too_long", strings.Repeat("a", shortener.MaxLength+1), false},
		{"hyphen_disallowed", "abc-123", false},
		{"underscore_disallowed", "abc_123", false},
		{"slash_disallowed", "abc/123", false},
		{"min_length_ok", strings.Repeat("a", shortener.MinLength), true},
		{"default_length_ok", "abc1234", true},
		{"mixed_case_digits_ok", "AbCd0Z9", true},
		{"max_length_ok", strings.Repeat("Z", shortener.MaxLength), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := shortener.ValidCode(tt.code); got != tt.want {
				t.Errorf("ValidCode(%q) = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}
