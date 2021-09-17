// +build linux

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

package service

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	goruntime "runtime"
	"syscall"
	"time"

	"github.com/containerd/cgroups"
	cgroupsv2 "github.com/containerd/cgroups/v2"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/pkg/process"
	"github.com/containerd/containerd/pkg/schedcore"
	"github.com/containerd/containerd/runtime/v2/runc"
	"github.com/containerd/containerd/runtime/v2/runc/options"
	"github.com/containerd/containerd/runtime/v2/shim"
	taskAPI "github.com/containerd/containerd/runtime/v2/task"
	runcC "github.com/containerd/go-runc"
	"github.com/containerd/typeurl"
	"github.com/gogo/protobuf/proto"
	ptypes "github.com/gogo/protobuf/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	exec "golang.org/x/sys/execabs"
	"golang.org/x/sys/unix"
)

// NewShimManager returns an implementation of the shim manager
// using runc
func NewShimManager(id string) shim.Shim {
	return &manager{
		id: id,
	}
}

// group labels specifies how the shim groups services.
// currently supports a runc.v2 specific .group label and the
// standard k8s pod label.  Order matters in this list
var groupLabels = []string{
	"io.containerd.runc.v2.group",
	"io.kubernetes.cri.sandbox-id",
}

type spec struct {
	Annotations map[string]string `json:"annotations,omitempty"`
}

type manager struct {
	id string
}

func newCommand(ctx context.Context, id, containerdBinary, containerdAddress, containerdTTRPCAddress string) (*exec.Cmd, error) {
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	args := []string{
		"-namespace", ns,
		"-id", id,
		"-address", containerdAddress,
	}
	cmd := exec.Command(self, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "GOMAXPROCS=4")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	return cmd, nil
}

func readSpec() (*spec, error) {
	f, err := os.Open("config.json")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var s spec
	if err := json.NewDecoder(f).Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (manager) StartShim(ctx context.Context, opts shim.StartOpts) (_ string, retErr error) {
	cmd, err := newCommand(ctx, opts.ID, opts.ContainerdBinary, opts.Address, opts.TTRPCAddress)
	if err != nil {
		return "", err
	}
	grouping := opts.ID
	spec, err := readSpec()
	if err != nil {
		return "", err
	}
	for _, group := range groupLabels {
		if groupID, ok := spec.Annotations[group]; ok {
			grouping = groupID
			break
		}
	}
	address, err := shim.SocketAddress(ctx, opts.Address, grouping)
	if err != nil {
		return "", err
	}

	socket, err := shim.NewSocket(address)
	if err != nil {
		// the only time where this would happen is if there is a bug and the socket
		// was not cleaned up in the cleanup method of the shim or we are using the
		// grouping functionality where the new process should be run with the same
		// shim as an existing container
		if !shim.SocketEaddrinuse(err) {
			return "", errors.Wrap(err, "create new shim socket")
		}
		if shim.CanConnect(address) {
			if err := shim.WriteAddress("address", address); err != nil {
				return "", errors.Wrap(err, "write existing socket for shim")
			}
			return address, nil
		}
		if err := shim.RemoveSocket(address); err != nil {
			return "", errors.Wrap(err, "remove pre-existing socket")
		}
		if socket, err = shim.NewSocket(address); err != nil {
			return "", errors.Wrap(err, "try create new shim socket 2x")
		}
	}
	defer func() {
		if retErr != nil {
			socket.Close()
			_ = shim.RemoveSocket(address)
		}
	}()

	// make sure that reexec shim-v2 binary use the value if need
	if err := shim.WriteAddress("address", address); err != nil {
		return "", err
	}

	f, err := socket.File()
	if err != nil {
		return "", err
	}

	cmd.ExtraFiles = append(cmd.ExtraFiles, f)

	goruntime.LockOSThread()
	if os.Getenv("SCHED_CORE") != "" {
		if err := schedcore.Create(schedcore.ProcessGroup); err != nil {
			return "", errors.Wrap(err, "enable sched core support")
		}
	}

	if err := cmd.Start(); err != nil {
		f.Close()
		return "", err
	}

	goruntime.UnlockOSThread()

	defer func() {
		if retErr != nil {
			cmd.Process.Kill()
		}
	}()
	// make sure to wait after start
	go cmd.Wait()
	if data, err := ioutil.ReadAll(os.Stdin); err == nil {
		if len(data) > 0 {
			var any ptypes.Any
			if err := proto.Unmarshal(data, &any); err != nil {
				return "", err
			}
			v, err := typeurl.UnmarshalAny(&any)
			if err != nil {
				return "", err
			}
			if opts, ok := v.(*options.Options); ok {
				if opts.ShimCgroup != "" {
					if cgroups.Mode() == cgroups.Unified {
						cg, err := cgroupsv2.LoadManager("/sys/fs/cgroup", opts.ShimCgroup)
						if err != nil {
							return "", errors.Wrapf(err, "failed to load cgroup %s", opts.ShimCgroup)
						}
						if err := cg.AddProc(uint64(cmd.Process.Pid)); err != nil {
							return "", errors.Wrapf(err, "failed to join cgroup %s", opts.ShimCgroup)
						}
					} else {
						cg, err := cgroups.Load(cgroups.V1, cgroups.StaticPath(opts.ShimCgroup))
						if err != nil {
							return "", errors.Wrapf(err, "failed to load cgroup %s", opts.ShimCgroup)
						}
						if err := cg.Add(cgroups.Process{
							Pid: cmd.Process.Pid,
						}); err != nil {
							return "", errors.Wrapf(err, "failed to join cgroup %s", opts.ShimCgroup)
						}
					}
				}
			}
		}
	}
	if err := shim.AdjustOOMScore(cmd.Process.Pid); err != nil {
		return "", errors.Wrap(err, "failed to adjust OOM score for shim")
	}
	return address, nil
}

func (m manager) Cleanup(ctx context.Context) (*taskAPI.DeleteResponse, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(filepath.Dir(cwd), m.id)
	ns, err := namespaces.NamespaceRequired(ctx)
	if err != nil {
		return nil, err
	}
	runtime, err := runc.ReadRuntime(path)
	if err != nil {
		return nil, err
	}
	opts, err := runc.ReadOptions(path)
	if err != nil {
		return nil, err
	}
	root := process.RuncRoot
	if opts != nil && opts.Root != "" {
		root = opts.Root
	}

	r := process.NewRunc(root, path, ns, runtime, "", false)
	if err := r.Delete(ctx, m.id, &runcC.DeleteOpts{
		Force: true,
	}); err != nil {
		logrus.WithError(err).Warn("failed to remove runc container")
	}
	if err := mount.UnmountAll(filepath.Join(path, "rootfs"), 0); err != nil {
		logrus.WithError(err).Warn("failed to cleanup rootfs mount")
	}
	return &taskAPI.DeleteResponse{
		ExitedAt:   time.Now(),
		ExitStatus: 128 + uint32(unix.SIGKILL),
	}, nil
}
