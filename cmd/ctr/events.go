package main

import (
	"encoding/json"
	"fmt"

	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/typeurl"
	"github.com/urfave/cli"
)

var eventsCommand = cli.Command{
	Name:  "events",
	Usage: "display containerd events",
	Action: func(context *cli.Context) error {
		eventsClient, err := getEventsService(context)
		if err != nil {
			return err
		}
		ctx, cancel := appContext(context)
		defer cancel()

		events, err := eventsClient.Subscribe(ctx, &eventsapi.SubscribeRequest{
			Filters: context.Args(),
		})
		if err != nil {
			return err
		}
		for {
			e, err := events.Recv()
			if err != nil {
				return err
			}

			var out []byte
			if e.Event != nil {
				v, err := typeurl.UnmarshalAny(e.Event)
				if err != nil {
					return err
				}
				out, err = json.Marshal(v)
				if err != nil {
					return err
				}
			}

			if _, err := fmt.Println(
				e.Timestamp,
				e.Namespace,
				e.Topic,
				string(out),
			); err != nil {
				return err
			}
		}
	},
}
