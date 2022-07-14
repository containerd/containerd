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

// Package converter provides image converter
package converter

import (
	"context"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/platforms"
	"golang.org/x/sync/singleflight"
)

type convertOpts struct {
	layerConvertFunc ConvertFunc
	docker2oci       bool
	indexConvertFunc ConvertFunc
	platformMC       platforms.MatchComparer
	hooks            ConvertHooks
	sg               *singleflight.Group
}

// Opt is an option for Convert()
type Opt func(*convertOpts) error

// WithLayerConvertFunc specifies the function that converts layers.
func WithLayerConvertFunc(fn ConvertFunc) Opt {
	return func(copts *convertOpts) error {
		copts.layerConvertFunc = fn
		return nil
	}
}

// WithDockerToOCI converts Docker media types into OCI ones.
func WithDockerToOCI(v bool) Opt {
	return func(copts *convertOpts) error {
		copts.docker2oci = true
		return nil
	}
}

// WithPlatform specifies the platform.
// Defaults to all platforms.
func WithPlatform(p platforms.MatchComparer) Opt {
	return func(copts *convertOpts) error {
		copts.platformMC = p
		return nil
	}
}

// WithIndexConvertFunc specifies the function that converts manifests and index (manifest lists).
// Defaults to DefaultIndexConvertFunc.
func WithIndexConvertFunc(fn ConvertFunc) Opt {
	return func(copts *convertOpts) error {
		copts.indexConvertFunc = fn
		return nil
	}
}

// WithConvertHooks specifies a configuration for hook callbacks called during blob conversion.
func WithConvertHooks(hooks ConvertHooks) Opt {
	return func(copts *convertOpts) error {
		copts.hooks = hooks
		return nil
	}
}

// WithSingleflight makes sure that only one conversion execution is
// in-flight for a given child descriptor at a time.
func WithSingleflight() Opt {
	return func(copts *convertOpts) error {
		copts.sg = &singleflight.Group{}
		return nil
	}
}

// Client is implemented by *containerd.Client .
type Client interface {
	WithLease(ctx context.Context, opts ...leases.Opt) (context.Context, func(context.Context) error, error)
	ContentStore() content.Store
	ImageService() images.Store
}

// Converter converts one or more images.
type Converter struct {
	client Client
	copts  convertOpts
}

// New creates a converter instance.
func New(client Client, opts ...Opt) (*Converter, error) {
	var copts convertOpts
	for _, o := range opts {
		if err := o(&copts); err != nil {
			return nil, err
		}
	}
	if copts.indexConvertFunc == nil {
		copts.indexConvertFunc = newDefaultWithOpts(copts).Convert
	}

	return &Converter{
		client: client,
		copts:  copts,
	}, nil
}

// Convert converts an image.
func (c *Converter) Convert(ctx context.Context, dstRef, srcRef string) (*images.Image, error) {
	ctx, done, err := c.client.WithLease(ctx)
	if err != nil {
		return nil, err
	}
	defer done(ctx)

	cs := c.client.ContentStore()
	is := c.client.ImageService()
	srcImg, err := is.Get(ctx, srcRef)
	if err != nil {
		return nil, err
	}

	dstDesc, err := c.copts.indexConvertFunc(ctx, cs, srcImg.Target)
	if err != nil {
		return nil, err
	}

	dstImg := srcImg
	dstImg.Name = dstRef
	if dstDesc != nil {
		dstImg.Target = *dstDesc
	}
	var res images.Image
	if dstRef != srcRef {
		_ = is.Delete(ctx, dstRef)
		res, err = is.Create(ctx, dstImg)
	} else {
		res, err = is.Update(ctx, dstImg)
	}
	return &res, err
}
