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

package local

import (
	"context"

	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/pkg/transfer"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes"
)

func (ts *localTransferService) push(ctx context.Context, ig transfer.ImageGetter, p transfer.ImagePusher, tops *transfer.TransferOpts) error {
	/*
		// TODO: Platform matching
		if pushCtx.PlatformMatcher == nil {
			if len(pushCtx.Platforms) > 0 {
				var ps []ocispec.Platform
				for _, platform := range pushCtx.Platforms {
					p, err := platforms.Parse(platform)
					if err != nil {
						return fmt.Errorf("invalid platform %s: %w", platform, err)
					}
					ps = append(ps, p)
				}
				pushCtx.PlatformMatcher = platforms.Any(ps...)
			} else {
				pushCtx.PlatformMatcher = platforms.All
			}
		}
	*/

	matcher := platforms.All
	// Filter push

	img, err := ig.Get(ctx, ts.images)
	if err != nil {
		return err
	}

	pusher, err := p.Pusher(ctx, img.Target)
	if err != nil {
		return err
	}

	var wrapper func(images.Handler) images.Handler

	/*
		// TODO: Add handlers
		if len(pushCtx.BaseHandlers) > 0 {
			wrapper = func(h images.Handler) images.Handler {
				h = images.Handlers(append(pushCtx.BaseHandlers, h)...)
				if pushCtx.HandlerWrapper != nil {
					h = pushCtx.HandlerWrapper(h)
				}
				return h
			}
		} else if pushCtx.HandlerWrapper != nil {
			wrapper = pushCtx.HandlerWrapper
		}
	*/

	return remotes.PushContent(ctx, pusher, img.Target, ts.content, ts.limiter, matcher, wrapper)
}
