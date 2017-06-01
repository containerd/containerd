package schema1

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/remotes"
	digest "github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

var (
	mediaTypeManifest = "application/vnd.docker.distribution.manifest.v1+json"
)

// Converter converts schema1 manifests to schema2 on fetch
type Converter struct {
	contentStore content.Store
	fetcher      remotes.Fetcher

	pulledManifest *manifest

	mu      sync.Mutex
	blobMap map[digest.Digest]digest.Digest
}

func NewConverter(contentStore content.Store, fetcher remotes.Fetcher) *Converter {
	return &Converter{
		contentStore: contentStore,
		fetcher:      fetcher,
		blobMap:      map[digest.Digest]digest.Digest{},
	}
}

func (c *Converter) Handle(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	switch desc.MediaType {
	case images.MediaTypeDockerSchema1Manifest:
		if err := c.fetchManifest(ctx, desc); err != nil {
			return nil, err
		}

		m := c.pulledManifest
		if len(m.FSLayers) != len(m.History) {
			return nil, errors.New("invalid schema 1 manifest, history and layer mismatch")
		}
		descs := make([]ocispec.Descriptor, 0, len(c.pulledManifest.FSLayers))

		for i := range m.FSLayers {
			var h v1History
			if err := json.Unmarshal([]byte(m.History[i].V1Compatibility), &h); err != nil {
				return nil, err
			}
			if !h.ThrowAway {
				descs = append(descs, ocispec.Descriptor{
					MediaType: images.MediaTypeDockerSchema2LayerGzip,
					Digest:    c.pulledManifest.FSLayers[i].BlobSum,
				})
			}
		}
		// Reverse
		for i := 0; i <= len(descs)/2; i++ {
			j := len(descs) - i - 1
			if i != j {
				descs[i], descs[j] = descs[j], descs[i]
			}
		}
		return descs, nil
	case images.MediaTypeDockerSchema2LayerGzip:
		if c.pulledManifest == nil {
			return nil, errors.New("manifest required for schema 1 blob pull")
		}
		return nil, c.fetchBlob(ctx, desc)
	default:
		return nil, fmt.Errorf("%v not support for schema 1 manifests", desc.MediaType)
	}
}

func (c *Converter) Convert(ctx context.Context) (ocispec.Descriptor, error) {
	if c.pulledManifest == nil {
		return ocispec.Descriptor{}, errors.New("missing schema 1 manifest for conversion")
	}

	img, err := convertSchema1Manifest(c.pulledManifest, c.blobMap)
	if err != nil {
		return ocispec.Descriptor{}, errors.Wrap(err, "schema 1 conversion failed")
	}
	b, err := json.Marshal(img)
	if err != nil {
		return ocispec.Descriptor{}, errors.Wrap(err, "failed to marshal image")
	}

	config := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.Canonical.FromBytes(b),
		Size:      int64(len(b)),
	}

	ref := remotes.MakeRefKey(ctx, config)
	if err := content.WriteBlob(ctx, c.contentStore, ref, bytes.NewReader(b), config.Size, config.Digest); err != nil {
		return ocispec.Descriptor{}, errors.Wrap(err, "failed to write config")
	}

	layers := make([]ocispec.Descriptor, 0)
	for _, layer := range c.pulledManifest.FSLayers {
		// TODO: Use rootfs mapping!
		info, err := c.contentStore.Info(ctx, layer.BlobSum)
		if err != nil {
			if content.IsNotFound(err) {
				continue
			}
			return ocispec.Descriptor{}, errors.Wrap(err, "failed to get blob info")
		}

		layers = append([]ocispec.Descriptor{{
			MediaType: ocispec.MediaTypeImageLayerGzip,
			Digest:    layer.BlobSum,
			Size:      info.Size,
		}}, layers...)
	}

	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
		Config: config,
		Layers: layers,
	}

	b, err = json.Marshal(manifest)
	if err != nil {
		return ocispec.Descriptor{}, errors.Wrap(err, "failed to marshal image")
	}

	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.Canonical.FromBytes(b),
		Size:      int64(len(b)),
	}

	ref = remotes.MakeRefKey(ctx, desc)
	if err := content.WriteBlob(ctx, c.contentStore, ref, bytes.NewReader(b), desc.Size, desc.Digest); err != nil {
		return ocispec.Descriptor{}, errors.Wrap(err, "failed to write config")
	}

	return desc, nil
}

func (c *Converter) fetchManifest(ctx context.Context, desc ocispec.Descriptor) error {
	log.G(ctx).Debug("fetch schema 1")

	rc, err := c.fetcher.Fetch(ctx, desc)
	if err != nil {
		return err
	}

	b, err := ioutil.ReadAll(rc)
	rc.Close()
	if err != nil {
		return err
	}

	b, err = stripSignature(b)
	if err != nil {
		return err
	}

	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	c.pulledManifest = &m

	return nil
}

func (c *Converter) fetchBlob(ctx context.Context, desc ocispec.Descriptor) error {
	log.G(ctx).Debug("fetch blob")

	ref := remotes.MakeRefKey(ctx, desc)

	var diffID digest.Digest

	cw, err := c.contentStore.Writer(ctx, ref, desc.Size, desc.Digest)
	if err != nil {
		if !content.IsExists(err) {
			return err
		}

		// TODO: Check if blob -> diff id mapping already exists

		r, err := c.contentStore.Reader(ctx, desc.Digest)
		if err != nil {
			return err
		}
		defer r.Close()

		gr, err := gzip.NewReader(r)
		defer gr.Close()

		diffID, err = digest.Canonical.FromReader(gr)
		if err != nil {
			return err
		}
	} else {
		defer cw.Close()

		rc, err := c.fetcher.Fetch(ctx, desc)
		if err != nil {
			return err
		}
		defer rc.Close()

		eg, _ := errgroup.WithContext(ctx)
		pr, pw := io.Pipe()

		eg.Go(func() error {
			gr, err := gzip.NewReader(pr)
			defer gr.Close()

			diffID, err = digest.Canonical.FromReader(gr)
			pr.CloseWithError(err)
			return err
		})

		eg.Go(func() error {
			defer pw.Close()
			return content.Copy(cw, io.TeeReader(rc, pw), desc.Size, desc.Digest)
		})

		if err := eg.Wait(); err != nil {
			return err
		}
	}

	c.mu.Lock()
	c.blobMap[desc.Digest] = diffID
	c.mu.Unlock()

	return nil
}

type fsLayer struct {
	BlobSum digest.Digest `json:"blobSum"`
}

type history struct {
	V1Compatibility string `json:"v1Compatibility"`
}

type manifest struct {
	FSLayers []fsLayer `json:"fsLayers"`
	History  []history `json:"history"`
}

type v1History struct {
	Author          string    `json:"author,omitempty"`
	Created         time.Time `json:"created"`
	Comment         string    `json:"comment,omitempty"`
	ThrowAway       bool      `json:"throwaway,omitempty"`
	ContainerConfig struct {
		Cmd []string `json:"Cmd,omitempty"`
	} `json:"container_config,omitempty"`
}

func convertSchema1Manifest(m *manifest, blobs map[digest.Digest]digest.Digest) (*ocispec.Image, error) {
	if len(m.History) == 0 {
		return nil, errors.New("no history")
	}

	var img ocispec.Image
	if err := json.Unmarshal([]byte(m.History[0].V1Compatibility), &img); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal image from schema 1 history")
	}

	diffIDs := make([]digest.Digest, 0, len(m.History))
	img.History = make([]ocispec.History, len(m.History))
	for i := range m.History {
		var h v1History
		if err := json.Unmarshal([]byte(m.History[i].V1Compatibility), &h); err != nil {
			return nil, errors.Wrap(err, "failed to unmarshal history")
		}

		img.History[len(img.History)-i-1] = ocispec.History{
			Author:     h.Author,
			Comment:    h.Comment,
			Created:    &h.Created,
			CreatedBy:  strings.Join(h.ContainerConfig.Cmd, " "),
			EmptyLayer: h.ThrowAway,
		}

		if !h.ThrowAway {
			diffID, ok := blobs[m.FSLayers[i].BlobSum]
			if !ok {
				return nil, errors.Errorf("no diff id for blob %s", m.FSLayers[i].BlobSum.String())
			}

			diffIDs = append(diffIDs, diffID)
		}

	}

	for i := 0; i <= len(diffIDs)/2; i++ {
		j := len(diffIDs) - i - 1
		if i != j {
			diffIDs[i], diffIDs[j] = diffIDs[j], diffIDs[i]
		}
	}

	img.RootFS = ocispec.RootFS{
		Type:    "layers",
		DiffIDs: diffIDs,
	}

	return &img, nil
}

type signature struct {
	Signatures []jsParsedSignature `json:"signatures"`
}

type jsParsedSignature struct {
	Protected string `json:"protected"`
}

type protectedBlock struct {
	Length int    `json:"formatLength"`
	Tail   string `json:"formatTail"`
}

// joseBase64UrlDecode decodes the given string using the standard base64 url
// decoder but first adds the appropriate number of trailing '=' characters in
// accordance with the jose specification.
// http://tools.ietf.org/html/draft-ietf-jose-json-web-signature-31#section-2
func joseBase64UrlDecode(s string) ([]byte, error) {
	switch len(s) % 4 {
	case 0:
	case 2:
		s += "=="
	case 3:
		s += "="
	default:
		return nil, errors.New("illegal base64url string")
	}
	return base64.URLEncoding.DecodeString(s)
}

func stripSignature(b []byte) ([]byte, error) {
	var sig signature
	if err := json.Unmarshal(b, &sig); err != nil {
		return nil, err
	}
	if len(sig.Signatures) == 0 {
		return nil, errors.New("no signatures")
	}
	pb, err := joseBase64UrlDecode(sig.Signatures[0].Protected)
	if err != nil {
		return nil, errors.Wrapf(err, "could not decode %s", sig.Signatures[0].Protected)
	}

	var protected protectedBlock
	if err := json.Unmarshal(pb, &protected); err != nil {
		return nil, err
	}

	if protected.Length > len(b) {
		return nil, errors.New("invalid protected length block")
	}

	tail, err := joseBase64UrlDecode(protected.Tail)
	if err != nil {
		return nil, errors.Wrap(err, "invalid tail base 64 value")
	}

	return append(b[:protected.Length], tail...), nil
}
