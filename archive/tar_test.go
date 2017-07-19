package archive

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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

func TestRelativeSymlinks(t *testing.T) {
	breakoutLinks := []fstest.Applier{
		fstest.Apply(
			baseApplier,
			fstest.Symlink("../other", "/home/derek/other"),
			fstest.Symlink("../../etc", "/home/derek/etc"),
			fstest.Symlink("up/../../other", "/home/derek/updown"),
		),
		fstest.Apply(
			baseApplier,
			fstest.Symlink("../../../breakout", "/home/derek/breakout"),
		),
		fstest.Apply(
			baseApplier,
			fstest.Symlink("../../breakout", "/breakout"),
		),
		fstest.Apply(
			baseApplier,
			fstest.Symlink("etc/../../upandout", "/breakout"),
		),
		fstest.Apply(
			baseApplier,
			fstest.Symlink("derek/../../../downandout", "/home/breakout"),
		),
		fstest.Apply(
			baseApplier,
			fstest.Symlink("/etc", "localetc"),
		),
	}

	for _, bo := range breakoutLinks {
		if err := testDiffApply(bo); err != nil {
			t.Fatalf("Test apply failed: %+v", err)
		}
	}
}

func TestBreakouts(t *testing.T) {
	tc := TarContext{}.WithUidGid(os.Getuid(), os.Getgid()).WithModTime(time.Now().UTC())
	expected := "unbroken"
	unbrokenCheck := func(root string) error {
		b, err := ioutil.ReadFile(filepath.Join(root, "etc", "unbroken"))
		if err != nil {
			return errors.Wrap(err, "failed to read unbroken")
		}
		if string(b) != expected {
			return errors.Errorf("/etc/unbroken: unexpected value %s, expected %s", b, expected)
		}
		return nil
	}
	sameFile := func(f1, f2 string) func(string) error {
		return func(root string) error {
			p1 := filepath.Join(root, f1)
			p2 := filepath.Join(root, f2)
			s1, err := os.Stat(p1)
			if err != nil {
				return err
			}
			s2, err := os.Stat(p2)
			if err != nil {
				return err
			}
			if !os.SameFile(s1, s2) {
				return errors.Errorf("files differ: %#v and %#v", s1, s2)
			}
			return nil
		}
	}

	breakouts := []struct {
		name      string
		w         WriterToTar
		validator func(string) error
	}{
		{
			name: "SymlinkAbsolute",
			w: TarAll(
				tc.Dir("etc", 0755),
				tc.Symlink("/etc", "localetc"),
				tc.File("/localetc/unbroken", []byte(expected), 0644),
			),
			validator: unbrokenCheck,
		},
		{
			name: "SymlinkUpAndOut",
			w: TarAll(
				tc.Dir("etc", 0755),
				tc.Dir("dummy", 0755),
				tc.Symlink("/dummy/../etc", "localetc"),
				tc.File("/localetc/unbroken", []byte(expected), 0644),
			),
			validator: unbrokenCheck,
		},
		{
			name: "SymlinkMultipleAbsolute",
			w: TarAll(
				tc.Dir("etc", 0755),
				tc.Dir("dummy", 0755),
				tc.Symlink("/etc", "/dummy/etc"),
				tc.Symlink("/dummy/etc", "localetc"),
				tc.File("/dummy/etc/unbroken", []byte(expected), 0644),
			),
			validator: unbrokenCheck,
		},
		{
			name: "SymlinkMultipleRelative",
			w: TarAll(
				tc.Dir("etc", 0755),
				tc.Dir("dummy", 0755),
				tc.Symlink("/etc", "/dummy/etc"),
				tc.Symlink("./dummy/etc", "localetc"),
				tc.File("/dummy/etc/unbroken", []byte(expected), 0644),
			),
			validator: unbrokenCheck,
		},
		{
			name: "SymlinkEmptyFile",
			w: TarAll(
				tc.Dir("etc", 0755),
				tc.File("etc/emptied", []byte("notempty"), 0644),
				tc.Symlink("/etc", "localetc"),
				tc.File("/localetc/emptied", []byte{}, 0644),
			),
			validator: func(root string) error {
				b, err := ioutil.ReadFile(filepath.Join(root, "etc", "emptied"))
				if err != nil {
					return errors.Wrap(err, "failed to read unbroken")
				}
				if len(b) > 0 {
					return errors.Errorf("/etc/emptied: non-empty")
				}
				return nil
			},
		},
		{
			name: "HardlinkRelative",
			w: TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Dir("breakouts", 0755),
				tc.Symlink("../../etc", "breakouts/d1"),
				tc.Link("/breakouts/d1/passwd", "breakouts/mypasswd"),
			),
			validator: sameFile("/breakouts/mypasswd", "/etc/passwd"),
		},
		{
			name: "HardlinkDownAndOut",
			w: TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Dir("breakouts", 0755),
				tc.Dir("downandout", 0755),
				tc.Symlink("../downandout/../../etc", "breakouts/d1"),
				tc.Link("/breakouts/d1/passwd", "breakouts/mypasswd"),
			),
			validator: sameFile("/breakouts/mypasswd", "/etc/passwd"),
		},
		{
			name: "HardlinkAbsolute",
			w: TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Symlink("/etc", "localetc"),
				tc.Link("/localetc/passwd", "localpasswd"),
			),
			validator: sameFile("localpasswd", "/etc/passwd"),
		},
		{
			name: "HardlinkRelativeLong",
			w: TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Symlink("../../../../../../../etc", "localetc"),
				tc.Link("/localetc/passwd", "localpasswd"),
			),
			validator: sameFile("localpasswd", "/etc/passwd"),
		},
		{
			name: "HardlinkRelativeUpAndOut",
			w: TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Symlink("upandout/../../../etc", "localetc"),
				tc.Link("/localetc/passwd", "localpasswd"),
			),
			validator: sameFile("localpasswd", "/etc/passwd"),
		},
		{
			name: "HardlinkDirectRelative",
			w: TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Link("../../../../../etc/passwd", "localpasswd"),
			),
			validator: sameFile("localpasswd", "/etc/passwd"),
		},
		{
			name: "HardlinkDirectAbsolute",
			w: TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Link("/etc/passwd", "localpasswd"),
			),
			validator: sameFile("localpasswd", "/etc/passwd"),
		},
		{
			name: "HardlinkSymlinkRelative",
			w: TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Symlink("../../../../../etc/passwd", "passwdlink"),
				tc.Link("/passwdlink", "localpasswd"),
			),
			validator: sameFile("/localpasswd", "/etc/passwd"),
		},
		{
			name: "HardlinkSymlinkAbsolute",
			w: TarAll(
				tc.Dir("etc", 0770),
				tc.File("/etc/passwd", []byte("inside"), 0644),
				tc.Symlink("/etc/passwd", "passwdlink"),
				tc.Link("/passwdlink", "localpasswd"),
			),
			validator: sameFile("/localpasswd", "/etc/passwd"),
		},
	}

	for _, bo := range breakouts {
		t.Run(bo.name, makeWriterToTarTest(bo.w, bo.validator))
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

func testDiffApply(a fstest.Applier) error {
	td, err := ioutil.TempDir("", "test-diff-apply-")
	if err != nil {
		return errors.Wrap(err, "failed to create temp dir")
	}
	defer os.RemoveAll(td)
	dest, err := ioutil.TempDir("", "test-diff-apply-dest-")
	if err != nil {
		return errors.Wrap(err, "failed to create temp dir")
	}
	defer os.RemoveAll(dest)

	if err := a.Apply(td); err != nil {
		return errors.Wrap(err, "failed to apply filesystem changes")
	}

	diffBytes, err := ioutil.ReadAll(Diff(context.Background(), "", td))
	if err != nil {
		return errors.Wrap(err, "failed to create diff")
	}

	if _, err := Apply(context.Background(), dest, bytes.NewReader(diffBytes)); err != nil {
		return errors.Wrap(err, "failed to apply tar stream")
	}

	return fstest.CheckDirectoryEqual(td, dest)
}

func makeWriterToTarTest(wt WriterToTar, validate func(string) error) func(*testing.T) {
	return func(t *testing.T) {
		td, err := ioutil.TempDir("", "test-writer-to-tar-")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(td)

		tr := TarFromWriterTo(wt)

		if _, err := Apply(context.Background(), td, tr); err != nil {
			t.Fatalf("Failed to apply tar: %v", err)
		}

		if validate != nil {
			if err := validate(td); err != nil {
				t.Errorf("Validation failed: %v", err)
			}

		}

	}
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

// WriterToTar is an type which writes to a tar writer
type WriterToTar interface {
	WriteTo(*tar.Writer) error
}

type writerToFn func(*tar.Writer) error

func (w writerToFn) WriteTo(tw *tar.Writer) error {
	return w(tw)
}

// TarAll creates a WriterToTar which calls all the provided writers
// in the order in which they are provided.
func TarAll(wt ...WriterToTar) WriterToTar {
	return writerToFn(func(tw *tar.Writer) error {
		for _, w := range wt {
			if err := w.WriteTo(tw); err != nil {
				return err
			}
		}
		return nil
	})
}

// TarFromWriterTo is used to create a tar stream from a tar record
// creator. This can be used to manifacture more specific tar records
// which allow testing specific tar cases which may be encountered
// by the untar process.
func TarFromWriterTo(wt WriterToTar) io.ReadCloser {
	r, w := io.Pipe()
	go func() {
		tw := tar.NewWriter(w)
		if err := wt.WriteTo(tw); err != nil {
			w.CloseWithError(err)
			return
		}
		w.CloseWithError(tw.Close())
	}()

	return r
}

// TarContext is used to create tar records
type TarContext struct {
	Uid int
	Gid int

	// ModTime sets the modtimes for all files, if nil the current time
	// is used for each file when it was written
	ModTime *time.Time

	Xattrs map[string]string
}

func (tc TarContext) newHeader(mode os.FileMode, name, link string, size int64) *tar.Header {
	ti := tarInfo{
		name: name,
		mode: mode,
		size: size,
		modt: tc.ModTime,
		hdr: &tar.Header{
			Uid:    tc.Uid,
			Gid:    tc.Gid,
			Xattrs: tc.Xattrs,
		},
	}

	if mode&os.ModeSymlink == 0 && link != "" {
		ti.hdr.Typeflag = tar.TypeLink
		ti.hdr.Linkname = link
	}

	hdr, err := tar.FileInfoHeader(ti, link)
	if err != nil {
		// Only returns an error on bad input mode
		panic(err)
	}

	return hdr
}

type tarInfo struct {
	name string
	mode os.FileMode
	size int64
	modt *time.Time
	hdr  *tar.Header
}

func (ti tarInfo) Name() string {
	return ti.name
}

func (ti tarInfo) Size() int64 {
	return ti.size
}
func (ti tarInfo) Mode() os.FileMode {
	return ti.mode
}

func (ti tarInfo) ModTime() time.Time {
	if ti.modt != nil {
		return *ti.modt
	}
	return time.Now().UTC()
}

func (ti tarInfo) IsDir() bool {
	return (ti.mode & os.ModeDir) != 0
}
func (ti tarInfo) Sys() interface{} {
	return ti.hdr
}

func (tc TarContext) WithUidGid(uid, gid int) TarContext {
	ntc := tc
	ntc.Uid = uid
	ntc.Gid = gid
	return ntc
}

func (tc TarContext) WithModTime(modtime time.Time) TarContext {
	ntc := tc
	ntc.ModTime = &modtime
	return ntc
}

// WithXattrs adds these xattrs to all files, merges with any
// previously added xattrs
func (tc TarContext) WithXattrs(xattrs map[string]string) TarContext {
	ntc := tc
	if ntc.Xattrs == nil {
		ntc.Xattrs = map[string]string{}
	}
	for k, v := range xattrs {
		ntc.Xattrs[k] = v
	}
	return ntc
}

func (tc TarContext) File(name string, content []byte, perm os.FileMode) WriterToTar {
	return writerToFn(func(tw *tar.Writer) error {
		return writeHeaderAndContent(tw, tc.newHeader(perm, name, "", int64(len(content))), content)
	})
}

func (tc TarContext) Dir(name string, perm os.FileMode) WriterToTar {
	return writerToFn(func(tw *tar.Writer) error {
		return writeHeaderAndContent(tw, tc.newHeader(perm|os.ModeDir, name, "", 0), nil)
	})
}

func (tc TarContext) Symlink(oldname, newname string) WriterToTar {
	return writerToFn(func(tw *tar.Writer) error {
		return writeHeaderAndContent(tw, tc.newHeader(0777|os.ModeSymlink, newname, oldname, 0), nil)
	})
}

func (tc TarContext) Link(oldname, newname string) WriterToTar {
	return writerToFn(func(tw *tar.Writer) error {
		return writeHeaderAndContent(tw, tc.newHeader(0777, newname, oldname, 0), nil)
	})
}

func writeHeaderAndContent(tw *tar.Writer, h *tar.Header, b []byte) error {
	if h.Size != int64(len(b)) {
		return errors.New("bad content length")
	}
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	if len(b) > 0 {
		if _, err := tw.Write(b); err != nil {
			return err
		}
	}
	return nil
}
