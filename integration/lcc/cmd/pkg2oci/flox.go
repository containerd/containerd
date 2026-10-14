//go:build !windows

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
	"archive/tar"
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/klauspost/compress/zstd"
)

// buildFloxLayers converts a Flox environment into a set of tar.zst blobs and
// returns their paths (in layer order) plus the base entrypoint for the image
// config. The caller appends any user-supplied --entrypoint tokens after the
// base entrypoint and is responsible for cleaning up tmpDir.
//
// Layer order: Nix closure store paths (one per path, sorted for determinism),
// system-utils layer (/bin and /usr/bin symlinks), activation layer (last).
func buildFloxLayers(floxEnvArg, tmpDir string) (blobPaths []string, baseEntrypoint []string, _ error) {
	envDir, cleanup, err := resolveFloxEnv(floxEnvArg, tmpDir)
	if err != nil {
		return nil, nil, err
	}
	if cleanup != "" {
		defer os.RemoveAll(cleanup)
	}

	floxEnv, envName, err := readFloxEnv(envDir)
	if err != nil {
		return nil, nil, fmt.Errorf("reading flox env: %w", err)
	}
	fmt.Fprintf(os.Stderr, "pkg2oci: flox env %q → %s\n", envName, floxEnv)

	reqData, err := os.ReadFile(filepath.Join(floxEnv, "requisites.txt"))
	if err != nil {
		return nil, nil, fmt.Errorf("reading requisites.txt in %s: %w", floxEnv, err)
	}
	storePaths := parseLines(string(reqData))
	if len(storePaths) == 0 {
		return nil, nil, fmt.Errorf("requisites.txt is empty in %s", floxEnv)
	}
	fmt.Fprintf(os.Stderr, "pkg2oci: packing %d Nix store paths\n", len(storePaths))

	for _, sp := range storePaths {
		blobPath := filepath.Join(tmpDir, filepath.Base(sp)+".tar.zst")
		if err := packNixStorePath(sp, blobPath); err != nil {
			return nil, nil, fmt.Errorf("packing %s: %w", sp, err)
		}
		blobPaths = append(blobPaths, blobPath)
	}

	sysLayer, err := buildSystemUtilsLayer(tmpDir, storePaths)
	if err != nil {
		return nil, nil, fmt.Errorf("building system-utils layer: %w", err)
	}
	if sysLayer != "" {
		blobPaths = append(blobPaths, sysLayer)
	}

	activationBlob, baseEP, err := buildActivationLayer(tmpDir, floxEnv, envName, storePaths)
	if err != nil {
		return nil, nil, fmt.Errorf("building activation layer: %w", err)
	}
	blobPaths = append(blobPaths, activationBlob)

	return blobPaths, baseEP, nil
}

// resolveFloxEnv returns the local env directory for floxEnvArg. If arg is a
// FloxHub reference (owner/name), it runs "flox pull" into a scratch directory
// inside tmpDir; cleanup of that directory is the caller's responsibility via
// tmpDir. For local paths the directory is returned as-is and must not be
// removed. The cleanup return value is always empty; the signature is kept for
// future use (e.g. pulling to a location outside tmpDir).
func resolveFloxEnv(floxEnvArg, tmpDir string) (envDir, cleanup string, _ error) {
	if isLocalPath(floxEnvArg) {
		info, err := os.Stat(floxEnvArg)
		if err != nil || !info.IsDir() {
			return "", "", fmt.Errorf("--flox-env %q is not a directory", floxEnvArg)
		}
		return floxEnvArg, "", nil
	}
	if !strings.Contains(floxEnvArg, "/") {
		return "", "", fmt.Errorf("--flox-env %q is not a local path or a valid FloxHub owner/name reference", floxEnvArg)
	}
	floxBin, err := exec.LookPath("flox")
	if err != nil {
		return "", "", fmt.Errorf("flox not found in PATH: %w", err)
	}
	scratchDir := filepath.Join(tmpDir, "remote-env")
	if err := os.Mkdir(scratchDir, 0755); err != nil {
		return "", "", fmt.Errorf("creating scratch dir: %w", err)
	}
	var stderr bytes.Buffer
	pullCmd := exec.Command(floxBin, "pull", "--dir", scratchDir, floxEnvArg)
	pullCmd.Stderr = &stderr
	if err := pullCmd.Run(); err != nil {
		return "", "", fmt.Errorf("flox pull %s: %w\n%s", floxEnvArg, err, stderr.String())
	}
	return scratchDir, "", nil
}

// readFloxEnv returns the Nix store path and name of the built flox environment
// by reading the pre-built symlink from .flox/run/. No flox invocation needed.
func readFloxEnv(envDir string) (storePath, name string, _ error) {
	data, err := os.ReadFile(filepath.Join(envDir, ".flox", "env.json"))
	if err != nil {
		return "", "", fmt.Errorf("reading env.json: %w", err)
	}
	var envJSON struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &envJSON); err != nil {
		return "", "", fmt.Errorf("parsing env.json: %w", err)
	}
	if envJSON.Name == "" {
		return "", "", fmt.Errorf("name field missing in env.json")
	}

	nixArch := runtime.GOARCH
	switch nixArch {
	case "amd64":
		nixArch = "x86_64"
	case "arm64":
		nixArch = "aarch64"
	}

	symlinkPath := filepath.Join(envDir, ".flox", "run", nixArch+"-linux."+envJSON.Name+".dev")
	resolved, err := filepath.EvalSymlinks(symlinkPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("flox environment not built at %s — run 'flox install' in %s first", symlinkPath, envDir)
		}
		return "", "", fmt.Errorf("resolving %s: %w", symlinkPath, err)
	}
	return resolved, envJSON.Name, nil
}

// writeTarZst creates outPath, writes a zstd-compressed tar stream by calling
// fn with the tar.Writer, then flushes and closes everything in order. fn's
// error takes priority over close errors.
func writeTarZst(outPath string, fn func(*tar.Writer) error) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	zw, err := zstd.NewWriter(f, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		f.Close()
		return err
	}
	tw := tar.NewWriter(zw)

	werr := fn(tw)

	cerr := tw.Close()
	if cerr == nil {
		cerr = zw.Close()
	} else {
		zw.Close()
	}
	f.Close()

	if werr != nil {
		return werr
	}
	return cerr
}

// packNixStorePath creates a deterministic zstd-compressed tar archive of
// storePath at outPath. Entries are rooted at "/" so they appear as
// nix/store/<hash>-name/... (no leading slash) in the archive.
func packNixStorePath(storePath, outPath string) error {
	return writeTarZst(outPath, func(tw *tar.Writer) error {
		type entry struct {
			abs, rel string
			info     os.FileInfo
		}
		var entries []entry
		if err := filepath.Walk(storePath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel("/", path)
			if err != nil {
				return err
			}
			entries = append(entries, entry{path, rel, info})
			return nil
		}); err != nil {
			return fmt.Errorf("walking %s: %w", storePath, err)
		}

		slices.SortFunc(entries, func(a, b entry) int { return cmp.Compare(a.rel, b.rel) })

		firstSeen := map[uint64]string{}
		zero := time.Time{}

		for _, e := range entries {
			mode := e.info.Mode()
			hdr := &tar.Header{
				Name:    e.rel,
				Mode:    int64(mode.Perm()),
				ModTime: zero,
			}
			switch {
			case e.info.IsDir():
				hdr.Typeflag = tar.TypeDir
				if !strings.HasSuffix(hdr.Name, "/") {
					hdr.Name += "/"
				}
				if err := tw.WriteHeader(hdr); err != nil {
					return err
				}
			case mode&os.ModeSymlink != 0:
				target, err := os.Readlink(e.abs)
				if err != nil {
					return err
				}
				hdr.Typeflag = tar.TypeSymlink
				hdr.Linkname = target
				if err := tw.WriteHeader(hdr); err != nil {
					return err
				}
			case mode.IsRegular():
				if stat, ok := e.info.Sys().(*syscall.Stat_t); ok && stat.Nlink > 1 {
					key := stat.Ino
					if first, ok := firstSeen[key]; ok {
						hdr.Typeflag = tar.TypeLink
						hdr.Linkname = first
						hdr.Size = 0
						if err := tw.WriteHeader(hdr); err != nil {
							return err
						}
						continue
					}
					firstSeen[key] = e.rel
				}
				hdr.Typeflag = tar.TypeReg
				hdr.Size = e.info.Size()
				if err := tw.WriteHeader(hdr); err != nil {
					return err
				}
				rf, err := os.Open(e.abs)
				if err != nil {
					return err
				}
				_, copyErr := io.Copy(tw, rf)
				rf.Close()
				if copyErr != nil {
					return copyErr
				}
			}
		}
		return nil
	})
}

// buildSystemUtilsLayer creates a thin tar.zst layer that symlinks /bin and
// /usr/bin to the coreutils Nix store path, giving hook scripts access to
// standard utilities. Returns "" if coreutils is not in the closure.
func buildSystemUtilsLayer(tmpDir string, storePaths []string) (string, error) {
	coreutilsBin := findCoreutilsBin(storePaths)
	if coreutilsBin == "" {
		fmt.Fprintln(os.Stderr, "pkg2oci: coreutils not found in closure; skipping system-utils layer")
		return "", nil
	}

	blobPath := filepath.Join(tmpDir, "system-utils.tar.zst")
	zero := time.Time{}
	err := writeTarZst(blobPath, func(tw *tar.Writer) error {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: "usr/", Mode: 0755, ModTime: zero}); err != nil {
			return err
		}
		for _, lnk := range []struct{ name, target string }{
			{"bin", coreutilsBin},
			{"usr/bin", coreutilsBin},
		} {
			if err := tw.WriteHeader(&tar.Header{
				Typeflag: tar.TypeSymlink, Name: lnk.name, Linkname: lnk.target, Mode: 0777, ModTime: zero,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return blobPath, nil
}

// buildActivationLayer generates the final CAS layer for a Flox OCI image.
// It contains the ActivateCtx JSON consumed by flox-activations(1) and the
// minimal filesystem skeleton required at container startup (etc/, run/, tmp/).
// Always uses root (uid=0, gid=0) and activation mode "run".
func buildActivationLayer(tmpDir, floxEnv, envName string, storePaths []string) (blobPath string, baseEntrypoint []string, _ error) {
	activateScript := filepath.Join(floxEnv, "activate")

	bashPath := findBashInteractive(storePaths)
	if bashPath == "" {
		var err error
		bashPath, err = readShebang(activateScript)
		if err != nil {
			return "", nil, fmt.Errorf("reading bash path from activate shebang: %w", err)
		}
	}

	interpreterPath, err := readInterpreterPath(activateScript)
	if err != nil {
		return "", nil, fmt.Errorf("reading interpreter path: %w", err)
	}

	floxActivationsPath, err := filepath.EvalSymlinks(filepath.Join(floxEnv, "libexec", "flox-activations"))
	if err != nil {
		return "", nil, fmt.Errorf("resolving flox-activations: %w", err)
	}

	const activateDataPath = "etc/cas/activate-data.json"
	ctx := activateCtx{
		FloxActivateStorePath: floxEnv,
		AttachCtx: attachCtx{
			Env:                    floxEnv,
			EnvCache:               "/tmp",
			EnvDescription:         envName,
			FloxActiveEnvironments: "[]",
			PromptColor1:           "99",
			PromptColor2:           "141",
			FloxPromptEnvironments: "floxenv",
			SetPrompt:              true,
			FloxEnvCudaDetection:   "0",
			InterpreterPath:        interpreterPath,
		},
		ActivationStateDir: "/run/flox/container-activations/" + filepath.Base(floxEnv),
		Mode:               "run",
		Shell:              shellSpec{Bash: bashPath},
		RemoveAfterReading: false,
	}
	ctxJSON, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return "", nil, fmt.Errorf("marshalling activate context: %w", err)
	}

	blobPath = filepath.Join(tmpDir, "activation-layer.tar.zst")
	zero := time.Time{}
	if err := writeTarZst(blobPath, func(tw *tar.Writer) error {
		for _, dir := range []struct {
			name string
			mode int64
		}{
			{"etc/", 0755},
			{"etc/cas/", 0755},
			{"run/", 01770},
			{"run/flox/", 0770},
			{"tmp/", 01777},
			{"root/", 0700},
			{"var/", 0755},
			{"var/empty/", 0755},
		} {
			if err := tw.WriteHeader(&tar.Header{
				Typeflag: tar.TypeDir, Name: dir.name, Mode: dir.mode, ModTime: zero,
			}); err != nil {
				return err
			}
		}
		for _, nf := range []struct {
			name, content string
		}{
			{"etc/passwd", "root:x:0:0:System administrator:/root:/bin/sh\nnobody:x:65534:65534:Unprivileged user:/var/empty:/bin/sh\n"},
			{"etc/group", "root:x:0:\nnobody:x:65534:\nnogroup:x:65534:\n"},
			{"etc/nsswitch.conf", "passwd:  files\ngroup:   files\nshadow:  files\nhosts:   files dns\n"},
		} {
			data := []byte(nf.content)
			if err := tw.WriteHeader(&tar.Header{
				Typeflag: tar.TypeReg, Name: nf.name, Size: int64(len(data)), Mode: 0644, ModTime: zero,
			}); err != nil {
				return err
			}
			if _, err := tw.Write(data); err != nil {
				return err
			}
		}
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: activateDataPath, Size: int64(len(ctxJSON)), Mode: 0644, ModTime: zero,
		}); err != nil {
			return err
		}
		_, err = tw.Write(ctxJSON)
		return err
	}); err != nil {
		return "", nil, err
	}

	return blobPath, []string{floxActivationsPath, "activate", "--activate-data", "/" + activateDataPath}, nil
}

// activateCtx mirrors flox-core's ActivateCtx struct consumed by flox-activations(1).
type activateCtx struct {
	FloxActivateStorePath string    `json:"flox_activate_store_path"`
	AttachCtx             attachCtx `json:"attach_ctx"`
	ProjectCtx            any       `json:"project_ctx"`
	ActivationStateDir    string    `json:"activation_state_dir"`
	Mode                  string    `json:"mode"`
	Shell                 shellSpec `json:"shell"`
	InvocationType        any       `json:"invocation_type"`
	RemoveAfterReading    bool      `json:"remove_after_reading"`
	MetricsUUID           any       `json:"metrics_uuid"`
}

type attachCtx struct {
	Env                    string `json:"env"`
	EnvCache               string `json:"env_cache"`
	EnvDescription         string `json:"env_description"`
	FloxActiveEnvironments string `json:"flox_active_environments"`
	PromptColor1           string `json:"prompt_color_1"`
	PromptColor2           string `json:"prompt_color_2"`
	FloxPromptEnvironments string `json:"flox_prompt_environments"`
	SetPrompt              bool   `json:"set_prompt"`
	FloxEnvCudaDetection   string `json:"flox_env_cuda_detection"`
	InterpreterPath        string `json:"interpreter_path"`
}

type shellSpec struct {
	Bash string `json:"bash"`
}

func findBashInteractive(storePaths []string) string {
	for _, sp := range storePaths {
		base := filepath.Base(sp)
		if len(base) > 33 && base[32] == '-' && strings.Contains(base[33:], "bash-interactive") {
			bin := filepath.Join(sp, "bin", "bash")
			if _, err := os.Stat(bin); err == nil {
				return bin
			}
		}
	}
	return ""
}

func findCoreutilsBin(storePaths []string) string {
	for _, sp := range storePaths {
		base := filepath.Base(sp)
		if len(base) > 33 && base[32] == '-' && strings.Contains(base[33:], "coreutils") {
			binDir := filepath.Join(sp, "bin")
			if fi, err := os.Stat(binDir); err == nil && fi.IsDir() {
				return binDir
			}
		}
	}
	return ""
}

func readInterpreterPath(activateScript string) (string, error) {
	data, err := os.ReadFile(activateScript)
	if err != nil {
		return "", err
	}
	const marker = `_activate_d="`
	for line := range strings.SplitSeq(string(data), "\n") {
		_, after, ok := strings.Cut(line, marker)
		if !ok {
			continue
		}
		val, _, ok := strings.Cut(after, `"`)
		if !ok {
			continue
		}
		return filepath.Dir(val), nil
	}
	return "", fmt.Errorf("could not find _activate_d= in %s", activateScript)
}

func readShebang(scriptPath string) (string, error) {
	f, err := os.Open(scriptPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 256)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return "", err
	}
	line := strings.SplitN(string(buf[:n]), "\n", 2)[0]
	if !strings.HasPrefix(line, "#!") {
		return "", fmt.Errorf("%s: no shebang line", scriptPath)
	}
	fields := strings.Fields(strings.TrimPrefix(line, "#!"))
	if len(fields) == 0 {
		return "", fmt.Errorf("%s: empty shebang", scriptPath)
	}
	return fields[0], nil
}

func parseLines(s string) []string {
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func isLocalPath(arg string) bool {
	if strings.HasPrefix(arg, "/") || strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") {
		return true
	}
	_, err := os.Stat(arg)
	return err == nil
}
