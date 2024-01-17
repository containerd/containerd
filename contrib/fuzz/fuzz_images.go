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
	"os"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/platforms"
	"github.com/containerd/containerd/v2/plugins/content/local"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func FuzzImagesCheck(data []byte) int {
	f := fuzz.NewConsumer(data)
	desc := ocispec.Descriptor{}
	err := f.GenerateStruct(&desc)
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
	_, _, _, _, _ = images.Check(context.Background(), cs, desc, platforms.Default())
	return 1
}
