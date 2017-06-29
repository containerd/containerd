package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	eventsapi "github.com/containerd/containerd/api/services/events/v1"
	"github.com/containerd/containerd/events"
	"github.com/urfave/cli"
)

var eventsCommand = cli.Command{
	Name:  "events",
	Usage: "display containerd events",
	Flags: []cli.Flag{
		cli.StringSliceFlag{
			Name:  "filter, f",
			Usage: "Specify one or more event filters (i.e. type==ContainerCreate or container_id==alpine)",
			Value: &cli.StringSlice{},
		},
	},
	Action: func(context *cli.Context) error {
		client, err := newClient(context)
		if err != nil {
			return err
		}

		ctx, cancel := appContext(context)
		defer cancel()

		events, err := client.Events(ctx, context.StringSlice("filter")...)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 10, 1, 3, ' ', 0)
		for {
			e, err := events.Recv()
			if err != nil {
				return err
			}

			out, err := getEventOutput(e)
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

func getEventOutput(env *eventsapi.Envelope) (string, error) {
	out := ""

	var de events.DynamicEvent
	if err := events.UnmarshalEvent(env.Event, &de); err != nil {
		return "", err
	}

	switch e := de.Event.(type) {
	case *eventsapi.ContainerCreate:
		out = fmt.Sprintf("id=%s image=%s runtime=%s", e.ContainerID, e.Image, e.Runtime)
	case *eventsapi.TaskCreate:
		out = "id=" + e.ContainerID
	case *eventsapi.TaskStart:
		out = "id=" + e.ContainerID
	case *eventsapi.TaskDelete:
		out = fmt.Sprintf("id=%s pid=%d status=%d", e.ContainerID, e.Pid, e.ExitStatus)
	case *eventsapi.ContainerUpdate:
		out = "id=" + e.ContainerID
	case *eventsapi.ContainerDelete:
		out = fmt.Sprintf("id=%s image=%s", e.ContainerID, e.Image)
	case *eventsapi.SnapshotPrepare:
		out = fmt.Sprintf("key=%s parent=%s", e.Key, e.Parent)
	case *eventsapi.SnapshotCommit:
		out = fmt.Sprintf("key=%s name=%s", e.Key, e.Name)
	case *eventsapi.SnapshotRemove:
		out = "key=" + e.Key
	case *eventsapi.ImageUpdate:
		out = fmt.Sprintf("name=%s labels=%s", e.Name, e.Labels)
	case *eventsapi.ImageDelete:
		out = "name=" + e.Name
	case *eventsapi.NamespaceCreate:
		out = fmt.Sprintf("name=%s labels=%s", e.Name, e.Labels)
	case *eventsapi.NamespaceUpdate:
		out = fmt.Sprintf("name=%s labels=%s", e.Name, e.Labels)
	case *eventsapi.NamespaceDelete:
		out = "name=" + e.Name
	case *eventsapi.RuntimeCreate:
		mounts := []string{}
		for _, m := range e.RootFS {
			mounts = append(mounts, fmt.Sprintf("type=%s:src=%s", m.Type, m.Source))
		}
		out = fmt.Sprintf("id=%s bundle=%s rootfs=%s checkpoint=%s", e.ID, e.Bundle, strings.Join(mounts, ","), e.Checkpoint)
	case *eventsapi.RuntimeEvent:
		out = fmt.Sprintf("id=%s type=%s pid=%d status=%d exited=%s", e.ID, e.Type, e.Pid, e.ExitStatus, e.ExitedAt)
	case *eventsapi.RuntimeDelete:
		out = fmt.Sprintf("id=%s runtime=%s status=%d exited=%s", e.ID, e.Runtime, e.ExitStatus, e.ExitedAt)
	default:
		out = env.Event.TypeUrl
	}

	return out, nil
}
