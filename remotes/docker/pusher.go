package docker

import (
	"context"
	"io"
	"io/ioutil"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/reference"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type dockerPusher struct {
	*dockerBase
	tag string
}

func (p dockerPusher) Push(ctx context.Context, desc ocispec.Descriptor) (content.Writer, error) {
	// TODO: Check status, return already exists

	var (
		ref        string
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
		return nil, err
	}

	req.Header.Set("Accept", strings.Join([]string{desc.MediaType, `*`}, ", "))
	resp, err := p.doRequestWithRetries(ctx, req, nil)
	if err != nil {
		if errors.Cause(err) != ErrInvalidAuthorization {
			return nil, err
		}
		log.G(ctx).WithError(err).Debugf("Unable to check existence, continuing with push")
	} else {
		if resp.StatusCode == http.StatusOK {
			return nil, content.ErrExists
			// TODO: Update status with total 0
		}
		if resp.StatusCode != http.StatusNotFound {
			// TODO: log error
			return nil, errors.Errorf("unexpected response: %s", resp.Status)
		}
	}

	// TODO: Lookup related objects for cross repository push

	if isManifest {
		var putPath string
		refspec := reference.Spec{Locator: p.locator}
		if p.tag != "" {
			putPath = path.Join("manifests", p.tag)
			refspec.Object = p.tag
		} else {
			putPath = path.Join("manifests", desc.Digest.String())
			refspec.Object = "@" + desc.Digest.String()
		}
		ref = refspec.String()

		req, err = http.NewRequest(http.MethodPut, p.url(putPath), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Add("Content-Type", desc.MediaType)
	} else {
		// TODO: Do monolithic upload if size is small

		// Start upload request
		req, err = http.NewRequest(http.MethodPost, p.url("blobs", "uploads")+"/", nil)
		if err != nil {
			return nil, err
		}

		resp, err := p.doRequestWithRetries(ctx, req, nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusAccepted {
			// TODO: log error
			return nil, errors.Errorf("unexpected response: %s", resp.Status)
		}
		ref = resp.Header.Get("Docker-Upload-Uuid")

		location := resp.Header.Get("Location")
		// Support paths without host in location
		if strings.HasPrefix(location, "/") {
			u := p.base
			u.Path = location
			location = u.String()
		}

		req, err = http.NewRequest(http.MethodPut, location, nil)
		if err != nil {
			return nil, err
		}
		q := req.URL.Query()
		q.Add("digest", desc.Digest.String())
		req.URL.RawQuery = q.Encode()

	}
	// TODO: Support chunked upload

	// TODO: Set status

	pr, pw := io.Pipe()
	respC := make(chan *http.Response, 1)

	req.Body = ioutil.NopCloser(pr)
	req.ContentLength = desc.Size

	go func() {
		defer close(respC)
		resp, err = p.doRequest(ctx, req)
		if err != nil {
			pr.CloseWithError(err)
			return
		}
		if resp.StatusCode != http.StatusCreated {
			// TODO: log error
			pr.CloseWithError(errors.Errorf("unexpected response: %s", resp.Status))
		}
		respC <- resp
	}()

	return &pushWriter{
		base:       p.dockerBase,
		ref:        ref,
		pipe:       pw,
		responseC:  respC,
		isManifest: isManifest,

		// TODO: Move this
		status: content.Status{
			Ref:       ref,
			Total:     desc.Size,
			Expected:  desc.Digest,
			StartedAt: time.Now(),
		},
	}, nil
}

type pushWriter struct {
	base *dockerBase
	ref  string

	pipe       *io.PipeWriter
	responseC  <-chan *http.Response
	isManifest bool

	// TODO: Move this to lookup from base
	status content.Status
}

func (pw *pushWriter) Write(p []byte) (n int, err error) {
	n, err = pw.pipe.Write(p)
	pw.status.Offset += int64(n)
	pw.status.UpdatedAt = time.Now()
	return
}

func (pw *pushWriter) Close() error {
	return pw.pipe.Close()
}

func (pw *pushWriter) Status() (content.Status, error) {
	// TODO: Lookup status from base tracker
	return pw.status, nil

}

func (pw *pushWriter) Digest() digest.Digest {
	// TODO: Get rid of this function?
	return pw.status.Expected
}

func (pw *pushWriter) Commit(size int64, expected digest.Digest) error {
	// Check whether read has already thrown an error
	if _, err := pw.pipe.Write([]byte{}); err != nil && err != io.ErrClosedPipe {
		return errors.Wrap(err, "pipe error before commit")
	}

	if err := pw.pipe.Close(); err != nil {
		return err
	}
	// TODO: Update status to determine committing

	// TODO: timeout waiting for response
	resp := <-pw.responseC
	if resp == nil {
		return errors.New("no response")
	}

	// TODO: Get status for size check

	if expected == "" {
		expected = pw.status.Expected
	}

	actual, err := digest.Parse(resp.Header.Get("Docker-Content-Digest"))
	if err != nil {
		return errors.Wrap(err, "invalid content digest in response")
	}

	if actual != expected {
		return errors.Errorf("got digest %s, expected %s", actual, expected)
	}

	return nil
}

func (pw *pushWriter) Truncate(size int64) error {
	// TODO: if blob close request and start new request at offset
	// TODO: always error on manifest
	return errors.New("cannot truncate remote upload")
}
