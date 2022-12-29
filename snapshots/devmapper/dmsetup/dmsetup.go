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

// Copyright 2012-2017 Docker, Inc.

package dmsetup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	blkdiscard "github.com/containerd/containerd/snapshots/devmapper/blkdiscard"
	exec "golang.org/x/sys/execabs"
	"golang.org/x/sys/unix"
)

const (
	// DevMapperDir represents devmapper devices location
	DevMapperDir = "/dev/mapper/"
	// SectorSize represents the number of bytes in one sector on devmapper devices
	SectorSize = 512
)

// ErrInUse represents an error mutating a device because it is in use elsewhere
var ErrInUse = errors.New("device is in use")

// DeviceInfo represents device info returned by "dmsetup info".
// dmsetup(8) provides more information on each of these fields.
type DeviceInfo struct {
	Name            string
	BlockDeviceName string
	TableLive       bool
	TableInactive   bool
	Suspended       bool
	ReadOnly        bool
	Major           uint32
	Minor           uint32
	OpenCount       uint32 // Open reference count
	TargetCount     uint32 // Number of targets in the live table
	EventNumber     uint32 // Last event sequence number (used by wait)
}

var errTable map[string]unix.Errno

func init() {
	// Precompute map of <text>=<errno> for optimal lookup
	errTable = make(map[string]unix.Errno)
	for errno := unix.EPERM; errno <= unix.EHWPOISON; errno++ {
		errTable[errno.Error()] = errno
	}
}

// CreatePool creates a device with the given name, data and metadata file and block size (see "dmsetup create")
func CreatePool(poolName, dataFile, metaFile string, blockSizeSectors uint32) error {
	thinPool, err := makeThinPoolMapping(dataFile, metaFile, blockSizeSectors)
	if err != nil {
		return err
	}

	_, err = dmsetup("create", poolName, "--table", thinPool)
	return err
}

// ReloadPool reloads existing thin-pool (see "dmsetup reload")
func ReloadPool(deviceName, dataFile, metaFile string, blockSizeSectors uint32) error {
	thinPool, err := makeThinPoolMapping(dataFile, metaFile, blockSizeSectors)
	if err != nil {
		return err
	}

	_, err = dmsetup("reload", deviceName, "--table", thinPool)
	return err
}

const (
	lowWaterMark = 32768                // Picked arbitrary, might need tuning
	skipZeroing  = "skip_block_zeroing" // Skipping zeroing to reduce latency for device creation
)

// makeThinPoolMapping makes thin-pool table entry
func makeThinPoolMapping(dataFile, metaFile string, blockSizeSectors uint32) (string, error) {
	dataDeviceSizeBytes, err := BlockDeviceSize(dataFile)
	if err != nil {
		return "", fmt.Errorf("failed to get block device size: %s: %w", dataFile, err)
	}

	// Thin-pool mapping target has the following format:
	// start - starting block in virtual device
	// length - length of this segment
	// metadata_dev - the metadata device
	// data_dev - the data device
	// data_block_size - the data block size in sectors
	// low_water_mark - the low water mark, expressed in blocks of size data_block_size
	// feature_args - the number of feature arguments
	// args
	lengthSectors := dataDeviceSizeBytes / SectorSize
	target := fmt.Sprintf("0 %d thin-pool %s %s %d %d 1 %s",
		lengthSectors,
		metaFile,
		dataFile,
		blockSizeSectors,
		lowWaterMark,
		skipZeroing)

	return target, nil
}

// CreateDevice sends "create_thin <deviceID>" message to the given thin-pool
func CreateDevice(poolName string, deviceID uint32) error {
	_, err := dmsetup("message", poolName, "0", fmt.Sprintf("create_thin %d", deviceID))
	return err
}

// ActivateDevice activates the given thin-device using the 'thin' target
func ActivateDevice(poolName string, deviceName string, deviceID uint32, size uint64, external string) error {
	mapping := makeThinMapping(poolName, deviceID, size, external)
	_, err := dmsetup("create", deviceName, "--table", mapping)
	return err
}

// makeThinMapping makes thin target table entry
func makeThinMapping(poolName string, deviceID uint32, sizeBytes uint64, externalOriginDevice string) string {
	lengthSectors := sizeBytes / SectorSize

	// Thin target has the following format:
	// start - starting block in virtual device
	// length - length of this segment
	// pool_dev - the thin-pool device, can be /dev/mapper/pool_name or 253:0
	// dev_id - the internal device id of the device to be activated
	// external_origin_dev - an optional block device outside the pool to be treated as a read-only snapshot origin.
	target := fmt.Sprintf("0 %d thin %s %d %s", lengthSectors, GetFullDevicePath(poolName), deviceID, externalOriginDevice)
	return strings.TrimSpace(target)
}

// SuspendDevice suspends the given device (see "dmsetup suspend")
func SuspendDevice(deviceName string) error {
	_, err := dmsetup("suspend", deviceName)
	return err
}

// ResumeDevice resumes the given device (see "dmsetup resume")
func ResumeDevice(deviceName string) error {
	_, err := dmsetup("resume", deviceName)
	return err
}

// Table returns the current table for the device
func Table(deviceName string) (string, error) {
	return dmsetup("table", deviceName)
}

// CreateSnapshot sends "create_snap" message to the given thin-pool.
// Caller needs to suspend and resume device if it is active.
func CreateSnapshot(poolName string, deviceID uint32, baseDeviceID uint32) error {
	_, err := dmsetup("message", poolName, "0", fmt.Sprintf("create_snap %d %d", deviceID, baseDeviceID))
	return err
}

// DeleteDevice sends "delete <deviceID>" message to the given thin-pool
func DeleteDevice(poolName string, deviceID uint32) error {
	_, err := dmsetup("message", poolName, "0", fmt.Sprintf("delete %d", deviceID))
	return err
}

// RemoveDeviceOpt represents command line arguments for "dmsetup remove" command
type RemoveDeviceOpt string

const (
	// RemoveWithForce flag replaces the table with one that fails all I/O if
	// open device can't be removed
	RemoveWithForce RemoveDeviceOpt = "--force"
	// RemoveWithRetries option will cause the operation to be retried
	// for a few seconds before failing
	RemoveWithRetries RemoveDeviceOpt = "--retry"
	// RemoveDeferred flag will enable deferred removal of open devices,
	// the device will be removed when the last user closes it
	RemoveDeferred RemoveDeviceOpt = "--deferred"
)

// RemoveDevice removes a device (see "dmsetup remove")
func RemoveDevice(deviceName string, opts ...RemoveDeviceOpt) error {
	args := []string{
		"remove",
	}

	for _, opt := range opts {
		args = append(args, string(opt))
	}

	args = append(args, GetFullDevicePath(deviceName))

	_, err := dmsetup(args...)
	if err == unix.ENXIO {
		// Ignore "No such device or address" error because we dmsetup
		// remove with "deferred" option, there is chance for the device
		// having been removed.
		return nil
	}

	return err
}

// Info outputs device information (see "dmsetup info").
// If device name is empty, all device infos will be returned.
func Info(deviceName string) ([]*DeviceInfo, error) {
	output, err := dmsetup(
		"info",
		"--columns",
		"--noheadings",
		"-o",
		"name,blkdevname,attr,major,minor,open,segments,events",
		"--separator",
		" ",
		deviceName)

	if err != nil {
		return nil, err
	}

	var (
		lines   = strings.Split(output, "\n")
		devices = make([]*DeviceInfo, len(lines))
	)

	for i, line := range lines {
		var (
			attr = ""
			info = &DeviceInfo{}
		)

		_, err := fmt.Sscan(line,
			&info.Name,
			&info.BlockDeviceName,
			&attr,
			&info.Major,
			&info.Minor,
			&info.OpenCount,
			&info.TargetCount,
			&info.EventNumber)

		if err != nil {
			return nil, fmt.Errorf("failed to parse line %q: %w", line, err)
		}

		// Parse attributes (see "man 8 dmsetup" for details)
		info.Suspended = strings.Contains(attr, "s")
		info.ReadOnly = strings.Contains(attr, "r")
		info.TableLive = strings.Contains(attr, "L")
		info.TableInactive = strings.Contains(attr, "I")

		devices[i] = info
	}

	return devices, nil
}

// Version returns "dmsetup version" output
func Version() (string, error) {
	return dmsetup("version")
}

// DeviceStatus represents devmapper device status information
type DeviceStatus struct {
	RawOutput string
	Offset    int64
	Length    int64
	Target    string
	Params    []string
}

// Status provides status information for devmapper device
func Status(deviceName string) (*DeviceStatus, error) {
	var (
		err    error
		status DeviceStatus
	)

	output, err := dmsetup("status", deviceName)
	if err != nil {
		return nil, err
	}
	status.RawOutput = output

	// Status output format:
	//  Offset (int64)
	//  Length (int64)
	//  Target type (string)
	//  Params (Array of strings)
	const MinParseCount = 4
	parts := strings.Split(output, " ")
	if len(parts) < MinParseCount {
		return nil, fmt.Errorf("failed to parse output: %q", output)
	}

	status.Offset, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse offset: %q: %w", parts[0], err)
	}

	status.Length, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse length: %q: %w", parts[1], err)
	}

	status.Target = parts[2]
	status.Params = parts[3:]

	return &status, nil
}

// GetFullDevicePath returns full path for the given device name (like "/dev/mapper/name")
func GetFullDevicePath(deviceName string) string {
	if strings.HasPrefix(deviceName, DevMapperDir) {
		return deviceName
	}

	return DevMapperDir + deviceName
}

// BlockDeviceSize returns size of block device in bytes
func BlockDeviceSize(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, fmt.Errorf("failed to seek on %q: %w", path, err)
	}
	return size, nil
}

// DiscardBlocks discards all blocks for the given thin device
//
//	ported from https://github.com/moby/moby/blob/7b9275c0da707b030e62c96b679a976f31f929d3/pkg/devicemapper/devmapper.go#L416
func DiscardBlocks(deviceName string) error {
	inUse, err := isInUse(deviceName)
	if err != nil {
		return err
	}
	if inUse {
		return ErrInUse
	}
	path := GetFullDevicePath(deviceName)
	_, err = blkdiscard.BlkDiscard(path)
	if err != nil {
		return err
	}
	return nil
}

func dmsetup(args ...string) (string, error) {
	data, err := exec.Command("dmsetup", args...).CombinedOutput()
	output := string(data)
	if err != nil {
		// Try find Linux error code otherwise return generic error with dmsetup output
		if errno, ok := tryGetUnixError(output); ok {
			return "", errno
		}

		return "", fmt.Errorf("dmsetup %s\nerror: %s\n: %w", strings.Join(args, " "), output, err)
	}

	output = strings.TrimSuffix(output, "\n")
	output = strings.TrimSpace(output)

	return output, nil
}

// tryGetUnixError tries to find Linux error code from dmsetup output
func tryGetUnixError(output string) (unix.Errno, bool) {
	// It's useful to have Linux error codes like EBUSY, EPERM, ..., instead of just text.
	// Unfortunately there is no better way than extracting/comparing error text.
	text := parseDmsetupError(output)
	if text == "" {
		return 0, false
	}

	err, ok := errTable[text]
	return err, ok
}

// dmsetup returns error messages in format:
//
//	device-mapper: message ioctl on <name> failed: File exists\n
//	Command failed\n
//
// parseDmsetupError extracts text between "failed: " and "\n"
func parseDmsetupError(output string) string {
	lines := strings.SplitN(output, "\n", 2)
	if len(lines) < 2 {
		return ""
	}

	line := lines[0]
	// Handle output like "Device /dev/mapper/snapshotter-suite-pool-snap-1 not found"
	if strings.HasSuffix(line, "not found") {
		return unix.ENXIO.Error()
	}

	const failedSubstr = "failed: "
	idx := strings.LastIndex(line, failedSubstr)
	if idx == -1 {
		return ""
	}

	str := line[idx:]

	// Strip "failed: " prefix
	str = strings.TrimPrefix(str, failedSubstr)

	str = strings.ToLower(str)
	return str
}

func isInUse(deviceName string) (bool, error) {
	info, err := Info(deviceName)
	if err != nil {
		return true, err
	}
	if len(info) != 1 {
		return true, errors.New("could not get device info")
	}
	return info[0].OpenCount != 0, nil
}
