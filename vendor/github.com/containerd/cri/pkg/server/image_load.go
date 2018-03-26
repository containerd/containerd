/*
Copyright 2017 The Kubernetes Authors.

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

package server

import (
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"

	api "github.com/containerd/cri/pkg/api/v1"
	"github.com/containerd/cri/pkg/containerd/importer"
	imagestore "github.com/containerd/cri/pkg/store/image"
)

// LoadImage loads a image into containerd.
func (c *criService) LoadImage(ctx context.Context, r *api.LoadImageRequest) (*api.LoadImageResponse, error) {
	path := r.GetFilePath()
	if !filepath.IsAbs(path) {
		return nil, errors.Errorf("path %q is not an absolute path", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open file")
	}
	repoTags, err := importer.Import(ctx, c.client, f)
	if err != nil {
		return nil, errors.Wrap(err, "failed to import image")
	}
	for _, repoTag := range repoTags {
		image, err := c.client.GetImage(ctx, repoTag)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get image %q", repoTag)
		}
		if err := image.Unpack(ctx, c.config.ContainerdConfig.Snapshotter); err != nil {
			logrus.WithError(err).Warnf("Failed to unpack image %q", repoTag)
			// Do not fail image importing. Unpack will be retried when container creation.
		}
		info, err := getImageInfo(ctx, image)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get image %q info", repoTag)
		}
		id := info.id

		if err := c.createImageReference(ctx, id, image.Target()); err != nil {
			return nil, errors.Wrapf(err, "failed to create image reference %q", id)
		}

		img := imagestore.Image{
			ID:        id,
			RepoTags:  []string{repoTag},
			ChainID:   info.chainID.String(),
			Size:      info.size,
			ImageSpec: info.imagespec,
			Image:     image,
		}

		if err := c.imageStore.Add(img); err != nil {
			return nil, errors.Wrapf(err, "failed to add image %q into store", id)
		}
		logrus.Debugf("Imported image with id %q, repo tag %q", id, repoTag)
	}
	return &api.LoadImageResponse{Images: repoTags}, nil
}
