package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	eventsapi "github.com/containerd/containerd/api/services/events"
	"github.com/containerd/containerd/api/types/event"
	"github.com/gogo/protobuf/proto"
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

		events, err := eventsClient.EventStream(ctx, &eventsapi.EventStreamRequest{})
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

func getEventOutput(evt *event.Envelope) (string, error) {

	out := ""
	switch evt.Event.TypeUrl {
	case "types.containerd.io/containerd.v1.types.event.ContainerCreate":
		e := &event.ContainerCreate{}
		if err := proto.Unmarshal(evt.Event.Value, e); err != nil {
			return out, err
		}
		out = fmt.Sprintf("id=%s image=%s runtime=%s", e.ContainerID, e.Image, e.Runtime)
	case "types.containerd.io/containerd.v1.types.event.TaskCreate":
		e := &event.TaskCreate{}
		if err := proto.Unmarshal(evt.Event.Value, e); err != nil {
			return out, err
		}
		out = "id=" + e.ContainerID
	case "types.containerd.io/containerd.v1.types.event.TaskStart":
		e := &event.TaskStart{}
		if err := proto.Unmarshal(evt.Event.Value, e); err != nil {
			return out, err
		}
		out = fmt.Sprintf("id=%s", e.ContainerID)
	case "types.containerd.io/containerd.v1.types.event.TaskDelete":
		e := &event.TaskDelete{}
		if err := proto.Unmarshal(evt.Event.Value, e); err != nil {
			return out, err
		}
		out = fmt.Sprintf("id=%s", e.ContainerID)
	case "types.containerd.io/containerd.v1.types.event.ContainerUpdate":
		e := &event.ContainerUpdate{}
		if err := proto.Unmarshal(evt.Event.Value, e); err != nil {
			return out, err
		}
		out = fmt.Sprintf("id=%s", e.ContainerID)
	case "types.containerd.io/containerd.v1.types.event.ContainerDelete":
		e := &event.ContainerDelete{}
		if err := proto.Unmarshal(evt.Event.Value, e); err != nil {
			return out, err
		}
		out = fmt.Sprintf("id=%s", e.ContainerID)
	case "types.containerd.io/containerd.v1.types.event.SnapshotPrepare":
		e := &event.SnapshotPrepare{}
		if err := proto.Unmarshal(evt.Event.Value, e); err != nil {
			return out, err
		}
		out = fmt.Sprintf("key=%s parent=%s", e.Key, e.Parent)
	case "types.containerd.io/containerd.v1.types.event.SnapshotCommit":
		e := &event.SnapshotCommit{}
		if err := proto.Unmarshal(evt.Event.Value, e); err != nil {
			return out, err
		}
		out = fmt.Sprintf("key=%s name=%s", e.Key, e.Name)
	case "types.containerd.io/containerd.v1.types.event.SnapshotRemove":
		e := &event.SnapshotRemove{}
		if err := proto.Unmarshal(evt.Event.Value, e); err != nil {
			return out, err
		}
		out = fmt.Sprintf("key=%s", e.Key)
	case "types.containerd.io/containerd.v1.types.event.ImagePut":
		e := &event.ImagePut{}
		if err := proto.Unmarshal(evt.Event.Value, e); err != nil {
			return out, err
		}
		out = fmt.Sprintf("name=%s labels=%s", e.Name, e.Labels)
	case "types.containerd.io/containerd.v1.types.event.ImageDelete":
		e := &event.ImageDelete{}
		if err := proto.Unmarshal(evt.Event.Value, e); err != nil {
			return out, err
		}
		out = fmt.Sprintf("name=%s", e.Name)
	case "types.containerd.io/containerd.v1.types.event.NamespaceCreate":
		e := &event.NamespaceCreate{}
		if err := proto.Unmarshal(evt.Event.Value, e); err != nil {
			return out, err
		}
		out = fmt.Sprintf("name=%s labels=%s", e.Name, e.Labels)
	case "types.containerd.io/containerd.v1.types.event.NamespaceUpdate":
		e := &event.NamespaceUpdate{}
		if err := proto.Unmarshal(evt.Event.Value, e); err != nil {
			return out, err
		}
		out = fmt.Sprintf("name=%s labels=%s", e.Name, e.Labels)
	case "types.containerd.io/containerd.v1.types.event.NamespaceDelete":
		e := &event.NamespaceDelete{}
		if err := proto.Unmarshal(evt.Event.Value, e); err != nil {
			return out, err
		}
		out = fmt.Sprintf("name=%s", e.Name)
	case "types.containerd.io/containerd.v1.types.event.RuntimeCreate":
		e := &event.RuntimeCreate{}
		if err := proto.Unmarshal(evt.Event.Value, e); err != nil {
			return out, err
		}
		mounts := []string{}
		for _, m := range e.RootFS {
			mounts = append(mounts, fmt.Sprintf("type=%s:src=%s", m.Type, m.Source))
		}
		out = fmt.Sprintf("id=%s bundle=%s rootfs=%s checkpoint=%s", e.ID, e.Bundle, strings.Join(mounts, ","), e.Checkpoint)
	case "types.containerd.io/containerd.v1.types.event.RuntimeEvent":
		e := &event.RuntimeEvent{}
		if err := proto.Unmarshal(evt.Event.Value, e); err != nil {
			return out, err
		}
		out = fmt.Sprintf("id=%s type=%s pid=%d status=%d exited=%s", e.ID, e.Type, e.Pid, e.ExitStatus, e.ExitedAt)
	case "types.containerd.io/containerd.v1.types.event.RuntimeDelete":
		e := &event.RuntimeDelete{}
		if err := proto.Unmarshal(evt.Event.Value, e); err != nil {
			return out, err
		}
		out = fmt.Sprintf("id=%s runtime=%s status=%d exited=%s", e.ID, e.Runtime, e.ExitStatus, e.ExitedAt)
	default:
		out = evt.Event.TypeUrl
	}

	return out, nil
}
