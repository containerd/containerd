package msdb

import (
	bolt "go.etcd.io/bbolt"
)

type BucketAlias bolt.Bucket

type MsBucket struct {
	master *bolt.Bucket
	slave  *bolt.Bucket
}

func (b *MsBucket) Master() *bolt.Bucket {
	return b.master
}

func (b *MsBucket) MasterSlave() []*bolt.Bucket {
	rslt := make([]*bolt.Bucket, 2)
	rslt[0] = b.master
	rslt[1] = b.slave
	return rslt
}

func (b *MsBucket) CreateBucketIfNotExists(key []byte) (*MsBucket, error) {
	var masterBucket, salveBucket *bolt.Bucket
	var err error

	masterBucket, err = b.master.CreateBucketIfNotExists(key)

	if err == nil {
		salveBucket, err = b.slave.CreateBucketIfNotExists(key)
	}

	if err != nil {
		return nil, err
	}

	return &MsBucket{
		master: masterBucket,
		slave:  salveBucket,
	}, nil
}

func (b *MsBucket) Bucket(name []byte) *MsBucket {
	var masterBucket, salveBucket *bolt.Bucket

	masterBucket = b.master.Bucket(name)

	if masterBucket == nil {
		return nil
	}

	salveBucket = b.slave.Bucket(name)
	if salveBucket == nil {
		return nil
	}

	return &MsBucket{
		master: masterBucket,
		slave:  salveBucket,
	}
}
