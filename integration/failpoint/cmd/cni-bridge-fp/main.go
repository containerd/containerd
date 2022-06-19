//go:build linux
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

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/containerd/containerd/pkg/failpoint"
	"github.com/containerd/continuity"
	"github.com/sirupsen/logrus"
)

type inheritedPodAnnotations struct {
	// CNIFailpointControlStateDir is used to specify the location of
	// failpoint control setting. In that such stateDir, the failpoint
	// setting is stored in the json file named by
	// `${K8S_POD_NAMESPACE}-${K8S_POD_NAME}.json`. The detail of json file
	// is described by FailpointConf.
	CNIFailpointControlStateDir string `json:"cniFailpointControlStateDir,omitempty"`
}

// FailpointConf is used to describe cmdAdd/cmdDel/cmdCheck command's failpoint.
type FailpointConf struct {
	Add   string `json:"cmdAdd"`
	Del   string `json:"cmdDel"`
	Check string `json:"cmdCheck"`
}

type netConf struct {
	RuntimeConfig struct {
		PodAnnotations inheritedPodAnnotations `json:"io.kubernetes.cri.pod-annotations"`
	} `json:"runtimeConfig,omitempty"`
}

func main() {
	stdinData, err := ioutil.ReadAll(os.Stdin)
	if err != nil {
		logrus.Fatalf("failed to read stdin: %v", err)
	}

	var conf netConf
	if err := json.Unmarshal(stdinData, &conf); err != nil {
		logrus.Fatalf("failed to parse network configuration: %v", err)
	}

	cniCmd, ok := os.LookupEnv("CNI_COMMAND")
	if !ok {
		logrus.Fatal("required env CNI_COMMAND")
	}

	cniPath, ok := os.LookupEnv("CNI_PATH")
	if !ok {
		logrus.Fatal("required env CNI_PATH")
	}

	evalFn, err := buildFailpointEval(conf.RuntimeConfig.PodAnnotations.CNIFailpointControlStateDir, cniCmd)
	if err != nil {
		logrus.Fatalf("failed to build failpoint evaluate function: %v", err)
	}

	if err := evalFn(); err != nil {
		logrus.Fatalf("failpoint: %v", err)
	}

	cmd := exec.Command(filepath.Join(cniPath, "bridge"))
	cmd.Stdin = bytes.NewReader(stdinData)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		logrus.Fatalf("failed to start bridge cni plugin: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		logrus.Fatalf("failed to wait for bridge cni plugin: %v", err)
	}
}

// buildFailpointEval will read and update the failpoint setting and then
// return delegated failpoint evaluate function
func buildFailpointEval(stateDir string, cniCmd string) (failpoint.EvalFn, error) {
	cniArgs, ok := os.LookupEnv("CNI_ARGS")
	if !ok {
		return nopEvalFn, nil
	}

	target := buildPodFailpointFilepath(stateDir, cniArgs)
	if target == "" {
		return nopEvalFn, nil
	}

	f, err := os.OpenFile(target, os.O_RDWR, 0666)
	if err != nil {
		if os.IsNotExist(err) {
			return nopEvalFn, nil
		}
		return nil, fmt.Errorf("failed to open file %s: %w", target, err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, fmt.Errorf("failed to lock failpoint setting %s: %w", target, err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	data, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("failed to read failpoint setting %s: %w", target, err)
	}

	var conf FailpointConf
	if err := json.Unmarshal(data, &conf); err != nil {
		return nil, fmt.Errorf("failed to unmarshal failpoint conf %s: %w", string(data), err)
	}

	var fpStr *string
	switch cniCmd {
	case "ADD":
		fpStr = &conf.Add
	case "DEL":
		fpStr = &conf.Del
	case "CHECK":
		fpStr = &conf.Check
	}

	if fpStr == nil || *fpStr == "" {
		return nopEvalFn, nil
	}

	fp, err := failpoint.NewFailpoint(cniCmd, *fpStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse failpoint %s: %w", *fpStr, err)
	}

	evalFn := fp.DelegatedEval()

	*fpStr = fp.Marshal()

	data, err = json.Marshal(conf)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal failpoint conf: %w", err)
	}
	return evalFn, continuity.AtomicWriteFile(target, data, 0666)
}

// buildPodFailpointFilepath returns the expected failpoint setting filepath
// by Pod metadata.
func buildPodFailpointFilepath(stateDir, cniArgs string) string {
	args := cniArgsIntoKeyValue(cniArgs)

	res := make([]string, 0, 2)
	for _, key := range []string{"K8S_POD_NAMESPACE", "K8S_POD_NAME"} {
		v, ok := args[key]
		if !ok {
			break
		}
		res = append(res, v)
	}
	if len(res) != 2 {
		return ""
	}
	return filepath.Join(stateDir, strings.Join(res, "-")+".json")
}

// cniArgsIntoKeyValue converts the CNI ARGS from `key1=value1;key2=value2...`
// into key/value hashmap.
func cniArgsIntoKeyValue(envStr string) map[string]string {
	parts := strings.Split(envStr, ";")
	res := make(map[string]string, len(parts))

	for _, part := range parts {
		keyValue := strings.SplitN(part, "=", 2)
		if len(keyValue) != 2 {
			continue
		}

		res[keyValue[0]] = keyValue[1]
	}
	return res
}

func nopEvalFn() error {
	return nil
}
