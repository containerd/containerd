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

package bindir

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/pkg/imageverifier"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
)

const outputLimitBytes = 1 << 15 // 32 KiB

type Config struct {
	BinDir             string        `toml:"bin_dir"`
	MaxVerifiers       int           `toml:"max_verifiers"`
	PerVerifierTimeout time.Duration `toml:"per_verifier_timeout"`
}

type ImageVerifier struct {
	config *Config
}

var _ imageverifier.ImageVerifier = (*ImageVerifier)(nil)

func NewImageVerifier(c *Config) *ImageVerifier {
	return &ImageVerifier{
		config: c,
	}
}

func (v *ImageVerifier) VerifyImage(ctx context.Context, name string, desc ocispec.Descriptor) (*imageverifier.Judgement, error) {
	// os.ReadDir sorts entries by name.
	entries, err := os.ReadDir(v.config.BinDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &imageverifier.Judgement{
				OK:     true,
				Reason: fmt.Sprintf("image verifier directory %v does not exist", v.config.BinDir),
			}, nil
		}

		return nil, fmt.Errorf("failed to list directory contents: %w", err)
	}

	if len(entries) == 0 {
		return &imageverifier.Judgement{
			OK:     true,
			Reason: fmt.Sprintf("no image verifier binaries found in %v", v.config.BinDir),
		}, nil
	}

	reason := &strings.Builder{}
	for i, entry := range entries {
		if (i+1) > v.config.MaxVerifiers && v.config.MaxVerifiers >= 0 {
			log.G(ctx).Warnf("image verifiers are being skipped since directory %v has %v entries, more than configured max of %v verifiers", v.config.BinDir, len(entries), v.config.MaxVerifiers)
			break
		}

		bin := entry.Name()
		start := time.Now()
		exitCode, vr, err := v.runVerifier(ctx, bin, name, desc)
		runtime := time.Since(start)
		if err != nil {
			return nil, fmt.Errorf("failed to call verifier %v (runtime %v): %w", bin, runtime, err)
		}

		if exitCode != 0 {
			return &imageverifier.Judgement{
				OK:     false,
				Reason: fmt.Sprintf("verifier %v rejected image (exit code %v): %v", bin, exitCode, vr),
			}, nil
		}

		if i > 0 {
			reason.WriteString(", ")
		}
		reason.WriteString(fmt.Sprintf("%v => %v", bin, vr))
	}

	return &imageverifier.Judgement{
		OK:     true,
		Reason: reason.String(),
	}, nil
}

func (v *ImageVerifier) runVerifier(ctx context.Context, bin string, imageName string, desc ocispec.Descriptor) (exitCode int, reason string, err error) {
	ctx, cancel := context.WithTimeout(ctx, v.config.PerVerifierTimeout)
	defer cancel()

	binPath := filepath.Join(v.config.BinDir, bin)
	args := []string{
		"-name", imageName,
		"-digest", desc.Digest.String(),
		"-stdin-media-type", ocispec.MediaTypeDescriptor,
	}

	cmd := exec.CommandContext(ctx, binPath, args...)

	// We construct our own pipes instead of using the default StdinPipe,
	// StoutPipe, and StderrPipe in order to set timeouts on reads and writes.
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		return -1, "", err
	}
	cmd.Stdin = stdinRead
	defer stdinRead.Close()
	defer stdinWrite.Close()

	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return -1, "", err
	}
	cmd.Stdout = stdoutWrite
	defer stdoutRead.Close()
	defer stdoutWrite.Close()

	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		return -1, "", err
	}
	cmd.Stderr = stderrWrite
	defer stderrRead.Close()
	defer stderrWrite.Close()

	// Close parent ends of pipes on timeout. Without this, I/O may hang in the
	// parent process.
	if d, ok := ctx.Deadline(); ok {
		stdinWrite.SetDeadline(d)
		stdoutRead.SetDeadline(d)
		stderrRead.SetDeadline(d)
	}

	// Finish configuring, and then fork & exec the child process.
	p, err := startProcess(ctx, cmd)
	if err != nil {
		return -1, "", err
	}
	defer p.cleanup(ctx)

	// Close the child ends of the pipes in the parent process.
	stdinRead.Close()
	stdoutWrite.Close()
	stderrWrite.Close()

	// Write the descriptor to stdin.
	go func() {
		// Descriptors are usually small enough to fit in a pipe buffer (which is
		// often 64 KiB on Linux) so this write usually won't block on the child
		// process reading stdin. However, synchronously writing to stdin may cause
		// the parent to block if the descriptor is larger than the pipe buffer and
		// the child process doesn't read stdin. Therefore, we write to stdin
		// asynchronously, limited by the stdinWrite deadline set above.
		err := json.NewEncoder(stdinWrite).Encode(desc)
		if err != nil {
			// This may error out with a "broken pipe" error if the descriptor is
			// larger than the pipe buffer and the child process does not read all
			// of stdin.
			log.G(ctx).WithError(err).Warn("failed to completely write descriptor to stdin")
		}
		stdinWrite.Close()
	}()

	// Pipe verifier stderr lines to debug logs.
	stderrLog := log.G(ctx).Logger.WithFields(logrus.Fields{
		"image_verifier": bin,
		"stream":         "stderr",
	})
	stderrLogDone := make(chan struct{})
	go func() {
		defer close(stderrLogDone)
		defer stderrRead.Close()
		lr := &io.LimitedReader{
			R: stderrRead,
			N: outputLimitBytes,
		}

		s := bufio.NewScanner(lr)
		for s.Scan() {
			stderrLog.Debug(s.Text())
		}
		if err := s.Err(); err != nil {
			stderrLog.WithError(err).Debug("error logging image verifier stderr")
		}

		if lr.N == 0 {
			// Peek ahead to see if stderr reader was truncated.
			b := make([]byte, 1)
			if n, _ := stderrRead.Read(b); n > 0 {
				stderrLog.Debug("(previous logs may be truncated)")
			}
		}

		// Discard the truncated part of stderr. Doing this rather than closing the
		// reader avoids broken pipe errors. This is bounded by the stderrRead
		// deadline.
		if _, err := io.Copy(io.Discard, stderrRead); err != nil {
			log.G(ctx).WithError(err).Error("error flushing stderr")
		}
	}()

	stdout, err := io.ReadAll(io.LimitReader(stdoutRead, outputLimitBytes))
	if err != nil {
		log.G(ctx).WithError(err).Error("error reading stdout")
	} else {
		m := strings.Builder{}
		m.WriteString(strings.TrimSpace(string(stdout)))
		// Peek ahead to see if stdout is truncated.
		b := make([]byte, 1)
		if n, _ := stdoutRead.Read(b); n > 0 {
			m.WriteString("(stdout truncated)")
		}
		reason = m.String()
	}

	// Discard the truncated part of stdout. Doing this rather than closing the
	// reader avoids broken pipe errors. This is bounded by the stdoutRead
	// deadline.
	if _, err := io.Copy(io.Discard, stdoutRead); err != nil {
		log.G(ctx).WithError(err).Error("error flushing stdout")
	}
	stdoutRead.Close()

	<-stderrLogDone
	if err := cmd.Wait(); err != nil {
		if ee := (&exec.ExitError{}); errors.As(err, &ee) && ee.ProcessState.Exited() {
			return ee.ProcessState.ExitCode(), reason, nil
		}
		return -1, "", fmt.Errorf("waiting on command to exit: %v", err)
	}

	return cmd.ProcessState.ExitCode(), reason, nil
}
