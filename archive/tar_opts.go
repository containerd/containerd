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

package archive

import (
	"archive/tar"
	"context"
	"io"
	"time"

	"github.com/containerd/containerd/mount"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// ApplyOptions provides additional options for an Apply operation
type ApplyOptions struct {
	Filter          Filter          // Filter tar headers
	ConvertWhiteout ConvertWhiteout // Convert whiteout files
	Parents         []string        // Parent directories to handle inherited attributes without CoW
	NoSameOwner     bool            // NoSameOwner will not attempt to preserve the owner specified in the tar archive.

	UsePreunpackedLayer bool               //Use for preunpacked
	Desc                ocispec.Descriptor //Used for preunpacked
	Mounts              []mount.Mount      //Used for preunpacked

	applyFunc func(context.Context, string, io.Reader, ApplyOptions) (int64, error)
}

// ApplyOpt allows setting mutable archive apply properties on creation
type ApplyOpt func(options *ApplyOptions) error

// Filter specific files from the archive
type Filter func(*tar.Header) (bool, error)

// ConvertWhiteout converts whiteout files from the archive
type ConvertWhiteout func(*tar.Header, string) (bool, error)

// all allows all files
func all(_ *tar.Header) (bool, error) {
	return true, nil
}

// WithFilter uses the filter to select which files are to be extracted.
func WithFilter(f Filter) ApplyOpt {
	return func(options *ApplyOptions) error {
		options.Filter = f
		return nil
	}
}

// WithConvertWhiteout uses the convert function to convert the whiteout files.
func WithConvertWhiteout(c ConvertWhiteout) ApplyOpt {
	return func(options *ApplyOptions) error {
		options.ConvertWhiteout = c
		return nil
	}
}

// WithNoSameOwner is same as '--no-same-owner` in 'tar' command.
// It'll skip attempt to preserve the owner specified in the tar archive.
func WithNoSameOwner() ApplyOpt {
	return func(options *ApplyOptions) error {
		options.NoSameOwner = true
		return nil
	}
}

// WithPreunpack uses preunpacked image layer
func WithUsePreunpackedLayer() ApplyOpt {
	return func(options *ApplyOptions) error {
		options.UsePreunpackedLayer = true
		return nil
	}
}

// WithParents provides parent directories for resolving inherited attributes
// directory from the filesystem.
// Inherited attributes are searched from first to last, making the first
// element in the list the most immediate parent directory.
// NOTE: When applying to a filesystem which supports CoW, file attributes
// should be inherited by the filesystem.
func WithParents(p []string) ApplyOpt {
	return func(options *ApplyOptions) error {
		options.Parents = p
		return nil
	}
}

// WithDesc provides parent directories for resolving inherited attributes
// directory from the filesystem.
// Inherited attributes are searched from first to last, making the first
// element in the list the most immediate parent directory.
// NOTE: When applying to a filesystem which supports CoW, file attributes
// should be inherited by the filesystem.
func WithDesc(desc ocispec.Descriptor) ApplyOpt {
	return func(options *ApplyOptions) error {
		options.Desc = desc
		return nil
	}
}

// WithMounts provides parent directories for resolving inherited attributes
// directory from the filesystem.
// Inherited attributes are searched from first to last, making the first
// element in the list the most immediate parent directory.
// NOTE: When applying to a filesystem which supports CoW, file attributes
// should be inherited by the filesystem.
func WithMounts(mounts []mount.Mount) ApplyOpt {
	return func(options *ApplyOptions) error {
		options.Mounts = mounts
		return nil
	}
}

// WriteDiffOptions provides additional options for a WriteDiff operation
type WriteDiffOptions struct {
	ParentLayers []string // Windows needs the full list of parent layers

	writeDiffFunc func(context.Context, io.Writer, string, string, WriteDiffOptions) error

	// SourceDateEpoch specifies the following timestamps to provide control for reproducibility.
	//   - The upper bound timestamp of the diff contents
	//   - The timestamp of the whiteouts
	//
	// See also https://reproducible-builds.org/docs/source-date-epoch/ .
	SourceDateEpoch *time.Time
}

// WriteDiffOpt allows setting mutable archive write properties on creation
type WriteDiffOpt func(options *WriteDiffOptions) error

// WithSourceDateEpoch specifies the SOURCE_DATE_EPOCH without touching the env vars.
func WithSourceDateEpoch(tm *time.Time) WriteDiffOpt {
	return func(options *WriteDiffOptions) error {
		options.SourceDateEpoch = tm
		return nil
	}
}
