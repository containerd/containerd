package namespaces

import (
	"testing"

	"github.com/pkg/errors"
)

func TestValidNamespaces(t *testing.T) {
	for _, testcase := range []struct {
		input string
		err   error
	}{
		{
			input: "default",
		},
		{
			input: "default-default",
		},
		{
			input: "default--default",
		},
		{
			input: "containerd.io",
		},
		{
			input: "foo.boo",
		},
		{
			input: "swarmkit.docker.io",
		},
		{
			input: "zn--e9.org", // or something like it!
		},
		{
			input: ".foo..foo",
			err:   errNamespaceInvalid,
		},
		{
			input: "foo/foo",
			err:   errNamespaceInvalid,
		},
		{
			input: "foo/..",
			err:   errNamespaceInvalid,
		},
		{
			input: "foo..foo",
			err:   errNamespaceInvalid,
		},
		{
			input: "foo.-boo",
			err:   errNamespaceInvalid,
		},
		{
			input: "-foo.boo",
			err:   errNamespaceInvalid,
		},
		{
			input: "foo.boo-",
			err:   errNamespaceInvalid,
		},
		{
			input: "foo_foo.boo_underscores", // boo-urns?
			err:   errNamespaceInvalid,
		},
	} {
		t.Run(testcase.input, func(t *testing.T) {
			if err := Validate(testcase.input); errors.Cause(err) != testcase.err {
				t.Log(errors.Cause(err), testcase.err)
				if testcase.err == nil {
					t.Fatalf("unexpected error: %v != nil", err)
				} else {
					t.Fatalf("expected error %v to be %v", err, testcase.err)
				}
			}
		})
	}
}
