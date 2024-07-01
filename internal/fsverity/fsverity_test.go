//go:build linux

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

package fsverity

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/containerd/containerd/v2/pkg/testutil"
)

type superblockROFeatures struct {
	_        [100]byte
	Features uint32
}

func TestEnable(t *testing.T) {

	testutil.RequiresRoot(t)

	rootDir := filepath.Join(t.TempDir(), "content")
	err := os.Mkdir(rootDir, 0755)
	if err != nil {
		t.Errorf("could not create temporary directory: %s", err.Error())
	}

	device, err := resolveDevicePath(rootDir)
	if err != nil {
		t.Skipf("invalid device: %s", err.Error())
	}

	var expected bool
	enabled, err := ext4IsVerity(device)
	if !enabled || err != nil {
		t.Logf("fsverity not enabled on ext4 file system: %s", err.Error())
		expected = false
	} else {
		t.Logf("fsverity enabled on ext4 file system")
		expected = true
	}

	verityFile := filepath.Join(rootDir, "fsverityFile")
	f, err := os.Create(verityFile)
	if err != nil {
		t.Errorf("could not create fsverity test file: %s", err.Error())
	}

	err = f.Close()
	if err != nil {
		t.Errorf("error closing fsverity test file: %s", err.Error())
	}

	defer func() {
		err := os.Remove(verityFile)
		if err != nil {
			t.Logf("error removing fsverity test file: %s", err.Error())
		}
	}()

	err = Enable(verityFile)
	if err != nil && expected {
		t.Errorf("fsverity Enable failed: %s", err.Error())
	}
	if err == nil && !expected {
		t.Errorf("fsverity Enable succeeded, expected Enable to fail")
	}
}

func TestIsEnabled(t *testing.T) {

	testDir := filepath.Join(t.TempDir(), "content")
	err := os.Mkdir(testDir, 0755)
	if err != nil {
		t.Errorf("could not create temporary directory: %s", err.Error())
	}

	if supported, err := IsSupported(testDir); !supported || err != nil {
		t.Skipf("fsverity is not supported")
	}

	verityFile := filepath.Join(testDir, "fsverityFile")
	f, err := os.Create(verityFile)
	if err != nil {
		t.Errorf("could not create fsverity test file: %s", err.Error())
	}

	err = f.Close()
	if err != nil {
		t.Errorf("error closing fsverity test file: %s", err.Error())
	}

	defer func() {
		err := os.Remove(verityFile)
		if err != nil {
			t.Logf("error removing fsverity test file: %s", err.Error())
		}
	}()

	err = Enable(verityFile)
	if err != nil {
		t.Errorf("fsverity Enable failed: %s", err.Error())
	}

	if enabled, err := IsEnabled(verityFile); !enabled || err != nil {
		t.Errorf("expected fsverity to be enabled on file, received enabled: %t; error: %s", enabled, err.Error())
	}
}

func resolveDevicePath(path string) (_ string, e error) {
	var devicePath string

	s, err := os.Stat(path)
	if err != nil {
		return devicePath, err
	}

	sys := s.Sys()
	stat, ok := sys.(*syscall.Stat_t)
	if !ok {
		return devicePath, fmt.Errorf("type assert to syscall.Stat_t failed")
	}

	// resolve to device path
	maj := (stat.Dev >> 8) & 0xff
	min := stat.Dev & 0xff

	m, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return devicePath, err
	}

	defer func() {
		err := m.Close()
		if err != nil {
			e = fmt.Errorf("could not close mountinfo: %v", err)
		}
	}()

	// scan for major:minor id and get the device path
	scanner := bufio.NewScanner(m)

	var entry string
	sub := fmt.Sprintf("%d:%d", maj, min)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), sub) {
			entry = scanner.Text()
			break
		}
	}
	if entry == "" {
		return devicePath, fmt.Errorf("device mount not found for device id %s", sub)
	}

	entryReader := strings.NewReader(entry)
	extScan := bufio.NewScanner(entryReader)
	extScan.Split(bufio.ScanWords)

	var word string
	for (word != "-") && extScan.Scan() {
		word = extScan.Text()
	}

	if !extScan.Scan() {
		return devicePath, fmt.Errorf("scanning mounts failed: %w", extScan.Err())
	}
	fs := extScan.Text()

	if fs != "ext4" {
		return devicePath, fmt.Errorf("not an ext4 file system, skipping device")
	}

	if !extScan.Scan() {
		return devicePath, fmt.Errorf("scanning mounts failed: %w", extScan.Err())
	}
	devicePath = extScan.Text()

	return devicePath, nil
}

func ext4IsVerity(fpath string) (bool, error) {
	b := superblockROFeatures{}

	r, err := os.Open(fpath)
	if err != nil {
		return false, err
	}

	defer func() {
		err := r.Close()
		if err != nil {
			fmt.Printf("failed to close %s: %s\n", fpath, err.Error())
		}
	}()

	// seek to superblock
	_, err = r.Seek(1024, 0)
	if err != nil {
		return false, err
	}

	err = binary.Read(r, binary.LittleEndian, &b)
	if err != nil {
		return false, err
	}

	// extract fsverity flag
	var verityMask uint32 = 0x8000
	res := verityMask & b.Features
	if res > 0 {
		return true, nil
	}

	return false, fmt.Errorf("fsverity not enabled on ext4 file system %s", fpath)
}
