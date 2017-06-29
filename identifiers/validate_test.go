package identifiers

import (
	"strings"
	"testing"

	"github.com/containerd/containerd/errdefs"
)

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
		"0912341234",
		"task.0.0123456789",
		strings.Repeat("a", maxLength),
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
		strings.Repeat("a", maxLength+1),
	} {

		t.Run(input, func(t *testing.T) {
			if err := Validate(input); err == nil {
				t.Fatal("expected invalid error")
			} else if !errdefs.IsInvalidArgument(err) {
				t.Fatal("error should be an invalid identifier error")
			}
		})
	}
}
