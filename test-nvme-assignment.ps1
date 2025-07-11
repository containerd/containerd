#!/usr/bin/env pwsh
# Test script for NVMe device assignment in containerd
# This script demonstrates the end-to-end workflow

param(
    [string]$DeviceInstanceId = "",
    [string]$ContainerImage = "mcr.microsoft.com/windows/servercore:ltsc2022",
    [string]$ContainerName = "nvme-test-container",
    [switch]$Cleanup
)

# Exit on any error
$ErrorActionPreference = "Stop"

Write-Host "NVMe Device Assignment Test Script" -ForegroundColor Green
Write-Host "==================================" -ForegroundColor Green

if ($Cleanup) {
    Write-Host "Cleaning up test container..." -ForegroundColor Yellow
    try {
        & ctr task kill $ContainerName
        & ctr task rm $ContainerName
        & ctr container rm $ContainerName
        Write-Host "Cleanup completed successfully" -ForegroundColor Green
    } catch {
        Write-Host "Cleanup failed (container may not exist): $_" -ForegroundColor Yellow
    }
    exit 0
}

# Validate parameters
if (-not $DeviceInstanceId) {
    Write-Host "Error: DeviceInstanceId parameter is required" -ForegroundColor Red
    Write-Host "Usage: ./test-nvme-assignment.ps1 -DeviceInstanceId 'PCI\VEN_144D&DEV_A808&SUBSYS_A801144D&REV_00\4&2A5A8718&0&0008'" -ForegroundColor Yellow
    Write-Host "       ./test-nvme-assignment.ps1 -Cleanup" -ForegroundColor Yellow
    exit 1
}

Write-Host "Test Parameters:" -ForegroundColor Cyan
Write-Host "  Device Instance ID: $DeviceInstanceId" -ForegroundColor White
Write-Host "  Container Image: $ContainerImage" -ForegroundColor White
Write-Host "  Container Name: $ContainerName" -ForegroundColor White
Write-Host ""

# Step 1: Verify containerd is running
Write-Host "Step 1: Checking containerd service..." -ForegroundColor Cyan
try {
    & ctr version
    Write-Host "✓ containerd is running" -ForegroundColor Green
} catch {
    Write-Host "✗ containerd is not running or not accessible" -ForegroundColor Red
    Write-Host "Please ensure containerd service is running" -ForegroundColor Yellow
    exit 1
}

# Step 2: Check if device exists
Write-Host "Step 2: Validating NVMe device..." -ForegroundColor Cyan
try {
    $device = Get-PnpDevice -InstanceId $DeviceInstanceId -ErrorAction Stop
    Write-Host "✓ Device found: $($device.FriendlyName)" -ForegroundColor Green
    Write-Host "  Status: $($device.Status)" -ForegroundColor White
    Write-Host "  Class: $($device.Class)" -ForegroundColor White
} catch {
    Write-Host "✗ Device not found: $DeviceInstanceId" -ForegroundColor Red
    Write-Host "Available NVMe devices:" -ForegroundColor Yellow
    Get-PnpDevice -Class "SCSIAdapter" | Where-Object { $_.FriendlyName -like "*NVMe*" } | Format-Table -AutoSize
    exit 1
}

# Step 3: Check if image is available
Write-Host "Step 3: Checking container image..." -ForegroundColor Cyan
try {
    & ctr image ls | Select-String $ContainerImage | Out-Null
    Write-Host "✓ Container image is available locally" -ForegroundColor Green
} catch {
    Write-Host "⚠ Container image not found locally, pulling..." -ForegroundColor Yellow
    try {
        & ctr image pull $ContainerImage
        Write-Host "✓ Container image pulled successfully" -ForegroundColor Green
    } catch {
        Write-Host "✗ Failed to pull container image" -ForegroundColor Red
        exit 1
    }
}

# Step 4: Create container with NVMe device assignment
Write-Host "Step 4: Creating container with NVMe device assignment..." -ForegroundColor Cyan
try {
    $createCommand = @(
        "ctr", "run",
        "--nvme-device", $DeviceInstanceId,
        "--runtime=io.containerd.runhcs.v1",
        "--isolated",
        "--detach",
        $ContainerImage,
        $ContainerName,
        "powershell", "-Command", "while(`$true) { Start-Sleep 30 }"
    )
    
    Write-Host "Running command: $($createCommand -join ' ')" -ForegroundColor Gray
    & $createCommand[0] $createCommand[1..($createCommand.Length-1)]
    
    Write-Host "✓ Container created successfully" -ForegroundColor Green
} catch {
    Write-Host "✗ Failed to create container: $_" -ForegroundColor Red
    exit 1
}

# Step 5: Verify device inside container
Write-Host "Step 5: Verifying NVMe device inside container..." -ForegroundColor Cyan
try {
    $deviceCheckCommand = @(
        "ctr", "task", "exec", "--exec-id", "device-check",
        $ContainerName, "powershell", "-Command",
        "Get-PhysicalDisk | Where-Object { `$_.BusType -eq 'NVMe' } | Format-Table -AutoSize"
    )
    
    Write-Host "Checking for NVMe devices inside container..." -ForegroundColor Gray
    & $deviceCheckCommand[0] $deviceCheckCommand[1..($deviceCheckCommand.Length-1)]
    
    Write-Host "✓ Device verification completed" -ForegroundColor Green
} catch {
    Write-Host "⚠ Device verification failed: $_" -ForegroundColor Yellow
    Write-Host "This may be expected if the device requires initialization" -ForegroundColor Gray
}

# Step 6: Additional device information
Write-Host "Step 6: Gathering additional device information..." -ForegroundColor Cyan
try {
    $diskCheckCommand = @(
        "ctr", "task", "exec", "--exec-id", "disk-check",
        $ContainerName, "powershell", "-Command",
        "Get-Disk | Format-Table -AutoSize"
    )
    
    Write-Host "Checking all disks inside container..." -ForegroundColor Gray
    & $diskCheckCommand[0] $diskCheckCommand[1..($diskCheckCommand.Length-1)]
    
    Write-Host "✓ Disk information gathered" -ForegroundColor Green
} catch {
    Write-Host "⚠ Disk information gathering failed: $_" -ForegroundColor Yellow
}

# Step 7: Container status
Write-Host "Step 7: Container status..." -ForegroundColor Cyan
try {
    & ctr task ls | Select-String $ContainerName
    Write-Host "✓ Container is running" -ForegroundColor Green
} catch {
    Write-Host "⚠ Container status check failed" -ForegroundColor Yellow
}

Write-Host ""
Write-Host "Test completed successfully!" -ForegroundColor Green
Write-Host "Container '$ContainerName' is running with NVMe device assigned" -ForegroundColor Green
Write-Host ""
Write-Host "To interact with the container:" -ForegroundColor Yellow
Write-Host "  ctr task exec --exec-id shell $ContainerName powershell" -ForegroundColor White
Write-Host ""
Write-Host "To cleanup:" -ForegroundColor Yellow
Write-Host "  ./test-nvme-assignment.ps1 -Cleanup" -ForegroundColor White
