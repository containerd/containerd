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
	"time"

	v1 "github.com/containerd/containerd/api/services/ttrpc/events/v1"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/ttrpcutil"
	"github.com/containerd/ttrpc"
	"github.com/gogo/protobuf/types"
	"gotest.tools/v3/assert"
)

func TestClientTTRPC_New(t *testing.T) {
	client, err := ttrpcutil.NewClient(address + ".ttrpc")
	assert.NilError(t, err)

	err = client.Close()
	assert.NilError(t, err)
}

func TestClientTTRPC_Reconnect(t *testing.T) {
	client, err := ttrpcutil.NewClient(address + ".ttrpc")
	assert.NilError(t, err)

	err = client.Reconnect()
	assert.NilError(t, err)

	service, err := client.EventsService()
	assert.NilError(t, err)

	// Send test request to make sure its alive after reconnect
	_, err = service.Forward(context.Background(), &v1.ForwardRequest{
		Envelope: &v1.Envelope{
			Timestamp: time.Now(),
			Namespace: namespaces.Default,
			Topic:     "/test",
			Event:     &types.Any{},
		},
	})
	assert.NilError(t, err)

	err = client.Close()
	assert.NilError(t, err)
}

func TestClientTTRPC_Close(t *testing.T) {
	client, err := ttrpcutil.NewClient(address + ".ttrpc")
	assert.NilError(t, err)

	service, err := client.EventsService()
	assert.NilError(t, err)

	err = client.Close()
	assert.NilError(t, err)

	_, err = service.Forward(context.Background(), &v1.ForwardRequest{Envelope: &v1.Envelope{}})
	assert.Equal(t, err, ttrpc.ErrClosed)

	err = client.Close()
	assert.NilError(t, err)
}
