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

package containerd

import (
	"context"
	"testing"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/testsuite"
	"github.com/containerd/containerd/errdefs"
	"github.com/pkg/errors"
)

func newContentStore(ctx context.Context, root string) (context.Context, content.Store, func() error, error) {
	client, err := New(address)
	if err != nil {
		return nil, nil, nil, err
	}
	ctx, releaselease, err := client.WithLease(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	cs := client.ContentStore()

	return ctx, cs, func() error {
		statuses, err := cs.ListStatuses(ctx)
		if err != nil {
			return err
		}
		for _, st := range statuses {
			if err := cs.Abort(ctx, st.Ref); err != nil {
				return errors.Wrapf(err, "failed to abort %s", st.Ref)
			}
		}
		releaselease()
		return cs.Walk(ctx, func(info content.Info) error {
			if err := cs.Delete(ctx, info.Digest); err != nil {
				if errdefs.IsNotFound(err) {
					return nil
				}

				return err
			}
			return nil
		})

	}, nil
}

func TestContentClient(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	testsuite.ContentSuite(t, "ContentClient", newContentStore)
}
