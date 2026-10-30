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

package server

import (
	"context"
	"fmt"

	"github.com/containerd/containerd/v2/core/sandbox"
	ctrdutil "github.com/containerd/containerd/v2/internal/cri/util"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const podRestoreTransactionKey = "io.cri-containerd.restore"

func init() {
	typeurl.Register(&podRestoreTransaction{}, "github.com/containerd/cri", "PodRestoreTransaction")
}

// podRestoreTransaction is saved with the initial record of a sandbox created
// by RestorePod. It stays uncommitted until every restored container's task
// checkpoint reference is saved; startup recovery removes uncommitted sandboxes.
type podRestoreTransaction struct {
	Version   int
	Committed bool
}

func loadPodRestoreTransaction(record sandbox.Sandbox) (*podRestoreTransaction, error) {
	transaction := new(podRestoreTransaction)
	if err := record.GetExtension(podRestoreTransactionKey, transaction); err != nil {
		return nil, err
	}
	if transaction.Version != 1 {
		return nil, fmt.Errorf("unsupported restore transaction version %d for sandbox %q: %w", transaction.Version, record.ID, errdefs.ErrFailedPrecondition)
	}
	return transaction, nil
}

func (c *criService) commitPodRestore(ctx context.Context, sandboxID string) error {
	record, err := c.client.SandboxStore().Get(ctx, sandboxID)
	if err != nil {
		return err
	}
	if err := record.AddExtension(podRestoreTransactionKey, &podRestoreTransaction{Version: 1, Committed: true}); err != nil {
		return err
	}
	_, err = c.client.SandboxStore().Update(ctx, record, "extensions."+podRestoreTransactionKey)
	return err
}

// rollbackPodRestore uses the ordinary CRI lifecycle, which also delegates
// backend cleanup to the selected controller. Never discard a transaction
// because its resources failed to load into the in-memory stores.
func (c *criService) rollbackPodRestore(ctx context.Context, sandboxID string) error {
	namespace, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return err
	}
	cleanupCtx, cancel := ctrdutil.DeferContext()
	defer cancel()
	cleanupCtx = namespaces.WithNamespace(cleanupCtx, namespace)
	if _, err := c.sandboxStore.Get(sandboxID); err != nil {
		return fmt.Errorf("restore sandbox %q is not loaded: %w", sandboxID, err)
	}
	containers, err := c.client.Containers(cleanupCtx)
	if err != nil {
		return err
	}
	for _, container := range containers {
		info, err := container.Info(cleanupCtx)
		if err != nil {
			return err
		}
		if info.SandboxID == sandboxID {
			if _, err := c.containerStore.Get(container.ID()); err != nil {
				return fmt.Errorf("restore container %q is not loaded: %w", container.ID(), err)
			}
		}
	}
	_, err = c.RemovePodSandbox(cleanupCtx, &runtime.RemovePodSandboxRequest{PodSandboxId: sandboxID})
	return err
}

// recoverPodRestores removes the sandboxes of restores interrupted before they
// were committed.
func (c *criService) recoverPodRestores(ctx context.Context) error {
	records, err := c.client.SandboxStore().List(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		if _, ok := record.Extensions[podRestoreTransactionKey]; !ok {
			continue
		}
		transaction, err := loadPodRestoreTransaction(record)
		if err != nil {
			return err
		}
		if transaction.Committed {
			continue
		}
		if err := c.rollbackPodRestore(ctx, record.ID); err != nil {
			return fmt.Errorf("roll back interrupted restore %q: %w", record.ID, err)
		}
	}
	return nil
}
