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

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/containerd/containerd/v2/pkg/failpoint"
	"github.com/containerd/continuity"
	"github.com/containernetworking/cni/pkg/invoke"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/version"
)

const delegatedPlugin = "bridge"

type netConf struct {
	RuntimeConfig struct {
		PodAnnotations inheritedPodAnnotations `json:"io.kubernetes.cri.pod-annotations"`
	} `json:"runtimeConfig,omitempty"`
}

type inheritedPodAnnotations struct {
	// FailpointConfPath represents filepath of failpoint settings.
	FailpointConfPath string `json:"failpoint.cni.containerd.io/confpath,omitempty"`
}

// failpointConf is used to describe cmdAdd/cmdDel/cmdCheck command's failpoint.
type failpointConf struct {
	Add   string `json:"cmdAdd,omitempty"`
	Del   string `json:"cmdDel,omitempty"`
	Check string `json:"cmdCheck,omitempty"`
}

func main() {
	skel.PluginMain(cmdAdd, cmdCheck, cmdDel, version.All, "bridge with failpoint support")
}

func cmdAdd(args *skel.CmdArgs) error {
	if err := handleFailpoint(args, "ADD"); err != nil {
		return err
	}

	result, err := invoke.DelegateAdd(context.TODO(), delegatedPlugin, args.StdinData, nil)
	if err != nil {
		return err
	}
	return result.Print()
}

func cmdCheck(args *skel.CmdArgs) error {
	if err := handleFailpoint(args, "CHECK"); err != nil {
		return err
	}

	return invoke.DelegateCheck(context.TODO(), delegatedPlugin, args.StdinData, nil)
}

func cmdDel(args *skel.CmdArgs) error {
	if err := handleFailpoint(args, "DEL"); err != nil {
		return err
	}

	return invoke.DelegateDel(context.TODO(), delegatedPlugin, args.StdinData, nil)
}

func handleFailpoint(args *skel.CmdArgs, cmdKind string) error {
	var conf netConf
	if err := json.Unmarshal(args.StdinData, &conf); err != nil {
		return fmt.Errorf("failed to parse network configuration: %w", err)
	}

	confPath := conf.RuntimeConfig.PodAnnotations.FailpointConfPath
	if len(confPath) == 0 {
		return nil
	}

	control, err := newFailpointControl(confPath)
	if err != nil {
		return err
	}

	evalFn, err := control.delegatedEvalFn(cmdKind)
	if err != nil {
		return err
	}
	return evalFn()
}

type failpointControl struct {
	confPath string
}

func newFailpointControl(confPath string) (*failpointControl, error) {
	if !filepath.IsAbs(confPath) {
		return nil, fmt.Errorf("failpoint confPath(%s) is required to be absolute", confPath)
	}

	return &failpointControl{
		confPath: confPath,
	}, nil
}

func (c *failpointControl) delegatedEvalFn(cmdKind string) (failpoint.EvalFn, error) {
	var resFn failpoint.EvalFn = nopEvalFn

	if err := c.updateTx(func(conf *failpointConf) error {
		var fpStr *string

		switch cmdKind {
		case "ADD":
			fpStr = &conf.Add
		case "DEL":
			fpStr = &conf.Del
		case "CHECK":
			fpStr = &conf.Check
		}

		if fpStr == nil || *fpStr == "" {
			return nil
		}

		fp, err := failpoint.NewFailpoint(cmdKind, *fpStr)
		if err != nil {
			return fmt.Errorf("failed to parse failpoint %s: %w", *fpStr, err)
		}

		resFn = fp.DelegatedEval()

		*fpStr = fp.Marshal()
		return nil

	}); err != nil {
		return nil, err
	}
	return resFn, nil
}

func (c *failpointControl) updateTx(updateFn func(conf *failpointConf) error) error {
	f, err := os.OpenFile(c.confPath, os.O_RDWR, 0666)
	if err != nil {
		return fmt.Errorf("failed to open confPath %s: %w", c.confPath, err)
	}
	defer f.Close()

	if err := flock(f.Fd()); err != nil {
		return fmt.Errorf("failed to lock failpoint setting %s: %w", c.confPath, err)
	}
	defer unflock(f.Fd())

	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("failed to read failpoint setting %s: %w", c.confPath, err)
	}

	var conf failpointConf
	if err := json.Unmarshal(data, &conf); err != nil {
		return fmt.Errorf("failed to unmarshal failpoint conf %s: %w", string(data), err)
	}

	if err := updateFn(&conf); err != nil {
		return err
	}

	data, err = json.Marshal(conf)
	if err != nil {
		return fmt.Errorf("failed to marshal failpoint conf: %w", err)
	}
	return continuity.AtomicWriteFile(c.confPath, data, 0666)
}

func nopEvalFn() error {
	return nil
}

func flock(fd uintptr) error {
	return syscall.Flock(int(fd), syscall.LOCK_EX)
}

func unflock(fd uintptr) error {
	return syscall.Flock(int(fd), syscall.LOCK_UN)
}
