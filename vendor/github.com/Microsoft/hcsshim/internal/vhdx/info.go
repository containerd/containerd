// vhdx package adds the utility methods necessary to deal with the vhdx that are used as the scratch
// space for the containers and the uvm.
package vhdx

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"github.com/Microsoft/go-winio/pkg/guid"
	"github.com/Microsoft/go-winio/vhd"
	"github.com/Microsoft/hcsshim/internal/log"
	"github.com/Microsoft/hcsshim/internal/oc"
	"github.com/sirupsen/logrus"
	"go.opencensus.io/trace"
	"golang.org/x/sys/windows"
)

const _IOCTL_DISK_GET_DRIVE_LAYOUT_EX = 0x00070050

var PARTITION_BASIC_DATA_GUID = guid.GUID{
	Data1: 0xebd0a0a2,
	Data2: 0xb9e5,
	Data3: 0x4433,
	Data4: [8]byte{0x87, 0xc0, 0x68, 0xb6, 0xb7, 0x26, 0x99, 0xc7},
}

const (
	PARTITION_STYLE_MBR uint32 = iota
	PARTITION_STYLE_GPT
	PARTITION_STYLE_RAW
)

// type partitionInformationMBR struct {
// 	PartitionType       uint8
// 	BootIndicator       uint8
// 	RecognizedPartition uint8
// 	HiddenSectors       uint32
// 	PartitionId         guid.GUID
// }

type partitionInformationGPT struct {
	PartitionType guid.GUID
	PartitionId   guid.GUID
	Attributes    uint64
	Name          [72]byte // wide char
}

type partitionInformationEx struct {
	PartitionStyle     uint32
	StartingOffset     int64
	PartitionLength    int64
	PartitionNumber    uint32
	RewritePartition   uint8
	IsServicePartition uint8
	_                  uint16
	// A union of partitionInformationMBR and partitionInformationGPT
	// since partitionInformationGPT is largest with 112 bytes
	GptMbrUnion [112]byte
}

type driveLayoutInformationGPT struct {
	DiskID               guid.GUID
	StartingUsableOffset int64
	UsableLength         int64
	MaxPartitionCount    uint32
}

// type driveLayoutInformationMBR struct {
// 	Signature uint32
// 	Checksum  uint32
// }

type driveLayoutInformationEx struct {
	PartitionStyle uint32
	PartitionCount uint32
	// A union of driveLayoutInformationGPT and driveLayoutInformationMBR
	// since driveLayoutInformationGPT is largest with 40 bytes
	GptMbrUnion    [40]byte
	PartitionEntry [1]partitionInformationEx
}

// Takes the handle to an attached vhdx and retrieves the drive layout information of that vhdx.
// Returns the driveLayoutInformationEx struct and a slice of partitionInfomrationEx struct containing
// one element for each partition found on the vhdx. Note: some of the members like (GptMbrUnion) of these
// structs are raw byte array and it is the responsibility of the calling function to properly parse them.
func getDriveLayout(ctx context.Context, diskHandle syscall.Handle) (driveLayoutInformationEx, []partitionInformationEx, error) {
	var (
		outBytes   uint32
		err        error
		volume     *os.File
		volumePath string
	)

	layoutData := struct {
		info driveLayoutInformationEx
		// driveLayoutInformationEx has a flexible array member at the end. The data returned
		// by IOCTL_DISK_GET_DRIVE_LAYOUT_EX usually has driveLayoutInformationEx.PartitionCount
		// number of elements in this array. For all practical purposes we don't expect to have
		// more than 64 partitions in a container/uvm vhdx.
		partitions [63]partitionInformationEx
	}{}

	volumePath, err = vhd.GetVirtualDiskPhysicalPath(diskHandle)
	if err != nil {
		return layoutData.info, layoutData.partitions[:0], err
	}

	volume, err = os.OpenFile(volumePath, os.O_RDONLY, 0)
	if err != nil {
		return layoutData.info, layoutData.partitions[:0], fmt.Errorf("failed to open drive: %s", err)
	}
	defer volume.Close()

	err = windows.DeviceIoControl(windows.Handle(volume.Fd()),
		_IOCTL_DISK_GET_DRIVE_LAYOUT_EX,
		nil,
		0,
		(*byte)(unsafe.Pointer(&layoutData)),
		uint32(unsafe.Sizeof(layoutData)),
		&outBytes,
		nil)
	if err != nil {
		return layoutData.info, layoutData.partitions[:0], fmt.Errorf("IOCTL to get disk layout failed: %s", err)
	}

	if layoutData.info.PartitionCount == 0 {
		return layoutData.info, []partitionInformationEx{}, nil
	} else {
		// parse the retrieved data into driveLayoutInformationEx and partitionInformationEx
		partitions := make([]partitionInformationEx, layoutData.info.PartitionCount)
		partitions[0] = layoutData.info.PartitionEntry[0]
		copy(partitions[1:], layoutData.partitions[:layoutData.info.PartitionCount-1])
		return layoutData.info, partitions, nil
	}
}

// Scratch VHDs are formatted with GPT style and have 1 MSFT_RESERVED
// partition and 1 BASIC_DATA partition.  This struct contains the
// partitionID of this BASIC_DATA partition and the DiskID of this
// scratch vhdx.
type ScratchVhdxPartitionInfo struct {
	DiskID      guid.GUID
	PartitionID guid.GUID
}

// Returns the VhdxInfo of a GPT vhdx at path vhdxPath.
func GetScratchVhdPartitionInfo(ctx context.Context, vhdxPath string) (_ ScratchVhdxPartitionInfo, err error) {
	var (
		diskHandle       syscall.Handle
		driveLayout      driveLayoutInformationEx
		partitions       []partitionInformationEx
		gptDriveLayout   driveLayoutInformationGPT
		gptPartitionInfo partitionInformationGPT
	)

	title := "hcsshim::GetScratchVhdPartitionInfo"
	ctx, span := trace.StartSpan(ctx, title)
	defer span.End()
	defer func() { oc.SetSpanStatus(span, err) }()
	span.AddAttributes(
		trace.StringAttribute("path", vhdxPath))

	diskHandle, err = vhd.OpenVirtualDisk(vhdxPath, vhd.VirtualDiskAccessNone, vhd.OpenVirtualDiskFlagNone)
	if err != nil {
		return ScratchVhdxPartitionInfo{}, fmt.Errorf("get scratch vhd info failed: %s", err)
	}
	defer func() {
		if closeErr := syscall.CloseHandle(diskHandle); closeErr != nil {
			log.G(ctx).WithFields(logrus.Fields{
				"disk path": vhdxPath,
				"error":     closeErr,
			}).Warn("failed to close vhd handle")
		}
	}()

	err = vhd.AttachVirtualDisk(diskHandle, vhd.AttachVirtualDiskFlagNone, &vhd.AttachVirtualDiskParameters{Version: 2})
	if err != nil {
		return ScratchVhdxPartitionInfo{}, fmt.Errorf("get scratch vhd info failed: %s", err)
	}

	defer func() {
		if detachErr := vhd.DetachVirtualDisk(diskHandle); detachErr != nil {
			log.G(ctx).WithFields(logrus.Fields{
				"disk path": vhdxPath,
				"error":     detachErr,
			}).Warn("failed to detach vhd")
		}
	}()

	driveLayout, partitions, err = getDriveLayout(ctx, diskHandle)
	if err != nil {
		return ScratchVhdxPartitionInfo{}, err
	}

	if driveLayout.PartitionStyle != PARTITION_STYLE_GPT {
		return ScratchVhdxPartitionInfo{}, fmt.Errorf("drive Layout:Expected partition style GPT(%d) found %d", PARTITION_STYLE_GPT, driveLayout.PartitionStyle)
	}

	if driveLayout.PartitionCount != 2 || len(partitions) != 2 {
		return ScratchVhdxPartitionInfo{}, fmt.Errorf("expected exactly 2 partitions. Got %d partitions and partition count of %d", len(partitions), driveLayout.PartitionCount)
	}

	if partitions[1].PartitionStyle != PARTITION_STYLE_GPT {
		return ScratchVhdxPartitionInfo{}, fmt.Errorf("partition Info:Expected partition style GPT(%d) found %d", PARTITION_STYLE_GPT, partitions[1].PartitionStyle)
	}

	bufReader := bytes.NewBuffer(driveLayout.GptMbrUnion[:])
	if err := binary.Read(bufReader, binary.LittleEndian, &gptDriveLayout); err != nil {
		return ScratchVhdxPartitionInfo{}, fmt.Errorf("failed to parse drive GPT layout: %s", err)
	}

	bufReader = bytes.NewBuffer(partitions[1].GptMbrUnion[:])
	if err := binary.Read(bufReader, binary.LittleEndian, &gptPartitionInfo); err != nil {
		return ScratchVhdxPartitionInfo{}, fmt.Errorf("failed to parse GPT partition info: %s", err)
	}

	if gptPartitionInfo.PartitionType != PARTITION_BASIC_DATA_GUID {
		return ScratchVhdxPartitionInfo{}, fmt.Errorf("expected partition type to have %s GUID found %s instead", PARTITION_BASIC_DATA_GUID, gptPartitionInfo.PartitionType)
	}

	log.G(ctx).WithFields(logrus.Fields{
		"Disk ID":          gptDriveLayout.DiskID,
		"GPT Partition ID": gptPartitionInfo.PartitionId,
	}).Debug("Scratch VHD partition info")

	return ScratchVhdxPartitionInfo{DiskID: gptDriveLayout.DiskID, PartitionID: gptPartitionInfo.PartitionId}, nil

}
