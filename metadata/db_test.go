package metadata

import (
	"encoding/binary"
	"testing"

	"github.com/boltdb/bolt"
	"github.com/containerd/containerd/errdefs"
	"github.com/pkg/errors"
)

func TestInit(t *testing.T) {
	ctx, db, cancel := testEnv(t)
	defer cancel()

	if err := NewDB(db, nil, nil).Init(ctx); err != nil {
		t.Fatal(err)
	}

	version, err := readDBVersion(db, bucketKeyVersion)
	if err != nil {
		t.Fatal(err)
	}
	if version != dbVersion {
		t.Fatalf("Unexpected version %d, expected %d", version, dbVersion)
	}
}

func TestMigrations(t *testing.T) {
	migrationTests := []struct {
		name  string
		init  func(*bolt.Tx) error
		check func(*bolt.Tx) error
	}{
		{
			name: "ChildrenKey",
			init: func(tx *bolt.Tx) error {
				bkt, err := createSnapshotterBucket(tx, "testing", "testing")
				if err != nil {
					return err
				}

				snapshots := []struct {
					key    string
					parent string
				}{
					{
						key:    "k1",
						parent: "",
					},
					{
						key:    "k2",
						parent: "k1",
					},
					{
						key:    "k2a",
						parent: "k1",
					},
					{
						key:    "a1",
						parent: "k2",
					},
				}

				for _, s := range snapshots {
					sbkt, err := bkt.CreateBucket([]byte(s.key))
					if err != nil {
						return err
					}
					if err := sbkt.Put(bucketKeyParent, []byte(s.parent)); err != nil {
						return err
					}
				}

				return nil
			},
			check: func(tx *bolt.Tx) error {
				bkt := getSnapshotterBucket(tx, "testing", "testing")
				if bkt == nil {
					return errors.Wrap(errdefs.ErrNotFound, "snapshots bucket not found")
				}
				snapshots := []struct {
					key      string
					children []string
				}{
					{
						key:      "k1",
						children: []string{"k2", "k2a"},
					},
					{
						key:      "k2",
						children: []string{"a1"},
					},
					{
						key:      "k2a",
						children: []string{},
					},
					{
						key:      "a1",
						children: []string{},
					},
				}

				for _, s := range snapshots {
					sbkt := bkt.Bucket([]byte(s.key))
					if sbkt == nil {
						return errors.Wrap(errdefs.ErrNotFound, "key does not exist")
					}

					cbkt := sbkt.Bucket(bucketKeyChildren)
					var cn int
					if cbkt != nil {
						cn = cbkt.Stats().KeyN
					}

					if cn != len(s.children) {
						return errors.Errorf("unexpected number of children %d, expected %d", cn, len(s.children))
					}

					for _, ch := range s.children {
						if v := cbkt.Get([]byte(ch)); v == nil {
							return errors.Errorf("missing child record for %s", ch)
						}
					}
				}

				return nil
			},
		},
	}

	if len(migrationTests) != len(migrations) {
		t.Fatal("Each migration must have a test case")
	}

	for i, mt := range migrationTests {
		t.Run(mt.name, runMigrationTest(i, mt.init, mt.check))
	}
}

func runMigrationTest(i int, init, check func(*bolt.Tx) error) func(t *testing.T) {
	return func(t *testing.T) {
		_, db, cancel := testEnv(t)
		defer cancel()

		if err := db.Update(init); err != nil {
			t.Fatal(err)
		}

		if err := db.Update(migrations[i].migrate); err != nil {
			t.Fatal(err)
		}

		if err := db.View(check); err != nil {
			t.Fatal(err)
		}
	}
}

func readDBVersion(db *bolt.DB, schema []byte) (int, error) {
	var version int
	if err := db.View(func(tx *bolt.Tx) error {
		bkt := tx.Bucket(schema)
		if bkt == nil {
			return errors.Wrap(errdefs.ErrNotFound, "no version bucket")
		}
		vb := bkt.Get(bucketKeyDBVersion)
		if vb == nil {
			return errors.Wrap(errdefs.ErrNotFound, "no version value")
		}
		v, _ := binary.Varint(vb)
		version = int(v)
		return nil
	}); err != nil {
		return 0, err
	}
	return version, nil
}
