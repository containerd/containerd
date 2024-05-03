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

package client

import (
	"context"
	"testing"
	"time"

	v1 "github.com/containerd/containerd/api/services/ttrpc/events/v1"
	apitypes "github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/protobuf"
	"github.com/containerd/containerd/v2/pkg/protobuf/types"
	"github.com/containerd/containerd/v2/pkg/ttrpcutil"
	"github.com/containerd/ttrpc"
	"github.com/stretchr/testify/assert"
)

func TestClientTTRPC_New(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	client, err := ttrpcutil.NewClient(address + ".ttrpc")
	assert.NoError(t, err)

	err = client.Close()
	assert.NoError(t, err)
}

func TestClientTTRPC_Reconnect(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	client, err := ttrpcutil.NewClient(address + ".ttrpc")
	assert.NoError(t, err)

	err = client.Reconnect()
	assert.NoError(t, err)

	service, err := client.EventsService()
	assert.NoError(t, err)

	// Send test request to make sure its alive after reconnect
	_, err = service.Forward(context.Background(), &v1.ForwardRequest{
		Envelope: &apitypes.Envelope{
			Timestamp: protobuf.ToTimestamp(time.Now()),
			Namespace: namespaces.Default,
			Topic:     "/test",
			Event:     &types.Any{},
		},
	})
	assert.NoError(t, err)

	err = client.Close()
	assert.NoError(t, err)
}

func TestClientTTRPC_Close(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	client, err := ttrpcutil.NewClient(address + ".ttrpc")
	assert.NoError(t, err)

	service, err := client.EventsService()
	assert.NoError(t, err)

	err = client.Close()
	assert.NoError(t, err)

	_, err = service.Forward(context.Background(), &v1.ForwardRequest{Envelope: &apitypes.Envelope{}})
	assert.Equal(t, err, ttrpc.ErrClosed)

	err = client.Close()
	assert.NoError(t, err)
}
