package msdb

import (
	bolt "go.etcd.io/bbolt"
)

type MsTx struct {
	master *bolt.Tx
	slave  *bolt.Tx
}

func (b *MsTx) Master() *bolt.Tx {
	return b.master
}

func (tx *MsTx) CreateBucketIfNotExists(name []byte) (*MsBucket, error) {
	var masterBucket, salveBucket *bolt.Bucket
	var err error

	masterBucket, err = tx.master.CreateBucketIfNotExists(name)

	if err == nil {
		salveBucket, err = tx.slave.CreateBucketIfNotExists(name)
	}

	if err != nil {
		return nil, err
	}

	return &MsBucket{
		master: masterBucket,
		slave:  salveBucket,
	}, nil
}

func (tx *MsTx) Bucket(name []byte) *MsBucket {
	var masterBucket, salveBucket *bolt.Bucket

	masterBucket = tx.master.Bucket(name)

	if masterBucket == nil {
		return nil
	}

	salveBucket = tx.slave.Bucket(name)
	if salveBucket == nil {
		return nil
	}

	return &MsBucket{
		master: masterBucket,
		slave:  salveBucket,
	}
}

func (tx *MsTx) Commit() error {
	err := tx.master.Commit()

	if err == nil {
		err = tx.slave.Commit()
	}

	return err
}

func (tx *MsTx) Rollback() error {
	err := tx.master.Rollback()

	if err == nil {
		err = tx.slave.Rollback()
	}

	return err
}
