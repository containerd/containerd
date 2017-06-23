package identifiers

import (
	"testing"
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
