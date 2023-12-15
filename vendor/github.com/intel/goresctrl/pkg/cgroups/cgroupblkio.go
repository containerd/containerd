// Copyright 2020-2021 Intel Corporation. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cgroups

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// cgroups blkio parameter filenames.
var blkioWeightFiles = []string{"blkio.bfq.weight", "blkio.weight"}
var blkioWeightDeviceFiles = []string{"blkio.bfq.weight_device", "blkio.weight_device"}
var blkioThrottleReadBpsFiles = []string{"blkio.throttle.read_bps_device"}
var blkioThrottleWriteBpsFiles = []string{"blkio.throttle.write_bps_device"}
var blkioThrottleReadIOPSFiles = []string{"blkio.throttle.read_iops_device"}
var blkioThrottleWriteIOPSFiles = []string{"blkio.throttle.write_iops_device"}

// BlockIOParameters contains cgroups blockio controller parameters.
//
// Effects of Weight and Rate values in SetBlkioParameters():
// Value  |  Effect
// -------+-------------------------------------------------------------------
//
//	  -1  |  Do not write to cgroups, value is missing.
//	   0  |  Write to cgroups, will clear the setting as specified in cgroups blkio interface.
//	other |  Write to cgroups, sets the value.
type BlockIOParameters struct {
	Weight                  int64
	WeightDevice            DeviceWeights
	ThrottleReadBpsDevice   DeviceRates
	ThrottleWriteBpsDevice  DeviceRates
	ThrottleReadIOPSDevice  DeviceRates
	ThrottleWriteIOPSDevice DeviceRates
}

// DeviceWeight contains values for
// - blkio.[io-scheduler].weight
type DeviceWeight struct {
	Major  int64
	Minor  int64
	Weight int64
}

// DeviceRate contains values for
// - blkio.throttle.read_bps_device
// - blkio.throttle.write_bps_device
// - blkio.throttle.read_iops_device
// - blkio.throttle.write_iops_device
type DeviceRate struct {
	Major int64
	Minor int64
	Rate  int64
}

// DeviceWeights contains weights for devices.
type DeviceWeights []DeviceWeight

// DeviceRates contains throttling rates for devices.
type DeviceRates []DeviceRate

// DeviceParameters interface provides functions common to DeviceWeights and DeviceRates.
type DeviceParameters interface {
	Append(maj, min, val int64)
	Update(maj, min, val int64)
}

// Append appends (major, minor, value) to DeviceWeights slice.
func (w *DeviceWeights) Append(maj, min, val int64) {
	*w = append(*w, DeviceWeight{Major: maj, Minor: min, Weight: val})
}

// Append appends (major, minor, value) to DeviceRates slice.
func (r *DeviceRates) Append(maj, min, val int64) {
	*r = append(*r, DeviceRate{Major: maj, Minor: min, Rate: val})
}

// Update updates device weight in DeviceWeights slice, or appends it if not found.
func (w *DeviceWeights) Update(maj, min, val int64) {
	for index, devWeight := range *w {
		if devWeight.Major == maj && devWeight.Minor == min {
			(*w)[index].Weight = val
			return
		}
	}
	w.Append(maj, min, val)
}

// Update updates device rate in DeviceRates slice, or appends it if not found.
func (r *DeviceRates) Update(maj, min, val int64) {
	for index, devRate := range *r {
		if devRate.Major == maj && devRate.Minor == min {
			(*r)[index].Rate = val
			return
		}
	}
	r.Append(maj, min, val)
}

// NewBlockIOParameters creates new BlockIOParameters instance.
func NewBlockIOParameters() BlockIOParameters {
	return BlockIOParameters{
		Weight: -1,
	}
}

// NewDeviceWeight creates new DeviceWeight instance.
func NewDeviceWeight() DeviceWeight {
	return DeviceWeight{
		Major:  -1,
		Minor:  -1,
		Weight: -1,
	}
}

// NewDeviceRate creates new DeviceRate instance.
func NewDeviceRate() DeviceRate {
	return DeviceRate{
		Major: -1,
		Minor: -1,
		Rate:  -1,
	}
}

type devMajMin struct {
	Major int64
	Minor int64
}

// ResetBlkioParameters adds new, changes existing and removes missing blockIO parameters in cgroupsDir.
func ResetBlkioParameters(groupDir string, blockIO BlockIOParameters) error {
	errs := []error{}
	oldBlockIO, _ := GetBlkioParameters(groupDir)
	newBlockIO := NewBlockIOParameters()
	newBlockIO.Weight = blockIO.Weight
	newBlockIO.WeightDevice = resetDevWeights(oldBlockIO.WeightDevice, blockIO.WeightDevice)
	newBlockIO.ThrottleReadBpsDevice = resetDevRates(oldBlockIO.ThrottleReadBpsDevice, blockIO.ThrottleReadBpsDevice)
	newBlockIO.ThrottleWriteBpsDevice = resetDevRates(oldBlockIO.ThrottleWriteBpsDevice, blockIO.ThrottleWriteBpsDevice)
	newBlockIO.ThrottleReadIOPSDevice = resetDevRates(oldBlockIO.ThrottleReadIOPSDevice, blockIO.ThrottleReadIOPSDevice)
	newBlockIO.ThrottleWriteIOPSDevice = resetDevRates(oldBlockIO.ThrottleWriteIOPSDevice, blockIO.ThrottleWriteIOPSDevice)
	errs = append(errs, SetBlkioParameters(groupDir, newBlockIO))
	return errors.Join(errs...)
}

// resetDevWeights adds wanted weight parameters to new and resets unwanted weights.
func resetDevWeights(old, wanted []DeviceWeight) []DeviceWeight {
	new := []DeviceWeight{}
	seenDev := map[devMajMin]bool{}
	for _, wdp := range wanted {
		seenDev[devMajMin{wdp.Major, wdp.Minor}] = true
		new = append(new, wdp)
	}
	for _, wdp := range old {
		if !seenDev[devMajMin{wdp.Major, wdp.Minor}] {
			new = append(new, DeviceWeight{wdp.Major, wdp.Minor, 0})
		}
	}
	return new
}

// resetDevRates adds wanted rate parameters to new and resets unwanted rates.
func resetDevRates(old, wanted []DeviceRate) []DeviceRate {
	new := []DeviceRate{}
	seenDev := map[devMajMin]bool{}
	for _, rdp := range wanted {
		new = append(new, rdp)
		seenDev[devMajMin{rdp.Major, rdp.Minor}] = true
	}
	for _, rdp := range old {
		if !seenDev[devMajMin{rdp.Major, rdp.Minor}] {
			new = append(new, DeviceRate{rdp.Major, rdp.Minor, 0})
		}
	}
	return new
}

// GetBlkioParameters returns BlockIO parameters from files in cgroups blkio controller directory.
func GetBlkioParameters(group string) (BlockIOParameters, error) {
	errs := []error{}
	blockIO := NewBlockIOParameters()

	errs = append(errs, readWeight(group, blkioWeightFiles, &blockIO.Weight))
	errs = append(errs, readDeviceParameters(group, blkioWeightDeviceFiles, &blockIO.WeightDevice))
	errs = append(errs, readDeviceParameters(group, blkioThrottleReadBpsFiles, &blockIO.ThrottleReadBpsDevice))
	errs = append(errs, readDeviceParameters(group, blkioThrottleWriteBpsFiles, &blockIO.ThrottleWriteBpsDevice))
	errs = append(errs, readDeviceParameters(group, blkioThrottleReadIOPSFiles, &blockIO.ThrottleReadIOPSDevice))
	errs = append(errs, readDeviceParameters(group, blkioThrottleWriteIOPSFiles, &blockIO.ThrottleWriteIOPSDevice))
	return blockIO, errors.Join(errs...)
}

// readWeight parses int64 from a cgroups entry.
func readWeight(groupDir string, filenames []string, rv *int64) error {
	contents, err := readFirstFile(groupDir, filenames)
	if err != nil {
		return err
	}
	parsed, err := strconv.ParseInt(strings.TrimSuffix(contents, "\n"), 10, 64)
	if err != nil {
		return fmt.Errorf("parsing weight from %#v found in %v failed: %w", contents, filenames, err)
	}
	*rv = parsed
	return nil
}

// readDeviceParameters parses device lines used for weights and throttling rates.
func readDeviceParameters(groupDir string, filenames []string, params DeviceParameters) error {
	errs := []error{}
	contents, err := readFirstFile(groupDir, filenames)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(contents, "\n") {
		// Device weight files may have "default NNN" line at the beginning. Skip it.
		if line == "" || strings.HasPrefix(line, "default ") {
			continue
		}
		// Expect syntax MAJOR:MINOR VALUE
		devVal := strings.Split(line, " ")
		if len(devVal) != 2 {
			errs = append(errs, fmt.Errorf("invalid line %q, single space expected", line))
			continue
		}
		majMin := strings.Split(devVal[0], ":")
		if len(majMin) != 2 {
			errs = append(errs, fmt.Errorf("invalid line %q, single colon expected before space", line))
			continue
		}
		major, majErr := strconv.ParseInt(majMin[0], 10, 64)
		minor, minErr := strconv.ParseInt(majMin[1], 10, 64)
		value, valErr := strconv.ParseInt(devVal[1], 10, 64)
		if majErr != nil || minErr != nil || valErr != nil {
			errs = append(errs, fmt.Errorf("invalid number when parsing \"major:minor value\" from \"%s:%s %s\"", majMin[0], majMin[1], devVal[1]))
			continue
		}
		params.Append(major, minor, value)
	}
	return errors.Join(errs...)
}

// readFirstFile returns contents of the first successfully read entry.
func readFirstFile(groupDir string, filenames []string) (string, error) {
	errs := []error{}
	// If reading all the files fails, return list of read errors.
	for _, filename := range filenames {
		content, err := Blkio.Group(groupDir).Read(filename)
		if err == nil {
			return content, nil
		}
		errs = append(errs, err)
	}
	err := errors.Join(errs...)
	if err != nil {
		return "", fmt.Errorf("could not read any of files %q: %w", filenames, err)
	}
	return "", nil
}

// SetBlkioParameters writes BlockIO parameters to files in cgroups blkio contoller directory.
func SetBlkioParameters(group string, blockIO BlockIOParameters) error {
	errs := []error{}
	if blockIO.Weight >= 0 {
		errs = append(errs, writeFirstFile(group, blkioWeightFiles, "%d", blockIO.Weight))
	}
	for _, wd := range blockIO.WeightDevice {
		errs = append(errs, writeFirstFile(group, blkioWeightDeviceFiles, "%d:%d %d", wd.Major, wd.Minor, wd.Weight))
	}
	for _, rd := range blockIO.ThrottleReadBpsDevice {
		errs = append(errs, writeFirstFile(group, blkioThrottleReadBpsFiles, "%d:%d %d", rd.Major, rd.Minor, rd.Rate))
	}
	for _, rd := range blockIO.ThrottleWriteBpsDevice {
		errs = append(errs, writeFirstFile(group, blkioThrottleWriteBpsFiles, "%d:%d %d", rd.Major, rd.Minor, rd.Rate))
	}
	for _, rd := range blockIO.ThrottleReadIOPSDevice {
		errs = append(errs, writeFirstFile(group, blkioThrottleReadIOPSFiles, "%d:%d %d", rd.Major, rd.Minor, rd.Rate))
	}
	for _, rd := range blockIO.ThrottleWriteIOPSDevice {
		errs = append(errs, writeFirstFile(group, blkioThrottleWriteIOPSFiles, "%d:%d %d", rd.Major, rd.Minor, rd.Rate))
	}
	return errors.Join(errs...)
}

// writeFirstFile writes content to the first existing file in the list under groupDir.
func writeFirstFile(groupDir string, filenames []string, format string, args ...interface{}) error {
	errs := []error{}
	// Returns list of errors from writes, list of single error due to all filenames missing or nil on success.
	for _, filename := range filenames {
		if err := Blkio.Group(groupDir).Write(filename, format, args...); err != nil {
			errs = append(errs, err)
			continue
		}
		return nil
	}
	err := errors.Join(errs...)
	if err != nil {
		data := fmt.Sprintf(format, args...)
		return fmt.Errorf("writing all files %v failed, errors: %w, content %q", filenames, err, data)
	}
	return nil
}
