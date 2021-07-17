package btrfs

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

const nativeReceive = false

func Receive(r io.Reader, dstDir string) error {
	if !nativeReceive {
		buf := bytes.NewBuffer(nil)
		cmd := exec.Command("btrfs", "receive", dstDir)
		cmd.Stdin = r
		cmd.Stderr = buf
		if err := cmd.Run(); err != nil {
			if buf.Len() != 0 {
				return errors.New(buf.String())
			}
			return err
		}
		return nil
	}
	var err error
	dstDir, err = filepath.Abs(dstDir)
	if err != nil {
		return err
	}
	realMnt, err := findMountRoot(dstDir)
	if err != nil {
		return err
	}
	dir, err := os.OpenFile(dstDir, os.O_RDONLY|syscall.O_NOATIME, 0755)
	if err != nil {
		return err
	}
	mnt, err := os.OpenFile(realMnt, os.O_RDONLY|syscall.O_NOATIME, 0755)
	if err != nil {
		return err
	}
	// We want to resolve the path to the subvolume we're sitting in
	// so that we can adjust the paths of any subvols we want to receive in.
	subvolID, err := getFileRootID(mnt)
	if err != nil {
		return err
	}
	//sr, err := send.NewStreamReader(r)
	//if err != nil {
	//	return err
	//}
	_, _ = dir, subvolID
	panic("not implemented")
}
