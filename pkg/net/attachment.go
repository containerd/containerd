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
	"fmt"
	"strings"

	"github.com/containerd/containerd/log"
	"github.com/containernetworking/cni/libcni"
	types100 "github.com/containernetworking/cni/pkg/types/100"
	"github.com/sirupsen/logrus"
)

type attachment struct {
	attachmentRecord
	cni   libcni.CNI
	store Store
}

var _ Attachment = (*attachment)(nil)

func (a *attachment) ID() string {
	return a.id
}

func (a *attachment) Manager() string {
	return a.manager
}

func (a *attachment) Network() string {
	return a.network.Name
}

func (a *attachment) Container() string {
	return a.args.ContainerID
}

func (a *attachment) IFName() string {
	return a.args.IFName
}

func (a *attachment) NSPath() string {
	return a.args.NSPath
}

func (a *attachment) Result() *types100.Result {
	return a.result
}

func (a *attachment) GCOwnerLables() map[string]string {
	key := fmt.Sprintf("%s.%s/%s", GCRefPrefix, a.network.Name, a.args.IFName)
	val := fmt.Sprintf("%s/%s", a.manager, a.id)
	return map[string]string{key: val}
}

func (a *attachment) Remove(ctx context.Context) error {
	log.G(ctx).WithFields(logrus.Fields{
		"manager": a.Manager(),
		"network": a.Network(),
		"id":      a.ID(),
	}).Debugf("remove")

	return a.store.DeleteAttachment(ctx, a.manager, a.ID(),
		func(ctx context.Context) error {
			if err := a.cni.DelNetworkList(ctx, a.network, a.args.config()); err != nil {
				// we can ignore not found error
				if isNotFoundDelError(a.NSPath(), err) {
					return nil
				}
				return err
			}
			return nil
		})
}

func (a *attachment) Check(ctx context.Context) error {
	log.G(ctx).WithFields(logrus.Fields{
		"manager": a.Manager(),
		"network": a.Network(),
		"id":      a.ID(),
	}).Debugf("check")
	return a.cni.CheckNetworkList(ctx, a.network, a.args.config())
}

func attachmentFromRecord(r *attachmentRecord, cni libcni.CNI, store Store) *attachment {
	return &attachment{
		attachmentRecord: *r,
		cni:              cni,
		store:            store,
	}
}

func createAttachmentID(net, ctrid, ifname string) string {
	return strings.Join([]string{net, ctrid, ifname}, "/")
}

func createAttachmentRecord(id, manager string, network *libcni.NetworkConfigList) *attachmentRecord {
	return &attachmentRecord{
		id:      id,
		manager: manager,
		network: network,
		labels:  make(map[string]string),
		args: AttachmentArgs{
			CapabilityArgs: make(map[string]interface{}),
			PluginArgs:     make(map[string]string),
		},
	}
}

// isNotFoundError returns if the err returned by DelNetworkList is due to
// non-existing network resource
// see: https://github.com/containerd/go-cni/blob/f108694a587347b7b24ec27a1f9b709423faafd3/cni.go#L252
func isNotFoundDelError(nsPath string, err error) bool {
	return (nsPath == "" && strings.Contains(err.Error(), "no such file or directory")) || strings.Contains(err.Error(), "not found")
}
