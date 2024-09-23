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

package command

import (
	gocontext "context"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/dialer"
	"github.com/containerd/containerd/protobuf/proto"
	"github.com/containerd/containerd/protobuf/types"
	"github.com/urfave/cli"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
)

var publishCommand = cli.Command{
	Name:  "publish",
	Usage: "Binary to publish events to containerd",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "namespace",
			Usage: "Namespace to publish to",
		},
		cli.StringFlag{
			Name:  "topic",
			Usage: "Topic of the event",
		},
	},
	Action: func(context *cli.Context) error {
		ctx := namespaces.WithNamespace(gocontext.Background(), context.String("namespace"))
		topic := context.String("topic")
		if topic == "" {
			return fmt.Errorf("topic required to publish event: %w", errdefs.ErrInvalidArgument)
		}
		payload, err := getEventPayload(os.Stdin)
		if err != nil {
			return err
		}
		client, err := connectEvents(context.GlobalString("address"))
		if err != nil {
			return err
		}
		if _, err := client.Publish(ctx, &eventsapi.PublishRequest{
			Topic: topic,
			Event: payload,
		}); err != nil {
			return errdefs.FromGRPC(err)
		}
		return nil
	},
}

func getEventPayload(r io.Reader) (*types.Any, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var any types.Any
	if err := proto.Unmarshal(data, &any); err != nil {
		return nil, err
	}
	return &any, nil
}

func connectEvents(address string) (eventsapi.EventsClient, error) {
	conn, err := connect(address, dialer.ContextDialer)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %q: %w", address, err)
	}
	return eventsapi.NewEventsClient(conn), nil
}

func connect(address string, d func(gocontext.Context, string) (net.Conn, error)) (*grpc.ClientConn, error) {
	backoffConfig := backoff.DefaultConfig
	backoffConfig.MaxDelay = 3 * time.Second
	connParams := grpc.ConnectParams{
		Backoff: backoffConfig,
	}
	gopts := []grpc.DialOption{
		grpc.WithBlock(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(d),
		grpc.FailOnNonTempDialError(true),
		grpc.WithConnectParams(connParams),
	}
	ctx, cancel := gocontext.WithTimeout(gocontext.Background(), 2*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, dialer.DialAddress(address), gopts...)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %q: %w", address, err)
	}
	return conn, nil
}
