package identifiers

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-")

func TestValidIdentifiers(t *testing.T) {
	for _, input := range []string{
		"default",
		"Default",
		t.Name(),
		"default-default",
		"default--default",
		"containerd.io",
		"foo.boo",
		"swarmkit.docker.io",
		"zn--e9.org", // or something like it!
		fmt.Sprintf("a0%s", randStringRunes(maxSizeLabel-2)), // length maxSizeLabel of label
		fmt.Sprintf("a0%s.a0%s.a0%s.a0%s", randStringRunes(maxSizeLabel-2), randStringRunes(maxSizeLabel-2),
			randStringRunes(maxSizeLabel-2), randStringRunes(maxSizeLabel-2)), // length maxSizeName of name
	} {
		t.Run(input, func(t *testing.T) {
			if err := Validate(input); err != nil {
				t.Fatalf("unexpected error: %v != nil", err)
			}
		})
	}
}

func TestInvalidIdentifiers(t *testing.T) {
	for _, input := range []string{
		".foo..foo",
		"foo.boo.",
		"foo.foo",
		"foo/foo",
		"foo_boo",
		"foo/..",
		"foo..foo",
		"foo.-boo",
		"-foo.boo",
		"foo.boo-",
		"foo_foo.boo_underscores",                            // boo-urns?
		fmt.Sprintf("ab%s", randStringRunes(maxSizeLabel-1)), // length maxSizeLabel+1 of label
		fmt.Sprintf("a0%s.a0%s.a0%s.a0%s.io", randStringRunes(maxSizeLabel-2), randStringRunes(maxSizeLabel-2),
			randStringRunes(maxSizeLabel-2), randStringRunes(maxSizeLabel-2)), // length maxSizeName+3 of name
	} {

		t.Run(input, func(t *testing.T) {
			if err := Validate(input); err == nil {
				t.Fatal("expected invalid error")
			} else if !IsInvalid(err) {
				t.Fatal("error should be an invalid identifier error")
			}
		})
	}
}

func randStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func init() {
	rand.Seed(time.Now().UnixNano())
}
