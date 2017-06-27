package identifiers

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-")

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
		"foo/foo",
		"foo/..",
		"foo..foo",
		"foo.-boo",
		"-foo.boo",
		"foo.boo-",
		"foo_foo.boo_underscores", // boo-urns?
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

func TestValidContainerId(t *testing.T) {
	for _, input := range []string{
		"default",
		"Default",
		"default-default",
		"default--default",
		"containerd.io",
		"foo.boo..",
		"foo_-.boo",
		"foo_-.",
		"swarmkit.docker.io",
		"zn--e9.org",
		fmt.Sprintf("a%s", randStringRunes(127)),
	} {
		t.Run(input, func(t *testing.T) {
			if err := ValidateContainerId(input); err != nil {
				t.Fatalf("unexpected error: %v != nil", err)
			}
		})
	}
}

func TestInvalidContainerId(t *testing.T) {
	for _, input := range []string{
		"",
		".foo..foo",
		"foo/foo",
		"foo/..",
		"-foo.boo",
		"_foo-boo",
		fmt.Sprintf("a%s", randStringRunes(128)),
	} {

		t.Run(input, func(t *testing.T) {
			if err := ValidateContainerId(input); err == nil {
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
