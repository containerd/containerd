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
	"fmt"
	"golang.org/x/net/context"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	api "github.com/containerd/cri/pkg/api/v1"
	"github.com/containerd/cri/pkg/containerd/importer"
	imagestore "github.com/containerd/cri/pkg/store/image"
)

// LoadImage loads a image into containerd.
func (c *criContainerdService) LoadImage(ctx context.Context, r *api.LoadImageRequest) (*api.LoadImageResponse, error) {
	path := r.GetFilePath()
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("path %q is not an absolute path", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %v", err)
	}
	repoTags, err := importer.Import(ctx, c.client, f)
	if err != nil {
		return nil, fmt.Errorf("failed to import image: %v", err)
	}
	for _, repoTag := range repoTags {
		image, err := c.client.GetImage(ctx, repoTag)
		if err != nil {
			return nil, fmt.Errorf("failed to get image %q: %v", repoTag, err)
		}
		if err := image.Unpack(ctx, c.config.ContainerdConfig.Snapshotter); err != nil {
			logrus.WithError(err).Warnf("Failed to unpack image %q", repoTag)
			// Do not fail image importing. Unpack will be retried when container creation.
		}
		info, err := getImageInfo(ctx, image)
		if err != nil {
			return nil, fmt.Errorf("failed to get image %q info: %v", repoTag, err)
		}
		id := info.id

		if err := c.createImageReference(ctx, id, image.Target()); err != nil {
			return nil, fmt.Errorf("failed to create image reference %q: %v", id, err)
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
			return nil, fmt.Errorf("failed to add image %q into store: %v", id, err)
		}
		logrus.Debugf("Imported image with id %q, repo tag %q", id, repoTag)
	}
	return &api.LoadImageResponse{Images: repoTags}, nil
}
