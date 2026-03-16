package platforms

import (
	"reflect"
	"testing"

	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestParseWithOSFeatures(t *testing.T) {
	t.Parallel()

	platform, err := Parse("linux(+erofs)/amd64")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	expected := specs.Platform{
		OS:           "linux",
		Architecture: "amd64",
		OSFeatures:   []string{"erofs"},
	}
	if !reflect.DeepEqual(platform, expected) {
		t.Fatalf("Parse() = %#v, want %#v", platform, expected)
	}
}

func TestParseWithOSVersionAndFeatures(t *testing.T) {
	t.Parallel()

	platform, err := Parse("windows(10.0.17763+win32k+gpu)/amd64")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	expected := specs.Platform{
		OS:           "windows",
		Architecture: "amd64",
		OSVersion:    "10.0.17763",
		OSFeatures:   []string{"gpu", "win32k"},
	}
	if !reflect.DeepEqual(platform, expected) {
		t.Fatalf("Parse() = %#v, want %#v", platform, expected)
	}
}

func TestFormatAllIncludesSortedOSFeatures(t *testing.T) {
	t.Parallel()

	platform := specs.Platform{
		OS:           "linux",
		Architecture: "amd64",
		OSFeatures:   []string{"selinux", "erofs", "selinux"},
	}

	if got, want := FormatAll(platform), "linux(+erofs+selinux)/amd64"; got != want {
		t.Fatalf("FormatAll() = %q, want %q", got, want)
	}
}

func TestOnlyStrictMatchesOSFeatureSubset(t *testing.T) {
	t.Parallel()

	matcher := OnlyStrict(specs.Platform{
		OS:           "linux",
		Architecture: "amd64",
		OSFeatures:   []string{"selinux", "erofs"},
	})

	testCases := []struct {
		name     string
		platform specs.Platform
		match    bool
	}{
		{
			name: "no feature requirement",
			platform: specs.Platform{
				OS:           "linux",
				Architecture: "amd64",
			},
			match: true,
		},
		{
			name: "supported feature subset",
			platform: specs.Platform{
				OS:           "linux",
				Architecture: "amd64",
				OSFeatures:   []string{"erofs"},
			},
			match: true,
		},
		{
			name: "unsupported feature",
			platform: specs.Platform{
				OS:           "linux",
				Architecture: "amd64",
				OSFeatures:   []string{"nydus"},
			},
			match: false,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := matcher.Match(tc.platform); got != tc.match {
				t.Fatalf("Match() = %v, want %v", got, tc.match)
			}
		})
	}
}

func TestOnlyStrictLessPrefersMoreSpecificOSFeatures(t *testing.T) {
	t.Parallel()

	matcher := OnlyStrict(specs.Platform{
		OS:           "linux",
		Architecture: "amd64",
		OSFeatures:   []string{"erofs"},
	})

	plain := specs.Platform{
		OS:           "linux",
		Architecture: "amd64",
	}
	erofs := specs.Platform{
		OS:           "linux",
		Architecture: "amd64",
		OSFeatures:   []string{"erofs"},
	}

	if !matcher.Less(erofs, plain) {
		t.Fatal("Less(erofs, plain) = false, want true")
	}
	if matcher.Less(plain, erofs) {
		t.Fatal("Less(plain, erofs) = true, want false")
	}
}
