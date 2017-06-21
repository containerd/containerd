package events

import (
	"path"
	"strings"

	"github.com/containerd/containerd/api/types/event"
	"github.com/gogo/protobuf/proto"
	protobuf "github.com/gogo/protobuf/types"
	"github.com/pkg/errors"
)

func getUrl(name string) string {
	base := "types.containerd.io"
	return path.Join(base, strings.Join([]string{
		"containerd",
		EventVersion,
		"types",
		"event",
		name,
	}, "."))
}

func convertToAny(evt Event) (*protobuf.Any, error) {
	url := ""
	var pb proto.Message
	switch v := evt.(type) {
	case event.ContainerCreate:
		url = getUrl("ContainerCreate")
		pb = &v
	case event.ContainerDelete:
		url = getUrl("ContainerDelete")
		pb = &v
	case event.TaskCreate:
		url = getUrl("TaskCreate")
		pb = &v
	case event.TaskStart:
		url = getUrl("TaskStart")
		pb = &v
	case event.TaskDelete:
		url = getUrl("TaskDelete")
		pb = &v
	case event.ContentDelete:
		url = getUrl("ContentDelete")
		pb = &v
	case event.SnapshotPrepare:
		url = getUrl("SnapshotPrepare")
		pb = &v
	case event.SnapshotCommit:
		url = getUrl("SnapshotCommit")
		pb = &v
	case event.SnapshotRemove:
		url = getUrl("SnapshotRemove")
		pb = &v
	case event.ImageUpdate:
		url = getUrl("ImageUpdate")
		pb = &v
	case event.ImageDelete:
		url = getUrl("ImageDelete")
		pb = &v
	case event.NamespaceCreate:
		url = getUrl("NamespaceCreate")
		pb = &v
	case event.NamespaceUpdate:
		url = getUrl("NamespaceUpdate")
		pb = &v
	case event.NamespaceDelete:
		url = getUrl("NamespaceDelete")
		pb = &v
	case event.RuntimeCreate:
		url = getUrl("RuntimeCreate")
		pb = &v
	case event.RuntimeEvent:
		url = getUrl("RuntimeEvent")
		pb = &v
	case event.RuntimeDelete:
		url = getUrl("RuntimeDelete")
		pb = &v
	default:
		return nil, errors.Errorf("unsupported event type: %T", v)
	}

	val, err := proto.Marshal(pb)
	if err != nil {
		return nil, err
	}

	return &protobuf.Any{
		TypeUrl: url,
		Value:   val,
	}, nil
}
