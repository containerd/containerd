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
	"google.golang.org/grpc"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const defaultStreamBatchSize = 5000

// StreamImages streams all existing images.
func (c *GRPCCRIImageService) StreamImages(r *runtime.StreamImagesRequest, s grpc.ServerStreamingServer[runtime.StreamImagesResponse]) error {
	ctx := s.Context()
	imagesInStore := c.imageStore.List()

	var images []*runtime.Image
	for _, image := range imagesInStore {
		images = append(images, toCRIImage(image))
	}

	for i := 0; i < len(images); i += defaultStreamBatchSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(i+defaultStreamBatchSize, len(images))
		if err := s.Send(&runtime.StreamImagesResponse{Images: images[i:end]}); err != nil {
			return err
		}
	}
	return nil
}
