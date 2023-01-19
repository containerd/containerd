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
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

var densityCommand = cli.Command{
	Name:  "density",
	Usage: "Stress tests density of containers running on a system",
	Flags: []cli.Flag{
		cli.IntFlag{
			Name:  "count",
			Usage: "Number of containers to run",
			Value: 10,
		},
	},
	Action: func(cliContext *cli.Context) error {
		var (
			pids  []uint32
			count = cliContext.Int("count")
		)

		if count < 1 {
			return errors.New("count cannot be less than one")
		}

		config := config{
			Address:     cliContext.GlobalString("address"),
			Duration:    cliContext.GlobalDuration("duration"),
			Concurrency: cliContext.GlobalInt("concurrent"),
			Exec:        cliContext.GlobalBool("exec"),
			Image:       cliContext.GlobalString("image"),
			JSON:        cliContext.GlobalBool("json"),
			Metrics:     cliContext.GlobalString("metrics"),
			Snapshotter: cliContext.GlobalString("snapshotter"),
		}
		client, err := config.newClient()
		if err != nil {
			return err
		}
		defer client.Close()
		ctx := namespaces.WithNamespace(context.Background(), "density")
		if err := cleanup(ctx, client); err != nil {
			return err
		}
		logrus.Infof("pulling %s", config.Image)
		image, err := client.Pull(ctx, config.Image, containerd.WithPullUnpack, containerd.WithPullSnapshotter(config.Snapshotter))
		if err != nil {
			return err
		}
		logrus.Info("generating spec from image")

		s := make(chan os.Signal, 1)
		signal.Notify(s, syscall.SIGTERM, syscall.SIGINT)

	loop:
		for i := 0; i < count+1; i++ {
			select {
			case <-s:
				break loop
			default:
				id := fmt.Sprintf("density-%d", i)

				c, err := client.NewContainer(ctx, id,
					containerd.WithSnapshotter(config.Snapshotter),
					containerd.WithNewSnapshot(id, image),
					containerd.WithNewSpec(
						oci.WithImageConfig(image),
						oci.WithProcessArgs("sleep", "120m"),
						oci.WithUsername("games")),
				)
				if err != nil {
					return err
				}
				defer c.Delete(ctx, containerd.WithSnapshotCleanup)

				t, err := c.NewTask(ctx, cio.NullIO)
				if err != nil {
					return err
				}
				defer t.Delete(ctx, containerd.WithProcessKill)
				if err := t.Start(ctx); err != nil {
					return err
				}
				pids = append(pids, t.Pid())
			}
		}
		var results struct {
			PSS             int `json:"pss"`
			RSS             int `json:"rss"`
			PSSPerContainer int `json:"pssPerContainer"`
			RSSPerContainer int `json:"rssPerContainer"`
		}

		for _, pid := range pids {
			shimPid, err := getppid(int(pid))
			if err != nil {
				return err
			}
			smaps, err := getMaps(shimPid)
			if err != nil {
				return err
			}
			results.RSS += smaps["Rss:"]
			results.PSS += smaps["Pss:"]
		}
		results.PSSPerContainer = results.PSS / count
		results.RSSPerContainer = results.RSS / count

		return json.NewEncoder(os.Stdout).Encode(results)
	},
}

func getMaps(pid int) (map[string]int, error) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/smaps", pid))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var (
		smaps = make(map[string]int)
		s     = bufio.NewScanner(f)
	)
	for s.Scan() {
		var (
			fields = strings.Fields(s.Text())
			name   = fields[0]
		)
		if len(fields) < 2 {
			continue
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		smaps[name] += n
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return smaps, nil
}

func getppid(pid int) (int, error) {
	bytes, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, err
	}
	s, err := parseStat(string(bytes))
	if err != nil {
		return 0, err
	}
	return int(s.PPID), nil
}

// Stat represents the information from /proc/[pid]/stat, as
// described in proc(5) with names based on the /proc/[pid]/status
// fields.
type Stat struct {
	// PID is the process ID.
	PID uint

	// Name is the command run by the process.
	Name string

	// StartTime is the number of clock ticks after system boot (since
	// Linux 2.6).
	StartTime uint64
	// Parent process ID.
	PPID uint
}

func parseStat(data string) (stat Stat, err error) {
	//nolint:dupword
	// From proc(5), field 2 could contain space and is inside `(` and `)`.
	// The following is an example:
	// 89653 (gunicorn: maste) S 89630 89653 89653 0 -1 4194560 29689 28896 0 3 146 32 76 19 20 0 1 0 2971844 52965376 3920 18446744073709551615 1 1 0 0 0 0 0 16781312 137447943 0 0 0 17 1 0 0 0 0 0 0 0 0 0 0 0 0 0
	i := strings.LastIndex(data, ")")
	if i <= 2 || i >= len(data)-1 {
		return stat, fmt.Errorf("invalid stat data: %q", data)
	}

	val, name, ok := strings.Cut(data[:i], "(")
	if !ok {
		return stat, fmt.Errorf("invalid stat data: %q", data)
	}

	stat.Name = name
	_, err = fmt.Sscanf(val, "%d", &stat.PID)
	if err != nil {
		return stat, err
	}

	// parts indexes should be offset by 3 from the field number given
	// proc(5), because parts is zero-indexed and we've removed fields
	// one (PID) and two (Name) in the paren-split.
	parts := strings.Split(data[i+2:], " ")
	fmt.Sscanf(parts[22-3], "%d", &stat.StartTime)
	fmt.Sscanf(parts[4-3], "%d", &stat.PPID)
	return stat, nil
}
