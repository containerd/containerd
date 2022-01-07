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

package metadata

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/labels"
	digest "github.com/opencontainers/go-digest"
	bolt "go.etcd.io/bbolt"
)

func TestHasSharedLabel(t *testing.T) {
	tmpdir, err := os.MkdirTemp("", "bucket-testing-")
	if err != nil {
		t.Error(err)
	}

	db, err := bolt.Open(filepath.Join(tmpdir, "metadata.db"), 0660, nil)
	if err != nil {
		t.Error(err)
	}

	err = createNamespaceLabelsBucket(db, "testing-with-shareable", true)
	if err != nil {
		t.Error(err)
	}

	err = createNamespaceLabelsBucket(db, "testing-without-shareable", false)
	if err != nil {
		t.Error(err)
	}

	err = db.View(func(tx *bolt.Tx) error {
		if !hasSharedLabel(tx, "testing-with-shareable") {
			return errors.New("hasSharedLabel should return true when label is set")
		}
		if hasSharedLabel(tx, "testing-without-shareable") {
			return errors.New("hasSharedLabel should return false when label is not set")
		}
		return nil
	})

	if err != nil {
		t.Error(err)
	}
}

func TestGetShareableBucket(t *testing.T) {
	tmpdir, err := os.MkdirTemp("", "bucket-testing-")
	if err != nil {
		t.Error(err)
	}

	db, err := bolt.Open(filepath.Join(tmpdir, "metadata.db"), 0660, nil)
	if err != nil {
		t.Error(err)
	}

	goodDigest := digest.FromString("gooddigest")
	imagePresentNS := "has-image-is-shareable"
	imageAbsentNS := "image-absent"

	// Create two namespaces, empty for now
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := createImagesBucket(tx, imagePresentNS)
		if err != nil {
			return err
		}

		_, err = createImagesBucket(tx, imageAbsentNS)
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		t.Error(err)
	}

	// Test that getShareableBucket is correctly returning nothing when a
	// a bucket with that digest is not present in any namespace.
	err = db.View(func(tx *bolt.Tx) error {
		if bkt := getShareableBucket(tx, goodDigest); bkt != nil {
			return errors.New("getShareableBucket should return nil if digest is not present")
		}
		return nil
	})

	if err != nil {
		t.Error(err)
	}

	// Create a blob bucket in one of the namespaces with a well-known digest
	err = db.Update(func(tx *bolt.Tx) error {
		_, err = createBlobBucket(tx, imagePresentNS, goodDigest)
		if err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		t.Error(err)
	}

	// Verify that it is still not retrievable if the shareable label is not present
	err = db.View(func(tx *bolt.Tx) error {
		if bkt := getShareableBucket(tx, goodDigest); bkt != nil {
			return errors.New("getShareableBucket should return nil if digest is present but doesn't have shareable label")
		}
		return nil
	})

	if err != nil {
		t.Error(err)
	}

	// Create the namespace labels bucket and mark it as shareable
	err = createNamespaceLabelsBucket(db, imagePresentNS, true)
	if err != nil {
		t.Error(err)
	}

	// Verify that this digest is retrievable from getShareableBucket
	err = db.View(func(tx *bolt.Tx) error {
		if bkt := getShareableBucket(tx, goodDigest); bkt == nil {
			return errors.New("getShareableBucket should not return nil if digest is present")
		}
		return nil
	})

	if err != nil {
		t.Error(err)
	}
}

func createNamespaceLabelsBucket(db transactor, ns string, shareable bool) error {
	err := db.Update(func(tx *bolt.Tx) error {
		err := withNamespacesLabelsBucket(tx, ns, func(bkt *bolt.Bucket) error {
			if shareable {
				err := bkt.Put([]byte(labels.LabelSharedNamespace), []byte("true"))
				if err != nil {
					return err
				}
			}
			return nil
		})
		return err
	})
	return err
}
