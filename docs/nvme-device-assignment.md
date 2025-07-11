# NVMe Device Assignment for Windows Containers

This document describes how to assign NVMe devices to Windows containers using containerd's static device assignment feature.

## Overview

containerd supports static device assignment for NVMe disks on Windows through Direct Device Assignment (DDA). This allows NVMe devices attached to a VM (L1 virtualization host) to be assigned to containers/UVMs (L2 guests) running inside the VM.

## Prerequisites

1. **Hardware**: Server with SR-IOV and DDA support
2. **VM Setup**: L1 VM with NVMe devices attached and FlexIOV enabled
3. **Software**: containerd with Windows container support

## PowerShell Workflow for Device Preparation

Before assigning an NVMe device to a container, you need to prepare it using PowerShell:

```powershell
# Step 1: Identify the NVMe device
Get-PnpDevice -Class "SCSIAdapter" | Where-Object { $_.FriendlyName -like "*NVMe*" }

# Step 2: Get device details
$nvmeDevice = Get-PnpDevice -InstanceId "PCI\VEN_144D&DEV_A808&SUBSYS_A801144D&REV_00\..."
$devicePath = $nvmeDevice.InstanceId

# Step 3: Disable the device (for reassignment)
Disable-PnpDevice -InstanceId $devicePath -Confirm:$false

# Step 4: Dismount/disable in host OS
Dismount-DiskImage -DevicePath $devicePath

# Step 5: Prepare for DDA assignment
# The device is now ready for assignment to a container
```

## Container Creation with NVMe Device Assignment

### Using ctr CLI

```bash
# Assign NVMe device by device instance ID
ctr run --nvme-device "PCI\VEN_144D&DEV_A808&SUBSYS_A801144D&REV_00\4&2A5A8718&0&0008" \
    --runtime=io.containerd.runhcs.v1 \
    --isolated \
    mcr.microsoft.com/windows/servercore:ltsc2022 \
    nvme-container \
    powershell

# Assign NVMe device by location path
ctr run --nvme-device "PCIROOT(0)#PCI(0302)#PCI(0000)" \
    --runtime=io.containerd.runhcs.v1 \
    --isolated \
    mcr.microsoft.com/windows/servercore:ltsc2022 \
    nvme-container \
    powershell
```

### Using OCI Spec

```go
// In your Go code
opts := []oci.SpecOpts{
    oci.WithWindowsNVMeDevice("PCI\\VEN_144D&DEV_A808&SUBSYS_A801144D&REV_00\\4&2A5A8718&0&0008", ""),
    oci.WithWindowsHyperV, // Enable isolation
}

spec, err := oci.GenerateSpec(ctx, nil, opts...)
```

### Using generic device assignment

```bash
# You can also use the generic device flag with class identifier
ctr run --device "class://5B45201D-F2F2-4F3B-85BB-30FF1F953599" \
    --runtime=io.containerd.runhcs.v1 \
    --isolated \
    mcr.microsoft.com/windows/servercore:ltsc2022 \
    nvme-container \
    powershell
```

## Device Access Inside Container

Once assigned, the NVMe device appears as a standard NVMe device inside the container:

```powershell
# Inside the container
# List all NVMe devices
Get-PhysicalDisk | Where-Object { $_.BusType -eq "NVMe" }

# Get device information
Get-Disk | Where-Object { $_.BusType -eq "NVMe" }

# Initialize and format the disk (if needed)
$disk = Get-Disk | Where-Object { $_.BusType -eq "NVMe" -and $_.PartitionStyle -eq "RAW" }
Initialize-Disk -Number $disk.Number -PartitionStyle GPT
New-Partition -DiskNumber $disk.Number -AssignDriveLetter -UseMaximumSize
Format-Volume -DriveLetter (Get-Partition -DiskNumber $disk.Number).DriveLetter -FileSystem NTFS

# Use the device
Get-Volume | Where-Object { $_.DriveType -eq "Fixed" }
```

## Technical Details

### Device Assignment Infrastructure

The NVMe device assignment reuses the same infrastructure as GPU assignment:
- **VPCI (Virtual PCI)**: For device virtualization
- **DDA (Direct Device Assignment)**: For hardware pass-through
- **device-util.exe**: For device management operations

### Device Identification

NVMe devices can be identified using:
- **Device Instance ID**: `PCI\VEN_144D&DEV_A808&SUBSYS_A801144D&REV_00\4&2A5A8718&0&0008`
- **Location Path**: `PCIROOT(0)#PCI(0302)#PCI(0000)`
- **Class GUID**: `5B45201D-F2F2-4F3B-85BB-30FF1F953599` (NVMe class)

### Runtime Requirements

- **Isolation**: Must use `--isolated` flag or `oci.WithWindowsHyperV`
- **Runtime**: Must use `io.containerd.runhcs.v1` runtime
- **Static Assignment**: Device must be assigned before container creation

## Troubleshooting

### Common Issues

1. **Device not found**: Verify device instance ID or location path
2. **Permission denied**: Ensure device is disabled/dismounted in host OS
3. **Container startup failure**: Check that isolation is enabled
4. **Device busy**: Ensure device is not in use by host OS

### Debugging

```powershell
# Check device status
Get-PnpDevice -InstanceId "PCI\VEN_144D&DEV_A808&SUBSYS_A801144D&REV_00\..."

# Check container logs
ctr task logs nvme-container

# Check HCS events
Get-WinEvent -LogName "Microsoft-Windows-Hyper-V-Compute-Admin"
```

## Example Workflow

Complete example of assigning an NVMe device to a container:

```powershell
# 1. Prepare the device
$deviceId = "PCI\VEN_144D&DEV_A808&SUBSYS_A801144D&REV_00\4&2A5A8718&0&0008"
Disable-PnpDevice -InstanceId $deviceId -Confirm:$false

# 2. Create container with NVMe device
ctr run --nvme-device $deviceId `
    --runtime=io.containerd.runhcs.v1 `
    --isolated `
    mcr.microsoft.com/windows/servercore:ltsc2022 `
    nvme-test `
    powershell

# 3. Inside container - verify device
Get-PhysicalDisk | Where-Object { $_.BusType -eq "NVMe" }

# 4. Cleanup
ctr container rm nvme-test
Enable-PnpDevice -InstanceId $deviceId -Confirm:$false
```

## Integration with CRI

The NVMe device assignment is also available through CRI (Container Runtime Interface):

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: nvme-pod
spec:
  containers:
  - name: nvme-container
    image: mcr.microsoft.com/windows/servercore:ltsc2022
    resources:
      limits:
        example.com/nvme-device: "1"
```

Note: CRI integration requires additional configuration and device plugin implementation.

## Security Considerations

- **Device isolation**: Assigned devices are isolated from host OS
- **Access control**: Container has full access to assigned device
- **Data security**: Ensure device data is properly secured
- **Resource limits**: Monitor device resource usage

## Performance Considerations

- **Direct access**: No virtualization overhead for device I/O
- **Bandwidth**: Full device bandwidth available to container
- **Latency**: Minimal latency due to direct assignment
- **Scalability**: Limited by available devices and VM resources
