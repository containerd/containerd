package docker

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"net/http"
	"path"
	"strings"

	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type dockerPusher struct {
	*dockerBase
	tag string
}

func (p dockerPusher) Push(ctx context.Context, desc ocispec.Descriptor, r io.Reader) error {
	var (
		isManifest bool
		existCheck string
	)

	switch desc.MediaType {
	case images.MediaTypeDockerSchema2Manifest, images.MediaTypeDockerSchema2ManifestList,
		ocispec.MediaTypeImageManifest, ocispec.MediaTypeImageIndex:
		isManifest = true
		existCheck = path.Join("manifests", desc.Digest.String())
	default:
		existCheck = path.Join("blobs", desc.Digest.String())
	}

	req, err := http.NewRequest(http.MethodHead, p.url(existCheck), nil)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", strings.Join([]string{desc.MediaType, `*`}, ", "))
	resp, err := p.doRequestWithRetries(ctx, req, nil)
	if err != nil {
		if errors.Cause(err) != ErrInvalidAuthorization {
			return err
		}
		log.G(ctx).WithError(err).Debugf("Unable to check existence, continuing with push")
	} else {
		if resp.StatusCode == http.StatusOK {
			return nil
		}
		if resp.StatusCode != http.StatusNotFound {
			// TODO: log error
			return errors.Errorf("unexpected response: %s", resp.Status)
		}
	}

	// TODO: Lookup related objects for cross repository push

	if isManifest {
		// Read all to use bytes.Reader for using GetBody
		b, err := ioutil.ReadAll(r)
		if err != nil {
			return errors.Wrap(err, "failed to read manifest")
		}
		var putPath string
		if p.tag != "" {
			putPath = path.Join("manifests", p.tag)
		} else {
			putPath = path.Join("manifests", desc.Digest.String())
		}

		req, err := http.NewRequest(http.MethodPut, p.url(putPath), nil)
		if err != nil {
			return err
		}
		req.ContentLength = int64(len(b))
		req.Body = ioutil.NopCloser(bytes.NewReader(b))
		req.GetBody = func() (io.ReadCloser, error) {
			return ioutil.NopCloser(bytes.NewReader(b)), nil
		}
		req.Header.Add("Content-Type", desc.MediaType)

		resp, err := p.doRequestWithRetries(ctx, req, nil)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusCreated {
			// TODO: log error
			return errors.Errorf("unexpected response: %s", resp.Status)
		}
	} else {
		// TODO: Do monolithic upload if size is small

		// TODO: Turn multi-request blob uploader into ingester

		// Start upload request
		req, err := http.NewRequest(http.MethodPost, p.url("blobs", "uploads")+"/", nil)
		if err != nil {
			return err
		}

		resp, err := p.doRequestWithRetries(ctx, req, nil)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusAccepted {
			// TODO: log error
			return errors.Errorf("unexpected response: %s", resp.Status)
		}

		location := resp.Header.Get("Location")
		// Support paths without host in location
		if strings.HasPrefix(location, "/") {
			u := p.base
			u.Path = location
			location = u.String()
		}

		// TODO: Support chunked upload
		req, err = http.NewRequest(http.MethodPut, location, r)
		if err != nil {
			return err
		}
		q := req.URL.Query()
		q.Add("digest", desc.Digest.String())
		req.URL.RawQuery = q.Encode()
		req.ContentLength = desc.Size

		resp, err = p.doRequest(ctx, req)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusCreated {
			// TODO: log error
			return errors.Errorf("unexpected response: %s", resp.Status)
		}

	}

	return nil
}
