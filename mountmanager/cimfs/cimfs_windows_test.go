package cimfs

import (
	"context"
	"errors"
	"testing"
)

func TestMountUnmount(t *testing.T) {
	cimPath := "testdata/test.cim"
	root := t.TempDir()
	ctx := context.Background()

	mm, err := NewCimfsMountManager(root)
	if err != nil {
		t.Fatalf("initialize cimfs mount manager: %s", err)
	}
	defer mm.Close()
	// cast the mount manager to cimfsMountManager for testing purpose
	cmm := mm.(*cimfsMountManager)

	mountResp, err := mm.Mount(ctx, &CimMountRequest{CimPath: cimPath})
	if err != nil {
		t.Fatalf("mount cim: %s", err)
	}

	mountedVol := mountResp.(*CimMountResponse).Volume

	vol, err := cmm.getMountPath(ctx, cimPath)
	if err != nil {
		t.Fatalf("verify mounted volume path: %s", err)
	} else if vol != mountedVol {
		t.Fatalf("mounted volume paths don't match")
	}

	_, err = mm.Unmount(ctx, &CimUnmountRequest{CimPath: cimPath})
	if err != nil {
		t.Fatalf("unmount cim: %s", err)
	}

	_, err = cmm.getMountPath(ctx, cimPath)
	if !errors.Is(err, ErrCimNotMounted) {
		t.Fatalf("should return not mounted error")
	}
}
