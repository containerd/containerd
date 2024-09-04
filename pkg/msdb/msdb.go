package msdb

import (
	"fmt"
	"io"
	"os"

	bolt "go.etcd.io/bbolt"
)

type MsDb struct {
	master *bolt.DB
	slave  *bolt.DB
}

func Open(path string, mode os.FileMode, options *bolt.Options) (msdb *MsDb, err error) {
	var masterDb *bolt.DB

	masterDb, err = tryOpenMasterDB(path, 0644, options)
	if err != nil {
		return nil, err
	}

	slaveDbPath := path + ".slave"
	err = copyFile(path, slaveDbPath)
	if err != nil {
		return nil, err
	}

	var slaveDb *bolt.DB
	slaveDb, err = bolt.Open(slaveDbPath, 0644, options)
	if err != nil {
		return nil, err
	}

	return &MsDb{
		master: masterDb,
		slave:  slaveDb,
	}, nil
}

func (db *MsDb) Master() *bolt.DB {
	return db.master
}

func (db *MsDb) Close() error {
	err := db.master.Close()

	if err == nil {
		err = db.slave.Close()
	}

	return err
}

func (db *MsDb) Begin(writable bool) (*MsTx, error) {
	var masterTx, salveTx *bolt.Tx
	var err error

	masterTx, err = db.master.Begin(writable)

	if err == nil {
		salveTx, err = db.slave.Begin(writable)
	}

	if err != nil {
		return nil, err
	}

	return &MsTx{
		master: masterTx,
		slave:  salveTx,
	}, nil
}

func (db *MsDb) Update(fn func(*bolt.Tx) error) error {
	err := db.master.Update(fn)

	if err == nil {
		err = db.slave.Update(fn)
	}

	return err
}

func (db *MsDb) View(fn func(*bolt.Tx) error) error {
	return db.master.View(fn)
}

func tryOpenMasterDB(path string, mode os.FileMode, options *bolt.Options) (db *bolt.DB, err error) {
	slaveDbPath := path + ".slave"
	pathBackup := path + ".backup"

	defer func() {
		if e := recover(); e != nil {
			if _, err := os.Stat(slaveDbPath); err == nil {
				if err := os.Rename(slaveDbPath, pathBackup); err != nil {
					panic(fmt.Sprintf("rename %s-%s err %v: failed (%v)", slaveDbPath, pathBackup, err, e))
				}
			} else {
				panic(fmt.Sprintf("slave db path %s err %v, by open db %s failed (%v)", slaveDbPath, err, path, e))
			}

			panic(fmt.Sprintf("open db %s failed (%v),rename %s-%s success", path, e, slaveDbPath, pathBackup))
		}
	}()

	if _, err := os.Stat(pathBackup); err == nil {
		if err := os.Rename(pathBackup, path); err != nil {
			return nil, err
		}
	}

	db, err = bolt.Open(path, 0644, options)

	return
}

func copyFile(src, dst string) error {
	os.RemoveAll(dst)

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return err
	}

	if err := dstFile.Sync(); err != nil {
		return err
	}

	return nil
}
