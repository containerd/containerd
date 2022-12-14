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

package net

import (
	"context"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/log"
	"github.com/containernetworking/cni/libcni"
	types100 "github.com/containernetworking/cni/pkg/types/100"
	"github.com/sirupsen/logrus"
)

type network struct {
	networkRecord
	cni   libcni.CNI
	store Store
}

var _ Network = (*network)(nil)

func (n *network) Name() string {
	return n.config.Name
}

func (n *network) Manager() string {
	return n.manager
}

func (n *network) Config() *libcni.NetworkConfigList {
	return n.config
}

func (n *network) Labels() map[string]string {
	r := make(map[string]string)
	for k, v := range n.labels {
		r[k] = v
	}
	return r
}

func (n *network) Update(ctx context.Context, opts ...NetworkOpt) error {
	log.G(ctx).WithFields(logrus.Fields{"manager:": n.manager, "network:": n.config.Name}).Debugf("update network")

	c := NetworkConfig{}
	for _, o := range opts {
		if err := o(&c); err != nil {
			return err
		}
	}
	if c.Conflist.Name != n.Name() {
		return errdefs.ErrInvalidArgument
	}
	if err := c.validate(); err != nil {
		return err
	}

	update := networkRecord{
		manager: n.manager,
		config:  c.Conflist,
		labels:  c.Labels,
	}
	if err := n.store.UpdateNetwork(ctx, &update); err != nil {
		return err
	}
	n.networkRecord = update

	return nil
}

func (n *network) Delete(ctx context.Context) error {
	log.G(ctx).WithField("manager", n.manager).WithField("network", n.config.Name).Debugf("delete network")

	return n.store.DeleteNetwork(ctx, n.manager, n.config.Name)
}

func (n *network) Attach(ctx context.Context, opts ...AttachmentOpt) (Attachment, error) {
	rec := createAttachmentRecord("", n.manager, n.config)
	for _, o := range opts {
		if err := o(&rec.args); err != nil {
			return nil, err
		}
	}

	if err := rec.args.validate(); err != nil {
		return nil, err
	}

	rec.id = createAttachmentID(n.config.Name, rec.args.ContainerID, rec.args.IFName)

	log.G(ctx).WithFields(logrus.Fields{
		"manager": rec.manager,
		"id":      rec.id,
		"nspath":  rec.args.NSPath,
	}).Debugf("attach")

	leaseID, ok := leases.FromContext(ctx)
	if ok {
		rec.labels["lease"] = leaseID
	}

	creator := func(ctx context.Context) (*types100.Result, error) {
		result, err := n.cni.AddNetworkList(ctx, n.config, rec.args.config())
		if err != nil {
			return nil, err
		}
		return types100.NewResultFromResult(result)
	}

	deleter := func(ctx context.Context) error {
		if err := n.cni.DelNetworkList(ctx, n.config, rec.args.config()); err != nil {
			// ignore not found error
			if isNotFoundDelError(rec.args.NSPath, err) {
				return nil
			}
			return err
		}
		return nil
	}

	if err := n.store.CreateAttachment(ctx, rec, creator, deleter); err != nil {
		return nil, err
	}

	return attachmentFromRecord(rec, n.cni, n.store), nil
}

func (n *network) List(ctx context.Context) []Attachment {
	var attachList []Attachment

	if err := n.Walk(ctx, func(r *attachment) error {
		attachList = append(attachList, r)
		return nil

	}); err != nil {
		return nil
	}

	return attachList
}

func (n *network) Walk(ctx context.Context, fn func(*attachment) error) error {
	var attachList []*attachment

	if err := n.store.WalkAttachments(ctx, n.manager, n.config.Name, func(rec *attachmentRecord) error {
		attachList = append(attachList, attachmentFromRecord(rec, n.cni, n.store))
		return nil

	}); err != nil {
		return err
	}

	for _, att := range attachList {
		fn(att)
	}

	return nil
}

func networkFromRecord(r *networkRecord, cni libcni.CNI, store Store) *network {
	return &network{
		networkRecord: *r,
		cni:           cni,
		store:         store,
	}
}

func createNetworkRecord(manager string, conflist *libcni.NetworkConfigList) *networkRecord {
	return &networkRecord{
		manager: manager,
		config:  conflist,
		labels:  make(map[string]string),
	}
}
