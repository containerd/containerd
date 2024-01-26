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

package images

import (
	"flag"
	"fmt"
	"os"
	"sync"

	"github.com/containerd/log"
	"github.com/pelletier/go-toml/v2"
)

var imageListFile = flag.String("image-list", "", "The TOML file containing the non-default images to be used in tests.")

// ImageList holds public image references
type ImageList struct {
	Alpine           string
	BusyBox          string
	Pause            string
	ResourceConsumer string
	VolumeCopyUp     string
	VolumeOwnership  string
	ArgsEscaped      string
	DockerSchema1    string
}

var (
	imageMap  map[int]string
	imageList ImageList
)

var initOnce sync.Once

func initImages(imageListFile string) {
	imageList = ImageList{
		Alpine:           "ghcr.io/containerd/alpine:3.14.0",
		BusyBox:          "ghcr.io/containerd/busybox:1.36",
		Pause:            "registry.k8s.io/pause:3.9",
		ResourceConsumer: "registry.k8s.io/e2e-test-images/resource-consumer:1.10",
		VolumeCopyUp:     "ghcr.io/containerd/volume-copy-up:2.2",
		VolumeOwnership:  "ghcr.io/containerd/volume-ownership:2.1",
		ArgsEscaped:      "cplatpublic.azurecr.io/args-escaped-test-image-ns:1.0",
		DockerSchema1:    "registry.k8s.io/busybox@sha256:4bdd623e848417d96127e16037743f0cd8b528c026e9175e22a84f639eca58ff",
	}

	if imageListFile != "" {
		log.L.Infof("loading image list from file: %s", imageListFile)

		fileContent, err := os.ReadFile(imageListFile)
		if err != nil {
			panic(fmt.Errorf("error reading '%v' file contents: %v", imageList, err))
		}

		err = toml.Unmarshal(fileContent, &imageList)
		if err != nil {
			panic(fmt.Errorf("error unmarshalling '%v' TOML file: %v", imageList, err))
		}
	}

	log.L.Infof("Using the following image list: %+v", imageList)
	imageMap = initImageMap(imageList)
}

const (
	// None is to be used for unset/default images
	None = iota
	// Alpine image
	Alpine
	// BusyBox image
	BusyBox
	// Pause image
	Pause
	// ResourceConsumer image
	ResourceConsumer
	// VolumeCopyUp image
	VolumeCopyUp
	// VolumeOwnership image
	VolumeOwnership
	// ArgsEscaped tests image for ArgsEscaped windows bug
	ArgsEscaped
	// DockerSchema1 image with docker schema 1
	DockerSchema1
)

func initImageMap(imageList ImageList) map[int]string {
	images := map[int]string{}
	images[Alpine] = imageList.Alpine
	images[BusyBox] = imageList.BusyBox
	images[Pause] = imageList.Pause
	images[ResourceConsumer] = imageList.ResourceConsumer
	images[VolumeCopyUp] = imageList.VolumeCopyUp
	images[VolumeOwnership] = imageList.VolumeOwnership
	images[ArgsEscaped] = imageList.ArgsEscaped
	images[DockerSchema1] = imageList.DockerSchema1
	return images
}

// Get returns the fully qualified URI to an image (including version)
func Get(image int) string {
	initOnce.Do(func() {
		initImages(*imageListFile)
	})

	return imageMap[image]
}
