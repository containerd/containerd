package docker

import (
	"context"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type dockerFetcher struct {
	*dockerBase
}

func (r dockerFetcher) Fetch(ctx context.Context, desc ocispec.Descriptor) (io.ReadCloser, error) {
	ctx = log.WithLogger(ctx, log.G(ctx).WithFields(
		logrus.Fields{
			"base":   r.base.String(),
			"digest": desc.Digest,
		},
	))

	paths, err := getV2URLPaths(desc)
	if err != nil {
		return nil, err
	}

	for _, path := range paths {
		u := r.url(path)

		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Set("Accept", strings.Join([]string{desc.MediaType, `*`}, ", "))
		resp, err := r.doRequestWithRetries(ctx, req, nil)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode > 299 {
			if resp.StatusCode == http.StatusNotFound {
				continue // try one of the other urls.
			}
			resp.Body.Close()
			return nil, errors.Errorf("unexpected status code %v: %v", u, resp.Status)
		}

		return resp.Body, nil
	}

	return nil, errors.New("not found")
}

// getV2URLPaths generates the candidate urls paths for the object based on the
// set of hints and the provided object id. URLs are returned in the order of
// most to least likely succeed.
func getV2URLPaths(desc ocispec.Descriptor) ([]string, error) {
	var urls []string

	switch desc.MediaType {
	case images.MediaTypeDockerSchema2Manifest, images.MediaTypeDockerSchema2ManifestList,
		images.MediaTypeDockerSchema1Manifest,
		ocispec.MediaTypeImageManifest, ocispec.MediaTypeImageIndex:
		urls = append(urls, path.Join("manifests", desc.Digest.String()))
	}

	// always fallback to attempting to get the object out of the blobs store.
	urls = append(urls, path.Join("blobs", desc.Digest.String()))

	return urls, nil
}
