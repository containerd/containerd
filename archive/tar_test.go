package archive

import (
	"bytes"
	"context"
	"io/ioutil"
	"os"
	"os/exec"
	"testing"

	_ "crypto/sha256"

	"github.com/containerd/containerd/fs"
	"github.com/containerd/containerd/fs/fstest"
	"github.com/pkg/errors"
)

const tarCmd = "tar"

// baseApplier creates a basic filesystem layout
// with multiple types of files for basic tests.
var baseApplier = fstest.Apply(
	fstest.CreateDir("/etc/", 0755),
	fstest.CreateFile("/etc/hosts", []byte("127.0.0.1 localhost"), 0644),
	fstest.Link("/etc/hosts", "/etc/hosts.allow"),
	fstest.CreateDir("/usr/local/lib", 0755),
	fstest.CreateFile("/usr/local/lib/libnothing.so", []byte{0x00, 0x00}, 0755),
	fstest.Symlink("libnothing.so", "/usr/local/lib/libnothing.so.2"),
	fstest.CreateDir("/home", 0755),
	fstest.CreateDir("/home/derek", 0700),
)

func TestUnpack(t *testing.T) {
	requireTar(t)

	if err := testApply(baseApplier); err != nil {
		t.Fatalf("Test apply failed: %+v", err)
	}
}

func TestBaseDiff(t *testing.T) {
	requireTar(t)

	if err := testBaseDiff(baseApplier); err != nil {
		t.Fatalf("Test base diff failed: %+v", err)
	}
}

func TestDiffApply(t *testing.T) {
	fstest.FSSuite(t, diffApplier{})
}

func testApply(a fstest.Applier) error {
	td, err := ioutil.TempDir("", "test-apply-")
	if err != nil {
		return errors.Wrap(err, "failed to create temp dir")
	}
	defer os.RemoveAll(td)
	dest, err := ioutil.TempDir("", "test-apply-dest-")
	if err != nil {
		return errors.Wrap(err, "failed to create temp dir")
	}
	defer os.RemoveAll(dest)

	if err := a.Apply(td); err != nil {
		return errors.Wrap(err, "failed to apply filesystem changes")
	}

	tarArgs := []string{"c", "-C", td}
	names, err := readDirNames(td)
	if err != nil {
		return errors.Wrap(err, "failed to read directory names")
	}
	tarArgs = append(tarArgs, names...)

	cmd := exec.Command(tarCmd, tarArgs...)

	arch, err := cmd.StdoutPipe()
	if err != nil {
		return errors.Wrap(err, "failed to create stdout pipe")
	}

	if err := cmd.Start(); err != nil {
		return errors.Wrap(err, "failed to start command")
	}

	if _, err := Apply(context.Background(), dest, arch); err != nil {
		return errors.Wrap(err, "failed to apply tar stream")
	}

	return fstest.CheckDirectoryEqual(td, dest)
}

func testBaseDiff(a fstest.Applier) error {
	td, err := ioutil.TempDir("", "test-base-diff-")
	if err != nil {
		return errors.Wrap(err, "failed to create temp dir")
	}
	defer os.RemoveAll(td)
	dest, err := ioutil.TempDir("", "test-base-diff-dest-")
	if err != nil {
		return errors.Wrap(err, "failed to create temp dir")
	}
	defer os.RemoveAll(dest)

	if err := a.Apply(td); err != nil {
		return errors.Wrap(err, "failed to apply filesystem changes")
	}

	arch := Diff(context.Background(), "", td)

	cmd := exec.Command(tarCmd, "x", "-C", dest)
	cmd.Stdin = arch
	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, "tar command failed")
	}

	return fstest.CheckDirectoryEqual(td, dest)
}

type diffApplier struct{}

func (d diffApplier) TestContext(ctx context.Context) (context.Context, func(), error) {
	base, err := ioutil.TempDir("", "test-diff-apply-")
	if err != nil {
		return ctx, nil, errors.Wrap(err, "failed to create temp dir")
	}
	return context.WithValue(ctx, d, base), func() {
		os.RemoveAll(base)
	}, nil
}

func (d diffApplier) Apply(ctx context.Context, a fstest.Applier) (string, func(), error) {
	base := ctx.Value(d).(string)

	applyCopy, err := ioutil.TempDir("", "test-diffapply-apply-copy-")
	if err != nil {
		return "", nil, errors.Wrap(err, "failed to create temp dir")
	}
	defer os.RemoveAll(applyCopy)
	if err = fs.CopyDir(applyCopy, base); err != nil {
		return "", nil, errors.Wrap(err, "failed to copy base")
	}
	if err := a.Apply(applyCopy); err != nil {
		return "", nil, errors.Wrap(err, "failed to apply changes to copy of base")
	}

	diffBytes, err := ioutil.ReadAll(Diff(ctx, base, applyCopy))
	if err != nil {
		return "", nil, errors.Wrap(err, "failed to create diff")
	}

	if _, err = Apply(ctx, base, bytes.NewReader(diffBytes)); err != nil {
		return "", nil, errors.Wrap(err, "failed to apply tar stream")
	}

	return base, nil, nil
}

func readDirNames(p string) ([]string, error) {
	fis, err := ioutil.ReadDir(p)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(fis))
	for i, fi := range fis {
		names[i] = fi.Name()
	}
	return names, nil
}

func requireTar(t *testing.T) {
	if _, err := exec.LookPath(tarCmd); err != nil {
		t.Skipf("%s not found, skipping", tarCmd)
	}
}
