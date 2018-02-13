package command

import (
	gocontext "context"
	"io"
	"io/ioutil"
	"net"
	"os"
	"time"

	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/dialer"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/namespaces"
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

func connectEvents(address string) (eventsapi.EventsClient, error) {
	conn, err := connect(address, dialer.Dialer)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial %q", address)
	}
	return eventsapi.NewEventsClient(conn), nil
}

func connect(address string, d func(string, time.Duration) (net.Conn, error)) (*grpc.ClientConn, error) {
	gopts := []grpc.DialOption{
		grpc.WithBlock(),
		grpc.WithInsecure(),
		grpc.WithTimeout(60 * time.Second),
		grpc.WithDialer(d),
		grpc.FailOnNonTempDialError(true),
		grpc.WithBackoffMaxDelay(3 * time.Second),
	}
	conn, err := grpc.Dial(dialer.DialAddress(address), gopts...)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to dial %q", address)
	}
	return conn, nil
}
