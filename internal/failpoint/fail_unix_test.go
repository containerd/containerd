//go:build unix

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

package failpoint

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/fifo"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestPauseOnFIFO(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload []byte
		wantErr error
	}{
		{name: "signal", payload: []byte{1}},
		{name: "closed without signal", wantErr: io.EOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			path := filepath.Join(t.TempDir(), "gate.fifo")
			require.NoError(t, unix.Mkfifo(path, 0o600))
			done := startPauseOnFIFO(t, path)

			w, err := fifo.OpenFifo(ctx, path, os.O_WRONLY, 0)
			require.NoError(t, err)
			defer w.Close()

			select {
			case early := <-done:
				t.Fatalf("pause returned before a signal: %v", early)
			case <-time.After(500 * time.Millisecond):
			}

			if len(tc.payload) > 0 {
				_, err = w.Write(tc.payload)
				require.NoError(t, err)
			}
			require.NoError(t, w.Close())

			err = waitResult(ctx, t, done)
			if tc.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.wantErr)
				require.ErrorContains(t, err, path)
			}
		})
	}
}

func TestPauseOnFIFOInvalidPath(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setup   func(t *testing.T, path string) // nil leaves the path missing
		wantErr error
		wantMsg string
	}{
		{name: "missing path", wantErr: os.ErrNotExist},
		{
			name: "regular file",
			setup: func(t *testing.T, path string) {
				// The byte would look like a signal if the FIFO check were skipped.
				require.NoError(t, os.WriteFile(path, []byte{1}, 0o600))
			},
			wantMsg: "is not a FIFO",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			path := filepath.Join(t.TempDir(), "gate.fifo")
			if tc.setup != nil {
				tc.setup(t, path)
			}
			done := startPauseOnFIFO(t, path)

			err := waitResult(ctx, t, done)
			require.Error(t, err)
			require.ErrorContains(t, err, path)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			}
			if tc.wantMsg != "" {
				require.ErrorContains(t, err, tc.wantMsg)
			}
		})
	}
}

func startPauseOnFIFO(t *testing.T, path string) <-chan error {
	t.Helper()

	fp, err := NewFailpoint(t.Name(), "1*pause_on_fifo("+path+")")
	require.NoError(t, err)
	t.Logf("failpoint: %s", fp.Marshal())

	done := make(chan error, 1)
	go func() { done <- fp.Evaluate() }()
	return done
}

func waitResult(ctx context.Context, t *testing.T, done <-chan error) error {
	t.Helper()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		t.Fatal("timed out waiting for the FIFO failpoint")
		return nil
	}
}
