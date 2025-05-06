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

package integration

import (
	"fmt"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/continuity/fs"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
)

// TestIssue10467 tests the migration of sandboxes into the proper bucket. Prior to v1.7.21, the
// sandboxes were stored incorrectly in the root bucket. In order to verify the migration, a v1.7.20
// must run and create a sandbox, then check the migration after upgrading to a newer version.
func TestIssue10467(t *testing.T) {
	latestVersion := "v1.7.20"

	releaseBinDir := t.TempDir()

	downloadReleaseBinary(t, releaseBinDir, latestVersion)

	t.Logf("Install config for release %s", latestVersion)
	workDir := t.TempDir()
	oneSevenCtrdConfig(t, releaseBinDir, workDir)

	t.Log("Starting the previous release's containerd")
	previousCtrdBinPath := filepath.Join(releaseBinDir, "bin", "containerd")
	previousProc := newCtrdProc(t, previousCtrdBinPath, workDir, []string{"ENABLE_CRI_SANDBOXES=yes"})

	boltdbPath := filepath.Join(workDir, "root", "io.containerd.metadata.v1.bolt", "meta.db")

	ctrdLogPath := previousProc.logPath()
	t.Cleanup(func() {
		if t.Failed() {
			dumpFileContent(t, ctrdLogPath)
		}
	})

	require.NoError(t, previousProc.isReady())

	needToCleanup := true
	t.Cleanup(func() {
		if t.Failed() && needToCleanup {
			t.Logf("Try to cleanup leaky pods")
			cleanupPods(t, previousProc.criRuntimeService(t))
		}
	})

	t.Log("Prepare pods for current release")
	upgradeCaseFunc, hookFunc := shouldManipulateContainersInPodAfterUpgrade("")(t, 2, previousProc.criRuntimeService(t), previousProc.criImageService(t))
	needToCleanup = false
	require.Nil(t, hookFunc)

	t.Log("Gracefully stop previous release's containerd process")
	require.NoError(t, previousProc.kill(syscall.SIGTERM))
	require.NoError(t, previousProc.wait(5*time.Minute))

	t.Logf("%s should have bucket k8s.io in root", boltdbPath)
	db, err := bbolt.Open(boltdbPath, 0600, &bbolt.Options{ReadOnly: true})
	require.NoError(t, err)
	require.NoError(t, db.View(func(tx *bbolt.Tx) error {
		if tx.Bucket([]byte("k8s.io")) == nil {
			return fmt.Errorf("expected k8s.io bucket")
		}
		return nil
	}))
	require.NoError(t, db.Close())

	t.Log("Install default config for current release")
	currentReleaseCtrdDefaultConfig(t, workDir)

	t.Log("Starting the current release's containerd")
	currentProc := newCtrdProc(t, "containerd", workDir, nil)
	require.NoError(t, currentProc.isReady())

	t.Cleanup(func() {
		t.Log("Cleanup all the pods")
		cleanupPods(t, currentProc.criRuntimeService(t))

		t.Log("Stopping current release's containerd process")
		require.NoError(t, currentProc.kill(syscall.SIGTERM))
		require.NoError(t, currentProc.wait(5*time.Minute))
	})

	t.Logf("%s should not have bucket k8s.io in root after restart", boltdbPath)
	copiedBoltdbPath := filepath.Join(t.TempDir(), "meta.db.new")
	require.NoError(t, fs.CopyFile(copiedBoltdbPath, boltdbPath))

	db, err = bbolt.Open(copiedBoltdbPath, 0600, &bbolt.Options{ReadOnly: true})
	require.NoError(t, err)
	require.NoError(t, db.View(func(tx *bbolt.Tx) error {
		if tx.Bucket([]byte("k8s.io")) != nil {
			return fmt.Errorf("unexpected k8s.io bucket")
		}
		return nil
	}))
	require.NoError(t, db.Close())

	t.Log("Verifing")
	upgradeCaseFunc(t, currentProc.criRuntimeService(t), currentProc.criImageService(t))
}
