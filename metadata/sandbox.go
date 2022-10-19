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

package metadata

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/filters"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/metadata/boltutil"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/sandbox"
	"github.com/containerd/typeurl"
	runtime "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
	"go.etcd.io/bbolt"
)

type sandboxer struct {
	ctrl sandbox.Controller
	name string
	db   *DB
}

func newSandboxer(db *DB, name string, sn sandbox.Controller) *sandboxer {
	return &sandboxer{
		ctrl: sn,
		name: name,
		db:   db,
	}
}

func (s *sandboxer) Create(ctx context.Context, sb *sandbox.Sandbox) (*sandbox.Sandbox, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	var (
		id  = sb.ID
		now = time.Now().UTC()
		ret *sandbox.Sandbox
	)

	if id == "" {
		return nil, errors.Wrap(errdefs.ErrInvalidArgument, "empty sandbox ID")
	}

	if err := update(ctx, s.db, func(tx *bbolt.Tx) (retErr error) {
		parent := getSandboxBuckets(tx, ns, s.name)
		if parent != nil && parent.Bucket([]byte(id)) != nil {
			return errdefs.ErrAlreadyExists
		}

		in := &sandbox.Sandbox{
			ID:         id,
			Spec:       sb.Spec,
			Labels:     sb.Labels,
			CreatedAt:  now,
			UpdatedAt:  now,
			Extensions: sb.Extensions,
		}

		out, err := s.ctrl.Start(ctx, in)
		if err != nil {
			return err
		}
		defer func() {
			if retErr != nil {
				if err := s.ctrl.Shutdown(ctx, id); err != nil {
					log.G(ctx).Warnf("failed to shutdown vm when rollback, %v", err)
				}
			}
		}()

		if err := s.validate(in, out); err != nil {
			return err
		}

		parent, err = createSandboxBuckets(tx, ns, s.name)
		if err != nil {
			return err
		}

		if err := s.write(parent, out); err != nil {
			return err
		}

		ret = out
		return nil
	}); err != nil {
		return nil, err
	}

	return ret, nil
}

func (s *sandboxer) Update(ctx context.Context, sb *sandbox.Sandbox, fieldpaths ...string) (*sandbox.Sandbox, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	ret := &sandbox.Sandbox{}
	if err := update(ctx, s.db, func(tx *bbolt.Tx) error {
		parent := getSandboxBuckets(tx, ns, s.name)

		updated, err := s.read(parent, []byte(sb.ID))
		if err != nil {
			return err
		}

		if len(fieldpaths) == 0 {
			fieldpaths = []string{"labels", "extensions", "spec"}
		}

		for _, path := range fieldpaths {
			if strings.HasPrefix(path, "labels.") {
				if updated.Labels == nil {
					updated.Labels = map[string]string{}
				}

				key := strings.TrimPrefix(path, "labels.")
				updated.Labels[key] = sb.Labels[key]
				continue
			} else if strings.HasPrefix(path, "extensions.") {
				if updated.Extensions == nil {
					updated.Extensions = map[string]typeurl.Any{}
				}

				key := strings.TrimPrefix(path, "extensions.")
				updated.Extensions[key] = sb.Extensions[key]
				continue
			}

			switch path {
			case "labels":
				updated.Labels = sb.Labels
			case "extensions":
				updated.Extensions = sb.Extensions
			case "spec":
				updated.Spec = sb.Spec
			default:
				return errors.Wrapf(errdefs.ErrInvalidArgument, "cannot update %q field on sandbox %q", path, sb.ID)
			}
		}

		updated.UpdatedAt = time.Now().UTC()
		newSb, err := s.ctrl.Update(ctx, sb.ID, updated)
		if err != nil {
			return err
		}
		if err := s.validate(updated, newSb); err != nil {
			return err
		}

		if err := s.write(parent, newSb); err != nil {
			return err
		}

		ret = newSb
		return nil
	}); err != nil {
		return nil, err
	}

	return ret, nil
}

func (s *sandboxer) AppendContainer(ctx context.Context, sandboxID string, container *sandbox.Container) (*sandbox.Container, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	var ret *sandbox.Container
	if err := update(ctx, s.db, func(tx *bbolt.Tx) error {
		parent := getSandboxBuckets(tx, ns, s.name)
		updated, err := s.read(parent, []byte(sandboxID))
		if err != nil {
			return err
		}
		newCont, err := s.ctrl.AppendContainer(ctx, sandboxID, container)
		if err != nil {
			return err
		}
		updated.Containers = append(updated.Containers, *newCont)
		updated.UpdatedAt = time.Now().UTC()

		if err := s.validate(updated, updated); err != nil {
			return err
		}

		if err := s.write(parent, updated); err != nil {
			return err
		}

		ret = newCont
		return nil
	}); err != nil {
		return nil, err
	}

	return ret, nil
}

func (s *sandboxer) UpdateContainer(ctx context.Context, sandboxID string, container *sandbox.Container) (*sandbox.Container, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	var ret *sandbox.Container
	if err := update(ctx, s.db, func(tx *bbolt.Tx) error {
		parent := getSandboxBuckets(tx, ns, s.name)
		updated, err := s.read(parent, []byte(sandboxID))
		if err != nil {
			return err
		}
		newCont, err := s.ctrl.UpdateContainer(ctx, sandboxID, container)
		if err != nil {
			return err
		}
		for i, c := range updated.Containers {
			if c.ID == newCont.ID {
				updated.Containers[i] = *newCont
			}
		}
		updated.UpdatedAt = time.Now().UTC()

		if err := s.validate(updated, updated); err != nil {
			return err
		}

		if err := s.write(parent, updated); err != nil {
			return err
		}

		ret = newCont
		return nil
	}); err != nil {
		return nil, err
	}

	return ret, nil
}

func (s *sandboxer) RemoveContainer(ctx context.Context, sandboxID string, id string) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	if err := update(ctx, s.db, func(tx *bbolt.Tx) error {
		parent := getSandboxBuckets(tx, ns, s.name)
		updated, err := s.read(parent, []byte(sandboxID))
		if err != nil {
			return err
		}
		err = s.ctrl.RemoveContainer(ctx, sandboxID, id)
		if err != nil {
			return err
		}
		var residualContainers []sandbox.Container
		for _, c := range updated.Containers {
			if c.ID != id {
				residualContainers = append(residualContainers, c)
			}
		}
		updated.Containers = residualContainers
		updated.UpdatedAt = time.Now().UTC()

		if err := s.validate(updated, updated); err != nil {
			return err
		}

		if err := s.write(parent, updated); err != nil {
			return err
		}

		return nil
	}); err != nil {
		return err
	}

	return nil
}

func (s *sandboxer) Delete(ctx context.Context, id string) error {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}

	if err := update(ctx, s.db, func(tx *bbolt.Tx) error {
		buckets := getSandboxBuckets(tx, ns, s.name)
		if buckets == nil {
			return errors.Wrap(errdefs.ErrNotFound, "no sandbox buckets")
		}

		instance, err := s.read(buckets, []byte(id))
		if err != nil {
			return err
		}

		if err := buckets.DeleteBucket([]byte(id)); err != nil {
			return errors.Wrapf(err, "failed to delete bucket %q", id)
		}

		if err := s.ctrl.Shutdown(ctx, instance.ID); err != nil && !errdefs.IsNotFound(err) {
			return errors.Wrapf(err, "failed to delete sandbox %q", id)
		}

		return nil
	}); err != nil {
		return err
	}

	return nil
}

func (s *sandboxer) List(ctx context.Context, fields ...string) ([]sandbox.Sandbox, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	filter, err := filters.ParseAll(fields...)
	if err != nil {
		return nil, errors.Wrap(errdefs.ErrInvalidArgument, err.Error())
	}

	var (
		list []sandbox.Sandbox
	)

	if err := view(ctx, s.db, func(tx *bbolt.Tx) error {
		bucket := getSandboxBuckets(tx, ns, s.name)
		if bucket == nil {
			return errors.Wrap(errdefs.ErrNotFound, "not sandbox buckets")
		}

		if err := bucket.ForEach(func(k, v []byte) error {
			info, err := s.read(bucket, k)
			if err != nil {
				return errors.Wrapf(err, "failed to read bucket %q", string(k))
			}

			if filter.Match(adaptSandbox(info)) {
				list = append(list, *info)
			}

			return nil
		}); err != nil {
			return err
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return list, nil
}

func (s *sandboxer) Get(ctx context.Context, id string) (*sandbox.Sandbox, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}

	var ret *sandbox.Sandbox
	if err := view(ctx, s.db, func(tx *bbolt.Tx) error {
		bucket := getSandboxBuckets(tx, ns, s.name)
		if bucket == nil {
			return errors.Wrap(errdefs.ErrNotFound, "not sandbox buckets")
		}
		bkt := bucket.Bucket([]byte(id))
		if bkt == nil {
			return errors.Wrap(errdefs.ErrNotFound, "not sandbox metadata")
		}
		sb, err := s.read(bucket, []byte(id))
		if err != nil {
			return err
		}
		ret = sb
		return nil
	}); err != nil {
		return nil, err
	}

	return ret, nil
}

func (s *sandboxer) Status(ctx context.Context, id string) (sandbox.Status, error) {
	return s.ctrl.Status(ctx, id)
}

var _ sandbox.Sandboxer = &sandboxer{}

func (s *sandboxer) validate(old, new *sandbox.Sandbox) error {
	if new.ID == "" {
		return errors.Wrap(errdefs.ErrInvalidArgument, "instance ID must not be empty")
	}

	if new.CreatedAt.IsZero() {
		return errors.Wrap(errdefs.ErrInvalidArgument, "creation date must not be zero")
	}

	if new.UpdatedAt.IsZero() {
		return errors.Wrap(errdefs.ErrInvalidArgument, "updated date must not be zero")
	}

	if new.Spec == nil {
		return errors.Wrap(errdefs.ErrInvalidArgument, "sandbox spec must not be nil")
	}

	if old.ID != new.ID {
		return errors.Wrap(errdefs.ErrFailedPrecondition, "controller should preserve instance ID")
	}

	if old.CreatedAt != new.CreatedAt {
		return errors.Wrap(errdefs.ErrFailedPrecondition, "controller should preserve creation time")
	}

	return nil
}

func (s *sandboxer) read(parent *bbolt.Bucket, id []byte) (*sandbox.Sandbox, error) {
	var (
		inst sandbox.Sandbox
		err  error
	)

	bucket := parent.Bucket(id)
	if bucket == nil {
		return nil, errors.Wrapf(errdefs.ErrNotFound, "bucket %q not found", id)
	}

	inst.ID = string(id)
	inst.TaskAddress = string(bucket.Get([]byte("address")))

	inst.Labels, err = boltutil.ReadLabels(bucket)
	if err != nil {
		return nil, err
	}

	if err := boltutil.ReadTimestamps(bucket, &inst.CreatedAt, &inst.UpdatedAt); err != nil {
		return nil, err
	}

	specData := bucket.Get([]byte("spec"))
	if specData != nil {
		runtimeSpec := runtime.Spec{}
		if err := json.Unmarshal(specData, &runtimeSpec); err != nil {
			return nil, errors.Wrap(err, "failed to unmarshal runtime spec")
		}
		inst.Spec = &runtimeSpec
	}

	inst.Extensions, err = boltutil.ReadExtensions(bucket)
	if err != nil {
		return nil, err
	}

	inst.Containers, err = s.readContainers(bucket)
	if err != nil {
		return nil, err
	}

	return &inst, nil
}

func (s *sandboxer) readContainers(bkt *bbolt.Bucket) ([]sandbox.Container, error) {
	var containers []sandbox.Container
	cbkt := bkt.Bucket([]byte("containers"))
	if cbkt == nil {
		return containers, nil
	}
	if err := cbkt.ForEach(func(k, v []byte) error {
		c, err := s.readContainer(cbkt.Bucket(k))
		if err != nil {
			return err
		}
		containers = append(containers, c)
		return nil
	}); err != nil {
		return nil, err
	}
	return containers, nil
}

func (s *sandboxer) readContainer(bkt *bbolt.Bucket) (sandbox.Container, error) {
	var cont sandbox.Container

	cont.ID = string(bkt.Get([]byte("id")))
	labels, err := boltutil.ReadLabels(bkt)
	if err != nil {
		return cont, err
	}
	cont.Labels = labels

	ext, err := boltutil.ReadExtensions(bkt)
	if err != nil {
		return cont, err
	}
	cont.Extensions = ext

	cont.Spec, err = boltutil.ReadAny(bkt, []byte("spec"))
	if err != nil {
		return cont, err
	}
	rootfsBytes := bkt.Get([]byte("rootfs"))
	if len(rootfsBytes) > 0 {
		if err := json.Unmarshal(rootfsBytes, &cont.Rootfs); err != nil {
			return cont, err
		}
	}
	cont.Io, err = s.readIO(bkt)
	if err != nil {
		return cont, err
	}
	cont.Processes, err = s.readProcesses(bkt)
	if err != nil {
		return cont, err
	}
	return cont, nil
}

func (s *sandboxer) readIO(bkt *bbolt.Bucket) (*sandbox.IO, error) {
	bucket := bkt.Bucket([]byte("io"))
	if bucket == nil {
		return nil, nil
	}
	var io sandbox.IO
	io.Stdin = string(bucket.Get([]byte("stdin")))
	io.Stdin = string(bucket.Get([]byte("stdin")))
	io.Stdin = string(bucket.Get([]byte("stdin")))
	t := bucket.Get([]byte("terminal"))
	if len(t) >= 1 && t[0] > 0 {
		io.Terminal = true
	} else {
		io.Terminal = false
	}
	return &io, nil
}

func (s *sandboxer) readProcesses(bkt *bbolt.Bucket) ([]sandbox.Process, error) {
	var processes []sandbox.Process
	cbkt := bkt.Bucket([]byte("processes"))
	if cbkt == nil {
		return nil, nil
	}
	if err := cbkt.ForEach(func(k, v []byte) error {
		p, err := s.readProcess(cbkt.Bucket(k))
		if err != nil {
			return err
		}
		processes = append(processes, p)
		return nil
	}); err != nil {
		return nil, err
	}
	return processes, nil
}

func (s *sandboxer) readProcess(bkt *bbolt.Bucket) (sandbox.Process, error) {
	var process sandbox.Process

	process.ID = string(bkt.Get([]byte("id")))
	p, err := boltutil.ReadAny(bkt, []byte("process"))
	if err != nil {
		return process, err
	}
	process.Process = p
	process.Extensions, err = boltutil.ReadExtensions(bkt)
	if err != nil {
		return process, err
	}
	process.Io, err = s.readIO(bkt)
	if err != nil {
		return process, err
	}
	return process, nil
}

func (s *sandboxer) write(parent *bbolt.Bucket, instance *sandbox.Sandbox) error {
	bucket, err := parent.CreateBucketIfNotExists([]byte(instance.ID))
	if err != nil {
		return err
	}

	if err := bucket.Put([]byte("id"), []byte(instance.ID)); err != nil {
		return err
	}

	if err := bucket.Put([]byte("address"), []byte(instance.TaskAddress)); err != nil {
		return err
	}
	if err := boltutil.WriteTimestamps(bucket, instance.CreatedAt, instance.UpdatedAt); err != nil {
		return err
	}

	if err := boltutil.WriteLabels(bucket, instance.Labels); err != nil {
		return err
	}

	if err := boltutil.WriteExtensions(bucket, instance.Extensions); err != nil {
		return err
	}

	spec, err := json.Marshal(instance.Spec)
	if err != nil {
		return errors.Wrap(err, "failed to marshal runtime spec")
	}

	if err := bucket.Put([]byte("spec"), spec); err != nil {
		return err
	}

	if err := s.writeContainers(bucket, instance.Containers); err != nil {
		return err
	}

	return nil
}

func (s *sandboxer) writeContainers(parent *bbolt.Bucket, containers []sandbox.Container) error {
	if cbkts := parent.Bucket([]byte("containers")); cbkts != nil {
		if err := parent.DeleteBucket([]byte("containers")); err != nil {
			return err
		}
	}
	bucket, err := parent.CreateBucket([]byte("containers"))
	if err != nil {
		return err
	}
	for _, container := range containers {
		if err := s.writeContainer(bucket, container); err != nil {
			return err
		}
	}

	return nil
}

func (s *sandboxer) writeContainer(parent *bbolt.Bucket, container sandbox.Container) error {
	bucket, err := parent.CreateBucketIfNotExists([]byte(container.ID))
	if err != nil {
		return err
	}

	if err := bucket.Put([]byte("id"), []byte(container.ID)); err != nil {
		return err
	}
	if err := boltutil.WriteLabels(bucket, container.Labels); err != nil {
		return err
	}

	if err := boltutil.WriteExtensions(bucket, container.Extensions); err != nil {
		return err
	}
	if err := boltutil.WriteAny(bucket, []byte("spec"), container.Spec); err != nil {
		return err
	}
	rootfs, err := json.Marshal(container.Rootfs)
	if err != nil {
		return errors.Wrap(err, "failed to marshal container rootfs")
	}
	if err := bucket.Put([]byte("rootfs"), rootfs); err != nil {
		return err
	}
	if err := s.writeIo(bucket, container.Io); err != nil {
		return err
	}
	if err := bucket.Put([]byte("bundle"), []byte(container.Bundle)); err != nil {
		return err
	}

	if err := s.writeProcesses(bucket, container.Processes); err != nil {
		return err
	}

	return nil
}

func (s *sandboxer) writeProcesses(parent *bbolt.Bucket, processes []sandbox.Process) error {
	if pbkts := parent.Bucket([]byte("processes")); pbkts != nil {
		if err := parent.DeleteBucket([]byte("processes")); err != nil {
			return err
		}
	}
	bucket, err := parent.CreateBucket([]byte("processes"))
	if err != nil {
		return err
	}
	for _, process := range processes {
		if err := s.writeProcess(bucket, process); err != nil {
			return err
		}
	}

	return nil
}

func (s *sandboxer) writeProcess(parent *bbolt.Bucket, process sandbox.Process) error {
	bucket, err := parent.CreateBucketIfNotExists([]byte(process.ID))
	if err != nil {
		return err
	}

	if err := bucket.Put([]byte("id"), []byte(process.ID)); err != nil {
		return err
	}
	if err := boltutil.WriteAny(bucket, []byte("process"), process.Process); err != nil {
		return err
	}
	if err := boltutil.WriteExtensions(bucket, process.Extensions); err != nil {
		return err
	}
	if err := s.writeIo(bucket, process.Io); err != nil {
		return err
	}

	return nil
}

func (s *sandboxer) writeIo(parent *bbolt.Bucket, io *sandbox.IO) error {
	if io == nil {
		return nil
	}
	bucket, err := parent.CreateBucketIfNotExists([]byte("io"))
	if err != nil {
		return err
	}
	if err := bucket.Put([]byte("stdin"), []byte(io.Stdin)); err != nil {
		return err
	}
	if err := bucket.Put([]byte("stdout"), []byte(io.Stdout)); err != nil {
		return err
	}
	if err := bucket.Put([]byte("stdin"), []byte(io.Stderr)); err != nil {
		return err
	}
	var terminal byte
	if io.Terminal {
		terminal = 1
	}
	if err := bucket.Put([]byte("terminal"), []byte{terminal}); err != nil {
		return err
	}
	return nil
}
