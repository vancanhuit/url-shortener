// Package shortener generates short, URL-safe codes using a fixed base62
// alphabet ([0-9A-Za-z]). Codes are produced from cryptographically secure
// random bytes via rejection sampling so every position is uniformly
// distributed across the alphabet.
//
// At the default length of 7 the keyspace is 62^7 ≈ 3.5e12 -- more than
// enough headroom for the unique-constraint retry loop in the API layer to
// almost never trigger, while keeping URLs short.
package shortener

import (
	"crypto/rand"
	"errors"
	"fmt"
)

// Alphabet is the base62 character set used to render generated codes. The
// ordering ([0-9][A-Z][a-z]) is conventional; do not reorder, as it is also
// the alphabet accepted by ValidCode.
const Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// Bounds for code length. The lower bound rules out single-character codes
// that would collide with reserved paths (e.g. `/api`); the upper bound
// keeps URLs sane.
const (
	DefaultLength = 7
	MinLength     = 4
	MaxLength     = 64
)

// rejectionThreshold = 62*4 = 248. Bytes >= this value are rejected so the
// modulo into the 62-element alphabet stays uniform.
const rejectionThreshold = 62 * 4

// Generator emits codes of a fixed length. It is safe for concurrent use:
// crypto/rand.Reader is goroutine-safe and the struct is otherwise read-only.
type Generator struct {
	length int
}

// ErrLengthOutOfRange wraps the failure mode of NewGenerator so callers can
// match it with errors.Is when reporting config errors.
var ErrLengthOutOfRange = errors.New("shortener: length out of range")

// NewGenerator returns a Generator that emits codes of the given length.
// length must satisfy MinLength <= length <= MaxLength.
func NewGenerator(length int) (*Generator, error) {
	if length < MinLength || length > MaxLength {
		return nil, fmt.Errorf("%w: got %d, want [%d, %d]",
			ErrLengthOutOfRange, length, MinLength, MaxLength)
	}
	return &Generator{length: length}, nil
}

// Length returns the code length this generator emits.
func (g *Generator) Length() int { return g.length }

// Generate returns a freshly minted random code.
func (g *Generator) Generate() (string, error) {
	out := make([]byte, g.length)
	// Read in batches roughly proportional to the rejection rate so we
	// usually fill the result in a single Read.
	buf := make([]byte, g.length*2)
	pos := 0
	for pos < g.length {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("shortener: rand: %w", err)
		}
		for _, b := range buf {
			if int(b) >= rejectionThreshold {
				continue
			}
			out[pos] = Alphabet[int(b)%62]
			pos++
			if pos == g.length {
				break
			}
		}
	}
	return string(out), nil
}

// ValidCode reports whether code is a syntactically acceptable short code:
// non-empty, within length bounds, and made up exclusively of base62 chars.
// Used to validate user-supplied codes and to reject obvious junk in
// path-parameter handlers without hitting the database.
func ValidCode(code string) bool {
	if len(code) < MinLength || len(code) > MaxLength {
		return false
	}
	for i := range len(code) {
		if !isBase62(code[i]) {
			return false
		}
	}
	return true
}

func isBase62(b byte) bool {
	switch {
	case '0' <= b && b <= '9':
		return true
	case 'A' <= b && b <= 'Z':
		return true
	case 'a' <= b && b <= 'z':
		return true
	}
	return false
}
