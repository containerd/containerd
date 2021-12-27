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

package tasks

import (
	"context"
	gocontext "context"
	"fmt"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/netns"
	gocni "github.com/containerd/go-cni"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli"
)

type Network struct {
	id    string
	netNS *netns.NetNS
	cni   gocni.CNI
}

func LoadNetworkFromContainer(ctx gocontext.Context, id string, spec *specs.Spec) (*Network, error) {

	if spec.Linux == nil {
		return nil, nil
	}

	var nsPath string
	for _, ns := range spec.Linux.Namespaces {
		if ns.Type == specs.NetworkNamespace {
			nsPath = ns.Path
			break
		}
	}

	if nsPath == "" {
		return nil, nil
	}
	log.G(ctx).Debugf("Get container network namespace %s", nsPath)

	cni, err := gocni.New(gocni.WithDefaultConf, gocni.WithLoNetwork, gocni.WithMinNetworkCount(2))
	if err != nil {
		return nil, err
	}

	return &Network{
		id:    id,
		netNS: netns.LoadNetNS(nsPath),
		cni:   cni,
	}, nil
}

func SetupNetwork(ctx context.Context, id string, context *cli.Context) (*Network, error) {
	var err error

	var netnsMountDir = "/var/run/netns"
	netNS, err := netns.NewNetNS(netnsMountDir)
	if err != nil {
		return nil, err
	}

	defer func() {
		if err != nil && netNS != nil {
			if err := netNS.Remove(); err != nil {
				log.G(ctx).WithError(err).Error("failed to remove netNS in rollback")
			}
		}
	}()

	// CNI needs to attach to at least loopback network and a non host network,
	// hence networkAttachCount is 2.
	cni, err := gocni.New(gocni.WithDefaultConf, gocni.WithLoNetwork, gocni.WithMinNetworkCount(2))
	if err != nil {
		return nil, err
	}

	if _, err = cni.Setup(ctx, namespacedID(ctx, id), netNS.GetPath()); err != nil {
		return nil, err
	}

	// FIXME should check `--with-ns network:/run/xxx` before creating new NS?
	context.Set("with-ns", fmt.Sprintf("%s:%s", specs.NetworkNamespace, netNS.GetPath()))

	return &Network{
		id:    id,
		netNS: netNS,
		cni:   cni,
	}, nil
}

func (n *Network) Teardown(ctx context.Context) error {
	if err := n.cni.Remove(ctx, namespacedID(ctx, n.id), ""); err != nil {
		return err
	}
	if closed, err := n.netNS.Closed(); err != nil {
		return err
	} else if closed {
		return nil
	}
	return n.netNS.Remove()
}

func namespacedID(ctx context.Context, id string) string {
	ns, ok := namespaces.Namespace(ctx)
	if !ok {
		return id
	}
	return fmt.Sprintf("%s-%s", ns, id)
}
