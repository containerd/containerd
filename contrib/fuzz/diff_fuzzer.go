// Copyright 2021 ADA Logics Ltd
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package fuzz

import (
	"context"
	_ "crypto/sha256" // required by go-digest
	"os"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	"github.com/containerd/containerd/v2/core/diff/apply"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/containerd/containerd/v2/plugins/diff/walking"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func FuzzDiffApply(data []byte) int {
	f := fuzz.NewConsumer(data)

	mountsQty, err := f.GetInt()
	if err != nil {
		return 0
	}
	mounts := make([]mount.Mount, 0)
	for i := 0; i < mountsQty%30; i++ {
		m := mount.Mount{}
		err = f.GenerateStruct(&m)
		if err != nil {
			return 0
		}
		mounts = append(mounts, m)
	}
	desc := ocispec.Descriptor{}
	err = f.GenerateStruct(&desc)
	if err != nil {
		return 0
	}
	tmpdir, err := os.MkdirTemp("", "fuzzing-")
	if err != nil {
		return 0
	}
	cs, err := local.NewStore(tmpdir)
	if err != nil {
		return 0
	}
	fsa := apply.NewFileSystemApplier(cs)
	_, _ = fsa.Apply(context.Background(), desc, mounts)
	return 1
}

func FuzzDiffCompare(data []byte) int {
	f := fuzz.NewConsumer(data)

	lowerQty, err := f.GetInt()
	if err != nil {
		return 0
	}
	lower := make([]mount.Mount, 0)
	for i := 0; i < lowerQty%30; i++ {
		m := mount.Mount{}
		err = f.GenerateStruct(&m)
		if err != nil {
			return 0
		}
		lower = append(lower, m)
	}

	upperQty, err := f.GetInt()
	if err != nil {
		return 0
	}
	upper := make([]mount.Mount, 0)
	for i := 0; i < upperQty%30; i++ {
		m := mount.Mount{}
		err = f.GenerateStruct(&m)
		if err != nil {
			return 0
		}
		upper = append(upper, m)
	}

	ctx := context.Background()
	tmpdir, err := os.MkdirTemp("", "fuzzing-")
	if err != nil {
		return 0
	}
	cs, err := local.NewStore(tmpdir)
	if err != nil {
		return 0
	}
	walker := walking.NewWalkingDiff(cs)
	_, _ = walker.Compare(ctx, lower, upper)
	return 1
}
