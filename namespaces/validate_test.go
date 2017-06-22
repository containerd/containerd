package namespaces

import (
	"testing"

	"github.com/pkg/errors"
)

func TestValidNamespaces(t *testing.T) {
	for _, testcase := range []struct {
		name  string
		input string
		err   error
	}{
		{
			name:  "Default",
			input: "default",
		},
		{
			name:  "Hyphen",
			input: "default-default",
		},
		{
			name:  "DoubleHyphen",
			input: "default--default",
		},
		{
			name:  "containerD",
			input: "containerd.io",
		},
		{
			name:  "SwarmKit",
			input: "swarmkit.docker.io",
		},
		{
			name:  "Punycode",
			input: "zn--e9.org", // or something like it!
		},
		{
			name:  "LeadingPeriod",
			input: ".foo..foo",
			err:   errNamespaceInvalid,
		},
		{
			name:  "Path",
			input: "foo/foo",
			err:   errNamespaceInvalid,
		},
		{
			name:  "ParentDir",
			input: "foo/..",
			err:   errNamespaceInvalid,
		},
		{
			name:  "RepeatedPeriod",
			input: "foo..foo",
			err:   errNamespaceInvalid,
		},
		{
			name:  "OutOfPlaceHyphenEmbedded",
			input: "foo.-boo",
			err:   errNamespaceInvalid,
		},
		{
			name:  "OutOfPlaceHyphen",
			input: "-foo.boo",
			err:   errNamespaceInvalid,
		},
		{
			name:  "OutOfPlaceHyphenEnd",
			input: "foo.boo",
			err:   errNamespaceInvalid,
		},
		{
			name:  "Underscores",
			input: "foo_foo.boo_underscores", // boo-urns?
			err:   errNamespaceInvalid,
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			if err := Validate(testcase.input); err != nil {
				if errors.Cause(err) != testcase.err {
					if testcase.err == nil {
						t.Fatalf("unexpected error: %v != nil", err)
					} else {
						t.Fatalf("expected error %v to be %v", err, testcase.err)
					}
				} else {
					t.Logf("invalid %q detected as invalid: %v", testcase.input, err)
					return
				}

				t.Logf("%q is a valid namespace", testcase.input)
			}
		})
	}
}
