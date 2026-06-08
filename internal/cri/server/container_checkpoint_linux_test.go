//go:build linux

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

package server

import (
	"context"
	"sync"
	"testing"

	"github.com/containerd/log"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

type testLogHook struct {
	mu      sync.Mutex
	entries []string
}

func (h *testLogHook) Levels() []logrus.Level {
	return []logrus.Level{logrus.WarnLevel}
}

func (h *testLogHook) Fire(entry *logrus.Entry) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = append(h.entries, entry.Message)
	return nil
}

func TestFilterAndMergeAnnotations(t *testing.T) {
	for desc, tc := range map[string]struct {
		checkpointAnnotations map[string]string
		createAnnotations     map[string]string
		expectedAnnotations   map[string]string
		expectedWarnings      []string
	}{
		"cdi denied prefix boundaries": {
			checkpointAnnotations: map[string]string{
				"cdi.k8s.io/device":   "gpu",
				"cdi.k8s.io/":         "true",
				"cdi.k8s.io":          "true",
				"safe.org/cdi.k8s.io": "ignored",
				"other":               "val",
			},
			expectedAnnotations: map[string]string{
				"safe.org/cdi.k8s.io": "ignored",
				"other":               "val",
			},
			expectedWarnings: []string{
				`Denying annotation "cdi.k8s.io/device" in checkpoint restore`,
				`Denying annotation "cdi.k8s.io/" in checkpoint restore`,
				`Denying annotation "cdi.k8s.io" in checkpoint restore`,
			},
		},

		"createAnnotations update kubernetes metadata if present in both": {
			checkpointAnnotations: map[string]string{
				"io.kubernetes.container.hash":         "old-hash",
				"io.kubernetes.container.restartCount": "1",
				"safe.annotation":                      "2",
			},
			createAnnotations: map[string]string{
				"io.kubernetes.container.hash":         "new-hash",
				"io.kubernetes.container.restartCount": "2",
			},
			expectedAnnotations: map[string]string{
				"io.kubernetes.container.hash":         "new-hash",
				"io.kubernetes.container.restartCount": "2",
				"safe.annotation":                      "2",
			},
		},
	} {
		t.Run(desc, func(t *testing.T) {
			logger := logrus.New()
			logger.SetLevel(logrus.WarnLevel)
			hook := &testLogHook{}
			logger.AddHook(hook)
			ctx := log.WithLogger(context.Background(), logrus.NewEntry(logger))

			res := filterAndMergeAnnotations(
				ctx,
				tc.checkpointAnnotations,
				tc.createAnnotations,
			)

			assert.Equal(t, tc.expectedAnnotations, res)
			assert.ElementsMatch(t, tc.expectedWarnings, hook.entries)
		})
	}
}
