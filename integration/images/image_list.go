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

	"github.com/pelletier/go-toml"
	"github.com/sirupsen/logrus"
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
		Pause:            "registry.k8s.io/pause:3.8",
		ResourceConsumer: "registry.k8s.io/e2e-test-images/resource-consumer:1.10",
		VolumeCopyUp:     "ghcr.io/containerd/volume-copy-up:2.1",
		VolumeOwnership:  "ghcr.io/containerd/volume-ownership:2.1",
	}

	if imageListFile != "" {
		logrus.Infof("loading image list from file: %s", imageListFile)

		fileContent, err := os.ReadFile(imageListFile)
		if err != nil {
			panic(fmt.Errorf("error reading '%v' file contents: %v", imageList, err))
		}

		err = toml.Unmarshal(fileContent, &imageList)
		if err != nil {
			panic(fmt.Errorf("error unmarshalling '%v' TOML file: %v", imageList, err))
		}
	}

	logrus.Infof("Using the following image list: %+v", imageList)
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
)

func initImageMap(imageList ImageList) map[int]string {
	images := map[int]string{}
	images[Alpine] = imageList.Alpine
	images[BusyBox] = imageList.BusyBox
	images[Pause] = imageList.Pause
	images[ResourceConsumer] = imageList.ResourceConsumer
	images[VolumeCopyUp] = imageList.VolumeCopyUp
	images[VolumeOwnership] = imageList.VolumeOwnership
	return images
}

// Get returns the fully qualified URI to an image (including version)
func Get(image int) string {
	initOnce.Do(func() {
		initImages(*imageListFile)
	})

	return imageMap[image]
}
