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

package podsandbox

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/log"

	eventtypes "github.com/containerd/containerd/api/events"
	ctrdutil "github.com/containerd/containerd/v2/internal/cri/util"
)

const (
	// handleEventTimeout is the timeout for handling 1 event. Event monitor
	// handles events in serial, if one event blocks the event monitor, no
	// other events can be handled.
	// Add a timeout for each event handling, events that timeout will be requeued and
	// handled again in the future.
	handleEventTimeout = 10 * time.Second
)

type podSandboxEventHandler struct {
	controller *Controller
}

func (p *podSandboxEventHandler) HandleEvent(any interface{}) error {
	switch e := any.(type) {
	case *eventtypes.TaskExit:
		log.L.Infof("TaskExit event in podsandbox handler %+v", e)
		// Use ID instead of ContainerID to rule out TaskExit event for exec.
		sb := p.controller.store.Get(e.ID)
		if sb == nil || sb.Container == nil {
			return nil
		}
		ctx := ctrdutil.NamespacedContext()
		ctx, cancel := context.WithTimeout(ctx, handleEventTimeout)
		defer cancel()
		if err := handleSandboxTaskExit(ctx, sb, e); err != nil {
			return fmt.Errorf("failed to handle container TaskExit event: %w", err)
		}
		return nil
	}
	return nil
}
