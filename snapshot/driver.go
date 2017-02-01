package snapshot

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/containerd"
	"github.com/docker/containerd/testutil"
)

// Driver defines the methods required to implement a snapshot driver for
// allocating, snapshotting and mounting abstract filesystems. The model works
// by building up sets of changes with parent-child relationships.
//
// These differ from the concept of the graphdriver in that the Manager has no
// knowledge of images, layers or containers. Users simply prepare and commit
// directories. We also avoid the integration between graph driver's and the
// tar format used to represent the changesets.
//
// A snapshot represents a filesystem state. Every snapshot has a parent, where
// the empty parent is represented by the empty string. A diff can be taken
// between a parent and its snapshot to generate a classic layer.
//
// For convention, we define the following terms to be used throughout this
// interface for driver implementations:
//
// 	`name` - refers to a forkable snapshot, typically read only
// 	`key` - refers to an active transaction, either a prepare or view
// 	`parent` - refers to the parent in relation to a name
//
// TODO(stevvooe): Update this description when things settle.
//
// Importing a Layer
//
// To import a layer, we simply have the Manager provide a list of
// mounts to be applied such that our dst will capture a changeset. We start
// out by getting a path to the layer tar file and creating a temp location to
// unpack it to:
//
//	layerPath, tmpLocation := getLayerPath(), mkTmpDir() // just a path to layer tar file.
//
// We then use a Manager to prepare the temporary location as a
// snapshot point:
//
// 	sm := NewManager()
//	mounts, err := sm.Prepare(tmpLocation, "")
// 	if err != nil { ... }
//
// Note that we provide "" as the parent, since we are applying the diff to an
// empty directory. We get back a list of mounts from Manager.Prepare.
// Before proceeding, we perform all these mounts:
//
//	if err := MountAll(mounts); err != nil { ... }
//
// Once the mounts are performed, our temporary location is ready to capture
// a diff. In practice, this works similar to a filesystem transaction. The
// next step is to unpack the layer. We have a special function unpackLayer
// that applies the contents of the layer to target location and calculates the
// DiffID of the unpacked layer (this is a requirement for docker
// implementation):
//
//	layer, err := os.Open(layerPath)
//	if err != nil { ... }
// 	digest, err := unpackLayer(tmpLocation, layer) // unpack into layer location
// 	if err != nil { ... }
//
// When the above completes, we should have a filesystem the represents the
// contents of the layer. Careful implementations should verify that digest
// matches the expected DiffID. When completed, we unmount the mounts:
//
//	unmount(mounts) // optional, for now
//
// Now that we've verified and unpacked our layer, we create a location to
// commit the actual diff. For this example, we are just going to use the layer
// digest, but in practice, this will probably be the ChainID:
//
// 	diffPath := filepath.Join("/layers", digest) // name location for the uncompressed layer digest
//	if err := sm.Commit(diffPath, tmpLocation); err != nil { ... }
//
// Now, we have a layer in the Manager that can be accessed with the
// opaque diffPath provided during commit.
//
// Importing the Next Layer
//
// Making a layer depend on the above is identical to the process described
// above except that the parent is provided as diffPath when calling
// Manager.Prepare:
//
// 	mounts, err := sm.Prepare(tmpLocation, parentDiffPath)
//
// The diff will be captured at tmpLocation, as the layer is applied.
//
// Running a Container
//
// To run a container, we simply provide Manager.Prepare the diffPath
// of the image we want to start the container from. After mounting, the
// prepared path can be used directly as the container's filesystem:
//
// 	mounts, err := sm.Prepare(containerRootFS, imageDiffPath)
//
// The returned mounts can then be passed directly to the container runtime. If
// one would like to create a new image from the filesystem,
// Manager.Commit is called:
//
// 	if err := sm.Commit(newImageDiff, containerRootFS); err != nil { ... }
//
// Alternatively, for most container runs, Manager.Rollback will be
// called to signal Manager to abandon the changes.
type Driver interface {
	// Prepare returns a set of mounts corresponding to an active snapshot
	// transaction, identified by the provided transaction key.
	//
	// If a parent is provided, after performing the mounts, the destination
	// will start with the content of the parent. Changes to the mounted
	// destination will be captured in relation to the provided parent. The
	// default parent, "", is an empty directory.
	//
	// The changes may be saved to a new snapshot by calling Commit. When one
	// is done with the transaction, Remove should be called on the key.
	//
	// Multiple calls to Prepare or View with the same key should fail.
	Prepare(key, parent string) ([]containerd.Mount, error)

	// View behaves identically to Prepare except the result may not be committed
	// back to the snapshot manager. View returns a readonly view on the
	// parent, with the transaction tracked by the given key.
	//
	// This method operates identically to Prepare, except that Mounts returned
	// may have the readonly flag set. Any modifications to the underlying
	// filesystem will be ignored.
	//
	// Commit may not be called on the provided key. To collect the resources
	// associated with key, Remove must be called with key as the argument.
	View(key, parent string) ([]containerd.Mount, error)

	// Commit captures the changes between key and its parent into a snapshot
	// identified by name.  The name can then be used with the driver's other
	// methods to create subsequent snapshots.
	//
	// A snapshot will be created under name with the parent that started the
	// transaction.
	//
	// Commit may be called multiple times on the same key. Snapshots created
	// in this manner will all reference the parent used to start the
	// transaction.
	Commit(name, key string) error

	// Mounts returns the mounts for the transaction identified by key. Can be
	// called on an read-write or readonly transaction.
	//
	// This can be used to recover mounts after calling View or Prepare.
	Mounts(key string) ([]containerd.Mount, error)

	// Remove abandons the transaction identified by key. All resources
	// associated with the key will be removed.
	Remove(key string) error

	// Parent returns the parent of snapshot identified by name.
	Parent(name string) (string, error)

	// Exists returns true if the snapshot with name exists.
	Exists(name string) bool

	// Delete the snapshot idenfitied by name.
	//
	// If name has children, the operation will fail.
	Delete(name string) error

	// TODO(stevvooe): The methods below are still in flux. We'll need to work
	// out the roles of active and committed snapshots for this to become more
	// clear.

	// Walk the committed snapshots.
	Walk(fn func(name string) error) error

	// Active will call fn for each active transaction.
	Active(fn func(key string) error) error
}

// DriverSuite runs a test suite on the driver given a factory function.
func DriverSuite(t *testing.T, name string, driverFn func(root string) (Driver, func(), error)) {
	t.Run("Basic", makeTest(t, name, driverFn, checkDriverBasic))
}

func makeTest(t *testing.T, name string, driverFn func(root string) (Driver, func(), error), fn func(t *testing.T, driver Driver, work string)) func(t *testing.T) {
	return func(t *testing.T) {
		// Make two directories: a driver root and a play area for the tests:
		//
		// 	/tmp
		// 		work/ -> passed to test functions
		// 		root/ -> passed to driver
		//
		tmpDir, err := ioutil.TempDir("", "snapshot-suite-"+name+"-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		root := filepath.Join(tmpDir, "root")
		if err := os.MkdirAll(root, 0777); err != nil {
			t.Fatal(err)
		}

		driver, cleanup, err := driverFn(root)
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()

		work := filepath.Join(tmpDir, "work")
		if err := os.MkdirAll(work, 0777); err != nil {
			t.Fatal(err)
		}

		defer testutil.DumpDir(t, tmpDir)
		fn(t, driver, work)
	}
}

// checkDriverBasic tests the basic workflow of a snapshot driver.
func checkDriverBasic(t *testing.T, driver Driver, work string) {
	preparing := filepath.Join(work, "preparing")
	if err := os.MkdirAll(preparing, 0777); err != nil {
		t.Fatal(err)
	}

	mounts, err := driver.Prepare(preparing, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(mounts) < 1 {
		t.Fatal("expected mounts to have entries")
	}

	if err := containerd.MountAll(mounts, preparing); err != nil {
		t.Fatal(err)
	}
	defer testutil.Unmount(t, preparing)

	if err := ioutil.WriteFile(filepath.Join(preparing, "foo"), []byte("foo\n"), 0777); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(preparing, "a", "b", "c"), 0755); err != nil {
		t.Fatal(err)
	}

	committed := filepath.Join(work, "committed")
	if err := driver.Commit(committed, preparing); err != nil {
		t.Fatal(err)
	}

	parent, err := driver.Parent(committed)
	if err != nil {
		t.Fatal(err)
	}

	if parent != "" {
		t.Fatalf("parent of new layer should be empty, got driver.Parent(%q) == %q", committed, parent)
	}

	next := filepath.Join(work, "nextlayer")
	if err := os.MkdirAll(next, 0777); err != nil {
		t.Fatal(err)
	}

	mounts, err = driver.Prepare(next, committed)
	if err != nil {
		t.Fatal(err)
	}
	if err := containerd.MountAll(mounts, next); err != nil {
		t.Fatal(err)
	}
	defer testutil.Unmount(t, next)

	if err := ioutil.WriteFile(filepath.Join(next, "bar"), []byte("bar\n"), 0777); err != nil {
		t.Fatal(err)
	}

	// also, change content of foo to bar
	if err := ioutil.WriteFile(filepath.Join(next, "foo"), []byte("bar\n"), 0777); err != nil {
		t.Fatal(err)
	}

	if err := os.RemoveAll(filepath.Join(next, "a", "b")); err != nil {
		t.Log(err)
	}

	nextCommitted := filepath.Join(work, "committed-next")
	if err := driver.Commit(nextCommitted, next); err != nil {
		t.Fatal(err)
	}

	parent, err = driver.Parent(nextCommitted)
	if parent != committed {
		t.Fatalf("parent of new layer should be %q, got driver.Parent(%q) == %q", committed, next, parent)
	}
}
