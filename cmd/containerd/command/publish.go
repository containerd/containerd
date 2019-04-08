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
	"io"
	"io/ioutil"
	"net"
	"os"
	"time"

	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/dialer"
	"github.com/gogo/protobuf/types"
	"github.com/pkg/errors"
	"github.com/urfave/cli"
	"google.golang.org/grpc"
)

var publishCommand = cli.Command{
	Name:  "publish",
	Usage: "binary to publish events to containerd",
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "namespace",
			Usage: "namespace to publish to",
		},
		cli.StringFlag{
			Name:  "topic",
			Usage: "topic of the event",
		},
	},
	Action: func(context *cli.Context) error {
		ctx := namespaces.WithNamespace(gocontext.Background(), context.String("namespace"))
		topic := context.String("topic")
		if topic == "" {
			return errors.New("topic required to publish event")
		}
		c := connectEvents(context.GlobalString("address"))
		payload, err := getEventPayload(os.Stdin)
		if err != nil {
			return err
		}
		cCon := <-c
		if cCon.err != nil {
			return err
		}
		defer cCon.Close()
		if _, err := cCon.client.Publish(ctx, &eventsapi.PublishRequest{
			Topic: topic,
			Event: payload,
		}); err != nil {
			return errdefs.FromGRPC(err)
		}
		return nil
	},
}

type clientConnect struct {
	conn   *grpc.ClientConn
	client eventsapi.EventsClient
	err    error
}

func (c *clientConnect) Close() error {
	return c.conn.Close()
}

func getEventPayload(r io.Reader) (*types.Any, error) {
	data, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var any types.Any
	if err := any.Unmarshal(data); err != nil {
		return nil, err
	}
	return &any, nil
}

func connectEvents(address string) <-chan *clientConnect {
	c := make(chan *clientConnect, 1)
	go func() {
		conn, err := connect(address, dialer.Dialer)
		if err != nil {
			c <- &clientConnect{
				err: errors.Wrapf(err, "failed to dial %q", address),
			}
			return
		}
		c <- &clientConnect{
			client: eventsapi.NewEventsClient(conn),
			conn:   conn,
		}
	}()
	return c
}

func connect(address string, d func(string, time.Duration) (net.Conn, error)) (*grpc.ClientConn, error) {
	gopts := []grpc.DialOption{
		grpc.WithBlock(),
		grpc.WithInsecure(),
		grpc.WithDialer(d),
		grpc.FailOnNonTempDialError(true),
		grpc.WithBackoffMaxDelay(3 * time.Second),
	}
	ctx, cancel := gocontext.WithTimeout(gocontext.Background(), 2*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, dialer.DialAddress(address), gopts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial %q", address)
	}
	return conn, nil
}
