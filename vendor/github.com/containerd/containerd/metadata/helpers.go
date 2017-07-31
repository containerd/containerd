package metadata

import (
	"time"

	"github.com/boltdb/bolt"
	"github.com/pkg/errors"
)

func readLabels(m map[string]string, bkt *bolt.Bucket) error {
	return bkt.ForEach(func(k, v []byte) error {
		m[string(k)] = string(v)
		return nil
	})
}

// writeLabels will write a new labels bucket to the provided bucket at key
// bucketKeyLabels, replacing the contents of the bucket with the provided map.
//
// The provide map labels will be modified to have the final contents of the
// bucket. Typically, this removes zero-value entries.
func writeLabels(bkt *bolt.Bucket, labels map[string]string) error {
	// Remove existing labels to keep from merging
	if lbkt := bkt.Bucket(bucketKeyLabels); lbkt != nil {
		if err := bkt.DeleteBucket(bucketKeyLabels); err != nil {
			return err
		}
	}

	if len(labels) == 0 {
		return nil
	}

	lbkt, err := bkt.CreateBucket(bucketKeyLabels)
	if err != nil {
		return err
	}

	for k, v := range labels {
		if v == "" {
			delete(labels, k) // remove since we don't actually set it
			continue
		}

		if err := lbkt.Put([]byte(k), []byte(v)); err != nil {
			return errors.Wrapf(err, "failed to set label %q=%q", k, v)
		}
	}

	return nil
}

func readTimestamps(created, updated *time.Time, bkt *bolt.Bucket) error {
	for _, f := range []struct {
		b []byte
		t *time.Time
	}{
		{bucketKeyCreatedAt, created},
		{bucketKeyUpdatedAt, updated},
	} {
		v := bkt.Get(f.b)
		if v != nil {
			if err := f.t.UnmarshalBinary(v); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeTimestamps(bkt *bolt.Bucket, created, updated time.Time) error {
	createdAt, err := created.MarshalBinary()
	if err != nil {
		return err
	}
	updatedAt, err := updated.MarshalBinary()
	if err != nil {
		return err
	}
	for _, v := range [][2][]byte{
		{bucketKeyCreatedAt, createdAt},
		{bucketKeyUpdatedAt, updatedAt},
	} {
		if err := bkt.Put(v[0], v[1]); err != nil {
			return err
		}
	}

	return nil
}
