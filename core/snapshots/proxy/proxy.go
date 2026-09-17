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

package proxy

import (
	"context"
	"io"
	"time"

	snapshotsapi "github.com/containerd/containerd/api/services/snapshots/v1"
	"github.com/containerd/errdefs/pkg/errgrpc"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	protobuftypes "github.com/containerd/containerd/v2/pkg/protobuf/types"
)

// Opt configures a proxy snapshotter.
type Opt func(*proxySnapshotter)

// WithDefaultTimeout bounds each call to the proxy snapshotter when the
// caller's context carries no deadline of its own. A non-positive duration
// leaves calls unbounded.
func WithDefaultTimeout(d time.Duration) Opt {
	return func(p *proxySnapshotter) {
		p.defaultTimeout = d
	}
}

// NewSnapshotter returns a new Snapshotter which communicates over a GRPC
// connection using the containerd snapshot GRPC API.
func NewSnapshotter(client snapshotsapi.SnapshotsClient, snapshotterName string) snapshots.Snapshotter {
	return NewSnapshotterWithOpts(client, snapshotterName)
}

// NewSnapshotterWithOpts is NewSnapshotter with options applied.
func NewSnapshotterWithOpts(client snapshotsapi.SnapshotsClient, snapshotterName string, opts ...Opt) snapshots.Snapshotter {
	p := &proxySnapshotter{
		client:          client,
		snapshotterName: snapshotterName,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

type proxySnapshotter struct {
	client          snapshotsapi.SnapshotsClient
	snapshotterName string
	defaultTimeout  time.Duration
}

func (p *proxySnapshotter) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if p.defaultTimeout <= 0 {
		return ctx, func() {}
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, p.defaultTimeout)
}

func (p *proxySnapshotter) Stat(ctx context.Context, key string) (snapshots.Info, error) {
	ctx, cancel := p.withTimeout(ctx)
	defer cancel()

	resp, err := p.client.Stat(ctx,
		&snapshotsapi.StatSnapshotRequest{
			Snapshotter: p.snapshotterName,
			Key:         key,
		})
	if err != nil {
		return snapshots.Info{}, errgrpc.ToNative(err)
	}
	return InfoFromProto(resp.Info), nil
}

func (p *proxySnapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error) {
	ctx, cancel := p.withTimeout(ctx)
	defer cancel()

	resp, err := p.client.Update(ctx,
		&snapshotsapi.UpdateSnapshotRequest{
			Snapshotter: p.snapshotterName,
			Info:        InfoToProto(info),
			UpdateMask: &protobuftypes.FieldMask{
				Paths: fieldpaths,
			},
		})
	if err != nil {
		return snapshots.Info{}, errgrpc.ToNative(err)
	}
	return InfoFromProto(resp.Info), nil
}

func (p *proxySnapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	ctx, cancel := p.withTimeout(ctx)
	defer cancel()

	resp, err := p.client.Usage(ctx, &snapshotsapi.UsageRequest{
		Snapshotter: p.snapshotterName,
		Key:         key,
	})
	if err != nil {
		return snapshots.Usage{}, errgrpc.ToNative(err)
	}
	return UsageFromProto(resp), nil
}

func (p *proxySnapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	ctx, cancel := p.withTimeout(ctx)
	defer cancel()

	resp, err := p.client.Mounts(ctx, &snapshotsapi.MountsRequest{
		Snapshotter: p.snapshotterName,
		Key:         key,
	})
	if err != nil {
		return nil, errgrpc.ToNative(err)
	}
	return mount.FromProto(resp.Mounts), nil
}

func (p *proxySnapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	ctx, cancel := p.withTimeout(ctx)
	defer cancel()

	var local snapshots.Info
	for _, opt := range opts {
		if err := opt(&local); err != nil {
			return nil, err
		}
	}
	resp, err := p.client.Prepare(ctx, &snapshotsapi.PrepareSnapshotRequest{
		Snapshotter: p.snapshotterName,
		Key:         key,
		Parent:      parent,
		Labels:      local.Labels,
	})
	if err != nil {
		return nil, errgrpc.ToNative(err)
	}
	return mount.FromProto(resp.Mounts), nil
}

func (p *proxySnapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	ctx, cancel := p.withTimeout(ctx)
	defer cancel()

	var local snapshots.Info
	for _, opt := range opts {
		if err := opt(&local); err != nil {
			return nil, err
		}
	}
	resp, err := p.client.View(ctx, &snapshotsapi.ViewSnapshotRequest{
		Snapshotter: p.snapshotterName,
		Key:         key,
		Parent:      parent,
		Labels:      local.Labels,
	})
	if err != nil {
		return nil, errgrpc.ToNative(err)
	}
	return mount.FromProto(resp.Mounts), nil
}

func (p *proxySnapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	ctx, cancel := p.withTimeout(ctx)
	defer cancel()

	var local snapshots.Info
	for _, opt := range opts {
		if err := opt(&local); err != nil {
			return err
		}
	}
	_, err := p.client.Commit(ctx, &snapshotsapi.CommitSnapshotRequest{
		Snapshotter: p.snapshotterName,
		Name:        name,
		Key:         key,
		Parent:      local.Parent,
		Labels:      local.Labels,
	})
	return errgrpc.ToNative(err)
}

func (p *proxySnapshotter) Remove(ctx context.Context, key string) error {
	ctx, cancel := p.withTimeout(ctx)
	defer cancel()

	_, err := p.client.Remove(ctx, &snapshotsapi.RemoveSnapshotRequest{
		Snapshotter: p.snapshotterName,
		Key:         key,
	})
	return errgrpc.ToNative(err)
}

func (p *proxySnapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, fs ...string) error {
	ctx, cancel := p.withTimeout(ctx)
	defer cancel()

	sc, err := p.client.List(ctx, &snapshotsapi.ListSnapshotsRequest{
		Snapshotter: p.snapshotterName,
		Filters:     fs,
	})
	if err != nil {
		return errgrpc.ToNative(err)
	}
	for {
		resp, err := sc.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return errgrpc.ToNative(err)
		}
		if resp == nil {
			return nil
		}
		for _, info := range resp.Info {
			if err := fn(ctx, InfoFromProto(info)); err != nil {
				return err
			}
		}
	}
}

func (p *proxySnapshotter) Close() error {
	return nil
}

func (p *proxySnapshotter) Cleanup(ctx context.Context) error {
	ctx, cancel := p.withTimeout(ctx)
	defer cancel()

	_, err := p.client.Cleanup(ctx, &snapshotsapi.CleanupRequest{
		Snapshotter: p.snapshotterName,
	})
	return errgrpc.ToNative(err)
}
