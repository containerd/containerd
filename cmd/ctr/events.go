package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/typeurl"
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

		events, err := eventsClient.Stream(ctx, &eventsapi.StreamEventsRequest{})
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 10, 1, 3, ' ', 0)
		for {
			e, err := events.Recv()
			if err != nil {
				return err
			}

			v, err := typeurl.UnmarshalAny(e.Event)
			if err != nil {
				return err
			}
			out, err := json.Marshal(v)
			if err != nil {
				return err
			}

			if _, err := fmt.Fprintf(w,
				"%s\t%s",
				e.Timestamp,
				e.Topic,
			); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "\t%s\n", out); err != nil {
				return err
			}
			if err := w.Flush(); err != nil {
				return err
			}
		}
	},
}
