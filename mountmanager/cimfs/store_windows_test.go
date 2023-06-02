package cimfs

import (
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func verifyCimMountEntry(t *testing.T, db *bolt.DB, cimPath, volume string, expectedRefCount uint64) {
	t.Helper()
	tx, err := db.Begin(false)
	if err != nil {
		t.Fatalf("failed to begin transaction: %s", err)
	}
	defer tx.Rollback()

	bkt, err := fetchBucket(tx, string(bucketKeyVersion), string(bucketKeyCimMounts))
	if err != nil {
		t.Fatalf("get bucket for cim mounts bucket %s", err)
	}

	cimBkt := bkt.Bucket([]byte(cimPath))
	if cimBkt == nil && expectedRefCount == 0 {
		return
	} else if expectedRefCount == 0 {
		t.Fatalf("bucket for cim %s shouldn't exist", cimPath)
	} else if cimBkt == nil {
		t.Fatalf("bucket should exist for cim %s", cimPath)
	}

	if string(cimBkt.Get(bucketKeyVolume)) != volume {
		t.Fatalf("expected cim volume doesn't match")
	}

	refcount, err := decodeRefCount(cimBkt.Get(bucketKeyRefCount))
	if err != nil {
		t.Fatalf("decode refcount: %s", err)
	} else if refcount != expectedRefCount {
		t.Fatalf("expected refcount is %d, got %d", expectedRefCount, refcount)
	}
}

func TestRecordCimMountUnmount(t *testing.T) {
	root := t.TempDir()
	s, err := newStore(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatalf("failed to create store: %s", err)
	}
	defer s.Close()

	cimPath := "test.cim"
	volume := "\\\\?\\Volume{abcd}\\"
	err = s.recordCimMount(cimPath, volume)
	if err != nil {
		t.Fatalf("failed to record cim mount: %s", err)
	}

	// verify db contents
	verifyCimMountEntry(t, s.db, cimPath, volume, 1)

	err = s.recordCimUnmount(cimPath)
	if err != nil {
		t.Fatalf("failed to record cim unmount: %s", err)
	}

	// verify that the entry of the cim is deleted from the db
	verifyCimMountEntry(t, s.db, cimPath, volume, 0)
}

func TestMultipleReferencesToSameCim(t *testing.T) {
	root := t.TempDir()
	s, err := newStore(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatalf("failed to create store: %s", err)
	}
	defer s.Close()

	cimPath := "test.cim"
	volume := "\\\\?\\Volume{abcd}\\"
	err = s.recordCimMount(cimPath, volume)
	if err != nil {
		t.Fatalf("record cim mount: %s", err)
	}

	err = s.recordCimMount(cimPath, volume)
	if err != nil {
		t.Fatalf("record cim mount: %s", err)
	}

	// verify db contents
	verifyCimMountEntry(t, s.db, cimPath, volume, 2)

	err = s.recordCimUnmount(cimPath)
	if err != nil {
		t.Fatalf("record cim unmount: %s", err)
	}

	verifyCimMountEntry(t, s.db, cimPath, volume, 1)

	err = s.recordCimUnmount(cimPath)
	if err != nil {
		t.Fatalf("record cim unmount: %s", err)
	}

	// verify that the entry of the cim is deleted from the db
	verifyCimMountEntry(t, s.db, cimPath, volume, 0)
}

func TestMultipleCimMounts(t *testing.T) {
	root := t.TempDir()
	s, err := newStore(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatalf("failed to create store: %s", err)
	}
	defer s.Close()

	cim1Path := "test1.cim"
	cim2Path := "test2.cim"
	volume1 := "\\\\?\\Volume{v1}\\"
	volume2 := "\\\\?\\Volume{v2}\\"
	err = s.recordCimMount(cim1Path, volume1)
	if err != nil {
		t.Fatalf("record cim mount: %s", err)
	}

	err = s.recordCimMount(cim2Path, volume2)
	if err != nil {
		t.Fatalf("record cim mount: %s", err)
	}

	// verify db contents
	verifyCimMountEntry(t, s.db, cim1Path, volume1, 1)
	verifyCimMountEntry(t, s.db, cim2Path, volume2, 1)

	err = s.recordCimUnmount(cim1Path)
	if err != nil {
		t.Fatalf("record cim unmount: %s", err)
	}

	err = s.recordCimUnmount(cim2Path)
	if err != nil {
		t.Fatalf("record cim unmount: %s", err)
	}

	// verify that the entry of the cim is deleted from the db
	verifyCimMountEntry(t, s.db, cim1Path, volume1, 0)
	verifyCimMountEntry(t, s.db, cim2Path, volume2, 0)
}
