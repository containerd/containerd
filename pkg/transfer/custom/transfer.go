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

// Package custom implements a transfer.Transferrer that intercepts image pull
// operations and delegates them to a user-supplied black-box fetch function.
// All other transfer operations (push, import, export, tag) fall through to the
// stock "local" plugin via errdefs.ErrNotImplemented.
package custom

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/transfer"
	"github.com/containerd/containerd/snapshots"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// FetchFunc is the black-box function the user supplies.
// It receives the image reference (e.g. "docker.io/library/nginx:latest")
// and a target directory that is already a tmpfs mount with huge=advise
// and noatime. The function must populate targetDir with the desired rootfs
// content and return nil on success.
type FetchFunc func(ctx context.Context, imageRef string, targetDir string) error

// CustomTransferService intercepts pull operations and hands them to a
// user-defined FetchFunc, committing the result into a snapshotter.
type CustomTransferService struct {
	leases      leases.Manager
	images      images.Store
	snapshotter snapshots.Snapshotter
	snName      string // snapshotter name for GC label
	fetch       FetchFunc
}

// NewTransferService creates a custom TransferService.
func NewTransferService(lm leases.Manager, is images.Store, sn snapshots.Snapshotter, snapshotterName string, fn FetchFunc) transfer.Transferrer {
	return &CustomTransferService{
		leases:      lm,
		images:      is,
		snapshotter: sn,
		snName:      snapshotterName,
		fetch:       fn,
	}
}

// Transfer handles ImageFetcher→ImageStorer (pull) and returns
// ErrNotImplemented for everything else so the stock plugin can handle it.
func (ts *CustomTransferService) Transfer(ctx context.Context, src interface{}, dst interface{}, opts ...transfer.Opt) error {
	topts := &transfer.Config{}
	for _, opt := range opts {
		opt(topts)
	}

	s, ok := src.(transfer.ImageFetcher)
	if !ok {
		return fmt.Errorf("custom transfer: unsupported source %T: %w", src, errdefs.ErrNotImplemented)
	}
	d, ok := dst.(transfer.ImageStorer)
	if !ok {
		return fmt.Errorf("custom transfer: unsupported destination %T: %w", dst, errdefs.ErrNotImplemented)
	}

	return ts.pull(ctx, s, d, topts)
}

func (ts *CustomTransferService) pull(ctx context.Context, src transfer.ImageFetcher, dst transfer.ImageStorer, topts *transfer.Config) error {
	// 1. Acquire a lease to prevent GC during our work.
	ctx, done, err := ts.withLease(ctx)
	if err != nil {
		return err
	}
	defer done(ctx)

	// 2. Resolve the image reference.
	name, _, err := src.Resolve(ctx)
	if err != nil {
		return fmt.Errorf("custom transfer: failed to resolve image: %w", err)
	}

	if topts.Progress != nil {
		topts.Progress(transfer.Progress{
			Event: fmt.Sprintf("custom-pull: resolved %s", name),
			Name:  name,
		})
	}

	// 3. Commit the rootfs tree into the snapshotter.
	chainID, err := ts.commitTree(ctx, name)
	if err != nil {
		return err
	}

	if topts.Progress != nil {
		topts.Progress(transfer.Progress{
			Event: "custom-pull: snapshot committed",
			Name:  name,
		})
	}

	// 4. Record a synthetic image in the image store so containerd knows
	//    about this image and links it to the committed snapshot via a
	//    GC reference label.
	syntheticDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromString(name + chainID),
		Size:      0,
		Annotations: map[string]string{
			// This label tells the GC that this image references a
			// snapshot in our snapshotter — preventing collection.
			fmt.Sprintf("containerd.io/gc.ref.snapshot.%s", ts.snName): chainID,
		},
	}

	imgs, err := dst.Store(ctx, syntheticDesc, ts.images)
	if err != nil {
		return fmt.Errorf("custom transfer: failed to store image: %w", err)
	}

	if topts.Progress != nil {
		for _, img := range imgs {
			topts.Progress(transfer.Progress{
				Event: "saved",
				Name:  img.Name,
			})
		}
		topts.Progress(transfer.Progress{
			Event: fmt.Sprintf("custom-pull: completed for %s", name),
		})
	}

	return nil
}

// commitTree calls the black-box FetchFunc to populate a snapshot, then
// commits it. The snapshot key (chainID) is derived from the image name.
func (ts *CustomTransferService) commitTree(ctx context.Context, imageRef string) (string, error) {
	chainID := digest.FromString(imageRef).String()
	activeKey := fmt.Sprintf("custom-%s-%d", chainID, time.Now().UnixNano())

	// Check if already committed from a previous pull.
	if _, err := ts.snapshotter.Stat(ctx, chainID); err == nil {
		log.G(ctx).Debugf("custom transfer: snapshot %s already exists, skipping", chainID)
		return chainID, nil
	}

	// Prepare an active snapshot — the custom snapshotter returns tmpfs mounts.
	mounts, err := ts.snapshotter.Prepare(ctx, activeKey, "", snapshots.WithLabels(map[string]string{
		"containerd.io/snapshot.ref": chainID,
	}))
	if err != nil {
		if errdefs.IsAlreadyExists(err) {
			return chainID, nil
		}
		return "", fmt.Errorf("custom transfer: prepare snapshot: %w", err)
	}

	// Mount and call the black-box.
	// The custom snapshotter already created a tmpfs; mount.All bind-mounts
	// it at a temp target so we can hand a path to the FetchFunc.
	// Since there's exactly one bind mount with Source = the tmpfs dir,
	// we can hand Source directly to the FetchFunc to avoid a redundant
	// bind mount on top.
	if len(mounts) != 1 {
		ts.snapshotter.Remove(ctx, activeKey)
		return "", fmt.Errorf("custom transfer: expected 1 mount, got %d", len(mounts))
	}
	targetDir := mounts[0].Source

	if err := ts.fetch(ctx, imageRef, targetDir); err != nil {
		ts.snapshotter.Remove(ctx, activeKey)
		return "", fmt.Errorf("custom transfer: fetch failed for %s: %w", imageRef, err)
	}

	// Commit the active snapshot.
	if err := ts.snapshotter.Commit(ctx, chainID, activeKey); err != nil {
		if !errdefs.IsAlreadyExists(err) {
			ts.snapshotter.Remove(ctx, activeKey)
			return "", fmt.Errorf("custom transfer: commit snapshot: %w", err)
		}
	}

	return chainID, nil
}

// withLease attaches a lease on the context to prevent GC of in-flight work.
func (ts *CustomTransferService) withLease(ctx context.Context, opts ...leases.Opt) (context.Context, func(context.Context) error, error) {
	nop := func(context.Context) error { return nil }

	if _, ok := leases.FromContext(ctx); ok {
		return ctx, nop, nil
	}

	if len(opts) == 0 {
		opts = []leases.Opt{
			leases.WithRandomID(),
			leases.WithExpiration(24 * time.Hour),
		}
	}

	l, err := ts.leases.Create(ctx, opts...)
	if err != nil {
		return ctx, nop, err
	}

	ctx = leases.WithLease(ctx, l.ID)
	return ctx, func(ctx context.Context) error {
		return ts.leases.Delete(ctx, l)
	}, nil
}
