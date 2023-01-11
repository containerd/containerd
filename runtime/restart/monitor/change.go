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

package monitor

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"syscall"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/runtime/restart"
	"github.com/sirupsen/logrus"
)

type startChange struct {
	container containerd.Container
	logURI    string
	count     int

	// Deprecated(in release 1.5): but recognized now, prefer to use logURI
	logPath string
}

func (s *startChange) apply(ctx context.Context, client *containerd.Client) error {
	log := cio.NullIO

	if s.logURI != "" {
		uri, err := url.Parse(s.logURI)
		if err != nil {
			return fmt.Errorf("failed to parse %v into url: %w", s.logURI, err)
		}
		log = cio.LogURI(uri)
	} else if s.logPath != "" {
		log = cio.LogFile(s.logPath)
	}

	if s.logURI != "" && s.logPath != "" {
		logrus.Warnf("LogPathLabel=%v has been deprecated, using LogURILabel=%v",
			s.logPath, s.logURI)
	}

	task, err := s.container.Task(ctx, nil)
	if err == nil {
		if status, err := task.Status(ctx); err == nil {
			switch status.Status {
			case containerd.Stopped, containerd.Unknown:
			default:
				return nil
			}
		}
		task.Delete(ctx)
	}

	if s.count > 0 {
		labels := map[string]string{
			restart.CountLabel: strconv.Itoa(s.count),
		}
		opt := containerd.WithAdditionalContainerLabels(labels)
		if err := s.container.Update(ctx, containerd.UpdateContainerOpts(opt)); err != nil {
			return err
		}
	}

	task, err = s.container.NewTask(ctx, log)
	if err != nil {
		return err
	}
	return task.Start(ctx)
}

type stopChange struct {
	container containerd.Container
}

func (s *stopChange) apply(ctx context.Context, client *containerd.Client) error {
	return killTask(ctx, s.container)
}

func killTask(ctx context.Context, container containerd.Container) error {
	task, err := container.Task(ctx, nil)
	if err == nil {
		wait, err := task.Wait(ctx)
		if err != nil {
			if _, derr := task.Delete(ctx); derr == nil {
				return nil
			}
			return err
		}
		if err := task.Kill(ctx, syscall.SIGKILL, containerd.WithKillAll); err != nil {
			if _, derr := task.Delete(ctx); derr == nil {
				return nil
			}
			return err
		}
		<-wait
		if _, err := task.Delete(ctx); err != nil {
			return err
		}
	}
	return nil
}
