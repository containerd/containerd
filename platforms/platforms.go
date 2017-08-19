package platforms

import (
	"regexp"
	"runtime"
	"strings"

	"github.com/containerd/containerd/errdefs"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type platformKey struct {
	os      string
	arch    string
	variant string
}

var (
	selectorRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// ParseSelector parses the platform selector syntax into a platform
// declaration.
//
// Platform selectors are in the format <os|arch>[/<arch>[/<variant>]]. The
// minimum required information for a platform selector is the operating system
// or architecture. If there is only a single string (no slashes), the value
// will be matched against the known set of operating systems, then fall
// back to the known set of architectures. The missing component will be
// inferred based on the local environment.
func Parse(selector string) (specs.Platform, error) {
	if strings.Contains(selector, "*") {
		// TODO(stevvooe): need to work out exact wildcard handling
		return specs.Platform{}, errors.Wrapf(errdefs.ErrInvalidArgument, "%q: wildcards not yet supported", selector)
	}

	parts := strings.Split(selector, "/")

	for _, part := range parts {
		if !selectorRe.MatchString(part) {
			return specs.Platform{}, errors.Wrapf(errdefs.ErrInvalidArgument, "%q is an invalid component of %q: platform selector component must match %q", part, selector, selectorRe.String())
		}
	}

	var p specs.Platform
	switch len(parts) {
	case 1:
		// in this case, we will test that the value might be an OS, then look
		// it up. If it is not known, we'll treat it as an architecture. Since
		// we have very little information about the platform here, we are
		// going to be a little more strict if we don't know about the argument
		// value.
		p.OS = normalizeOS(parts[0])
		if isKnownOS(p.OS) {
			// picks a default architecture
			p.Architecture = runtime.GOARCH
			if p.Architecture == "arm" {
				// TODO(stevvooe): Resolve arm variant, if not v6 (default)
			}

			return p, nil
		}

		p.Architecture, p.Variant = normalizeArch(parts[0], "")
		if isKnownArch(p.Architecture) {
			p.OS = runtime.GOOS
			return p, nil
		}

		return specs.Platform{}, errors.Wrapf(errdefs.ErrInvalidArgument, "%q: unknown operating system or architecture", selector)
	case 2:
		// In this case, we treat as a regular os/arch pair. We don't care
		// about whether or not we know of the platform.
		p.OS = normalizeOS(parts[0])
		p.Architecture, p.Variant = normalizeArch(parts[1], "")

		return p, nil
	case 3:
		// we have a fully specified variant, this is rare
		p.OS = normalizeOS(parts[0])
		p.Architecture, p.Variant = normalizeArch(parts[1], parts[2])

		return p, nil
	}

	return specs.Platform{}, errors.Wrapf(errdefs.ErrInvalidArgument, "%q: cannot parse platform selector", selector)
}

func Match(selector string, platform specs.Platform) bool {
	return false
}

// Format returns a string that provides a shortened overview of the platform.
func Format(platform specs.Platform) string {
	if platform.OS == "" {
		return "unknown"
	}

	return joinNotEmpty(platform.OS, platform.Architecture, platform.Variant)
}

func joinNotEmpty(s ...string) string {
	var ss []string
	for _, s := range s {
		if s == "" {
			continue
		}

		ss = append(ss, s)
	}

	return strings.Join(ss, "/")
}

// Normalize validates and translate the platform to the canonical value.
//
// For example, if "Aarch64" is encountered, we change it to "arm64" or if
// "x86_64" is encountered, it becomes "amd64".
func Normalize(platform specs.Platform) specs.Platform {
	platform.OS = normalizeOS(platform.OS)
	platform.Architecture, platform.Variant = normalizeArch(platform.Architecture, platform.Variant)

	// these fields are deprecated, remove them
	platform.OSFeatures = nil
	platform.OSVersion = ""

	return platform
}
