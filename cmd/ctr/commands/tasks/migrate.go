// +build linux

package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"strings"

	"github.com/checkpoint-restore/criu/lib/go/src/criu"
	"github.com/checkpoint-restore/criu/phaul/src/phaul"
	"github.com/pkg/errors"
	"github.com/urfave/cli"

	"github.com/containerd/containerd"
	taskApi "github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/linux/runctypes"
	digest "github.com/opencontainers/go-digest"
	spec "github.com/opencontainers/image-spec/specs-go/v1"
)

type ctrLocal struct {
	ctx               context.Context
	containerID       string
	localClient       *containerd.Client
	destClient        *containerd.Client
}

type ctrRemote struct {
	ctx         context.Context
	containerID string
	client      taskApi.TasksClient
}

func (r *ctrRemote) StartIter() error {
	_, err := r.client.StartIter(r.ctx, &taskApi.StartIterRequest{ContainerID: r.containerID})
	return err
}

func (r *ctrRemote) StopIter() error {
	_, err := r.client.StopIter(r.ctx, &taskApi.StopIterRequest{ContainerID: r.containerID})
	return err
}

func (r *ctrRemote) DoRestore() error {
	return nil
}

func (l *ctrLocal) PostDump() error {
	return nil
}

func (l *ctrLocal) DumpCopyRestore(cr *criu.Criu, cfg phaul.PhaulConfig, last_cln_images_dir string) error {
	task, err := getTask(l.ctx, l.localClient, l.containerID)
	if err != nil {
		return err
	}
	img, err := task.Checkpoint(l.ctx, func(r *containerd.CheckpointTaskInfo) error {
		r.Options = &runctypes.CheckpointOptions{
			PageServer: fmt.Sprintf("%s:%d", cfg.Addr, cfg.Port),
			ParentPath: last_cln_images_dir,
			Exit:       true,
		}
		return nil
	})
	if err != nil {
		return err
	}

	if err := copyCheckpoint(l.ctx, l.localClient.ContentStore(), l.destClient.ContentStore(), img.Target().Digest); err != nil {
		return err
	}

	newC, err := l.destClient.NewContainer(l.ctx, l.containerID, containerd.WithCheckpoint(img, l.containerID))
	if err != nil {
		return err
	}

	// FIXME: only support NullIO now
	newTask, err := newC.NewTask(l.ctx, containerd.NullIO, containerd.WithTaskCheckpoint(img))
	if err != nil {
		return err
	}

	return newTask.Start(l.ctx)

}

func copyBlob(ctx context.Context, src content.Store, dest content.Store, digest digest.Digest) error {
	ra, err := src.ReaderAt(ctx, digest)
	if err != nil {
		return err
	}
	defer ra.Close()
	return content.WriteBlob(ctx, dest, digest.String(), content.NewReader(ra), 0, digest)
}

func copyCheckpoint(ctx context.Context, src content.Store, dest content.Store, digest digest.Digest) error {
	if err := copyBlob(ctx, src, dest, digest); err != nil {
		return err
	}

	b, err := content.ReadBlob(ctx, src, digest)
	if err != nil {
		return err
	}
	var index spec.Index
	if err := json.Unmarshal(b, &index); err != nil {
		return err
	}

	for _, m := range index.Manifests {
		fmt.Printf("mediatype: %s\ndigest: %s\n", m.MediaType, m.Digest)
		if err := copyBlob(ctx, src, dest, m.Digest); err != nil {
			return err
		}

	}
	return nil
}

func pullImage(ctx context.Context, src *containerd.Client, destAddr string, id string) error {
	container, err := src.LoadContainer(ctx, id)
	if err != nil {
		return err
	}
	img, err := container.Image(ctx)
	if err != nil {
		return err
	}
	dest, err := containerd.New(destAddr)
	if err != nil {
		return err
	}
	defer dest.Close()
	_, err = dest.Pull(ctx, img.Name(), containerd.WithPullUnpack)
	return err
}

func getTask(ctx context.Context, client *containerd.Client, id string) (containerd.Task, error) {
	container, err := client.LoadContainer(ctx, id)
	if err != nil {
		return nil, err
	}
	return container.Task(ctx, nil)
}

func makePhaulConfig(ctx context.Context, src *containerd.Client, destAddr string, id string, wdir string) (phaul.PhaulConfig, error) {
	var cfg phaul.PhaulConfig
	task, err := getTask(ctx, src, id)
	if err != nil {
		return cfg, err
	}
	pid := task.Pid()

	destIP, _, err := net.SplitHostPort(strings.TrimPrefix(destAddr, "tcp://"))
	if err != nil {
		return cfg, err
	}

	dest, err := containerd.New(destAddr)
	if err != nil {
		return cfg, err
	}
	defer dest.Close()

	pageServer, err := dest.TaskService().CreatePageServer(ctx, &taskApi.CreatePageServerRequest{ContainerID: id})
	if err != nil {
		return cfg, err
	}

	return phaul.PhaulConfig{
		Pid:  int(pid),
		Addr: destIP,
		Port: int32(pageServer.Port),
		Wdir: wdir,
	}, nil
}

func makePhaulClient(ctx context.Context, src *containerd.Client, destAddr string, id string, wdir string) (*phaul.PhaulClient, error) {
	dest, err := containerd.New(destAddr)
	if err != nil {
		return nil, err
	}
	ctrL := &ctrLocal{ctx: ctx, containerID: id, localClient: src, destClient: dest}

	remote := &ctrRemote{
		ctx: ctx,
		containerID: id,
		client: dest.TaskService(),
	}

	cfg, err := makePhaulConfig(ctx, src, destAddr, id, wdir)
	if err != nil {
		return nil, err
	}

	pc, err := phaul.MakePhaulClient(ctrL, remote, cfg)
	if err != nil {
		return nil, err
	}
	return pc, nil
}

var migrateCommand = cli.Command{
	Name:  "migrate",
	Usage: "migrate a running container",
	ArgsUsage: `<container-id>

Where "<container-id>" is the name for the instance of the container to be
migrated.`,
	Description: `The migrate command migrates the container.`,
	Flags: []cli.Flag{
		cli.StringFlag{Name: "dest-containerd-address", Value: "", Usage: "dest containerd address"},
	},
	Action: func(clicontext *cli.Context) error {
		id := clicontext.Args().First()
		if id == "" {
			return errors.New("container id must be provided")
		}
		destAddr := clicontext.String("dest-containerd-address")
		if destAddr == "" {
			return errors.New("dest containerd address must be provided")
		}

		client, ctx, cancel, err := commands.NewClient(clicontext)
		if err != nil {
			return err
		}
		defer cancel()

		if err = pullImage(ctx, client, destAddr, id); err != nil {
			return err
		}

		wdir, err := ioutil.TempDir("", "ctd-criu.work")
		if err != nil {
			return err
		}
		defer func() {
			if err == nil {
				os.RemoveAll(wdir)
			}
		}()

		pc, err := makePhaulClient(ctx, client, destAddr, id, wdir)
		if err != nil {
			return err
		}
		return pc.Migrate()
	},
}
