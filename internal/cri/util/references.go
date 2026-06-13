/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package util

import (
	"strings"

	reference "github.com/distribution/reference"
	imagedigest "github.com/opencontainers/go-digest"
)

// hasExplicitRegistry checks if the reference has an explicit registry domain.
func hasExplicitRegistry(ref string) bool {
	parsed, err := reference.ParseAnyReference(ref)
	if err != nil {
		return false
	}
	if named, ok := parsed.(reference.Named); ok {
		domain := reference.Domain(named)
		if domain == "" {
			return false
		}
		// If the extracted domain is not present in the original reference,
		// it means the library auto-filled it (e.g., "busybox" -> "docker.io/...").
		return strings.Contains(ref, domain)
	}
	return false
}

// ParseImageReferences parses a list of arbitrary image references and returns
// the repotags and repodigests. It filters out references without an explicit registry.
func ParseImageReferences(refs []string) ([]string, []string) {
	var tags, digests []string
	for _, ref := range refs {
		// CRI should only see images with fully-qualified registries.
		// We intercept the raw string here before any normalization happens,
		// to prevent "short tags" (like busybox:fixed) from being shown with
		// an automatic "docker.io" prefix.
		if !hasExplicitRegistry(ref) {
			continue
		}
		parsed, err := reference.ParseAnyReference(ref)
		if err != nil {
			continue
		}
		if _, ok := parsed.(reference.Canonical); ok {
			digests = append(digests, parsed.String())
		} else if _, ok := parsed.(reference.Tagged); ok {
			tags = append(tags, parsed.String())
		}
	}
	return tags, digests
}

// GetRepoDigestAndTag returns image repoDigest and repoTag of the named image reference.
func GetRepoDigestAndTag(namedRef reference.Named, digest imagedigest.Digest) (string, string) {
	var repoTag, repoDigest string
	if _, ok := namedRef.(reference.NamedTagged); ok {
		repoTag = namedRef.String()
	}
	if _, ok := namedRef.(reference.Canonical); ok {
		repoDigest = namedRef.String()
	} else {
		repoDigest = namedRef.Name() + "@" + digest.String()
	}
	return repoDigest, repoTag
}
