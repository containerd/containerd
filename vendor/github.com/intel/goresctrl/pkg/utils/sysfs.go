/*
Copyright 2022 Intel Corporation

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

package utils

import (
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
)

const (
	SysfsUncoreBasepath = "/sys/devices/system/cpu/intel_uncore_frequency/"
)

func setCPUFreqValue(cpu ID, setting string, value int) error {
	path := fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq/%s", cpu, setting)
	return writeFileInt(path, value)
}

// GetCPUFreqValue returns information of the currently used CPU frequency
func GetCPUFreqValue(cpu ID, setting string) (int, error) {
	str := fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq/%s", cpu, setting)

	raw, err := ioutil.ReadFile(str)
	if err != nil {
		return 0, err
	}

	value, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, err
	}

	return value, nil
}

// SetCPUScalingMinFreq sets the scaling_min_freq value of a given CPU
func SetCPUScalingMinFreq(cpu ID, freq int) error {
	return setCPUFreqValue(cpu, "scaling_min_freq", freq)
}

// SetCPUScalingMaxFreq sets the scaling_max_freq value of a given CPU
func SetCPUScalingMaxFreq(cpu ID, freq int) error {
	return setCPUFreqValue(cpu, "scaling_max_freq", freq)
}

// SetCPUsScalingMinFreq sets the scaling_min_freq value of a given set of CPUs
func SetCPUsScalingMinFreq(cpus []ID, freq int) error {
	for _, cpu := range cpus {
		if err := SetCPUScalingMinFreq(cpu, freq); err != nil {
			return err
		}
	}

	return nil
}

// SetCPUsScalingMaxFreq sets the scaling_max_freq value of a given set of CPUs
func SetCPUsScalingMaxFreq(cpus []ID, freq int) error {
	for _, cpu := range cpus {
		if err := SetCPUScalingMaxFreq(cpu, freq); err != nil {
			return err
		}
	}

	return nil
}

// UncoreFreqAvailable returns true if the uncore frequency control functions are available.
func UncoreFreqAvailable() bool {
	_, err := os.Stat(SysfsUncoreBasepath)
	return err == nil
}

// SetUncoreMinFreq sets the minimum uncore frequency of a CPU die. Frequency is specified in kHz.
func SetUncoreMinFreq(pkg, die ID, freqKhz int) error {
	return setUncoreFreqValue(pkg, die, "min_freq_khz", freqKhz)
}

// SetUncoreMaxFreq sets the maximum uncore frequency of a CPU die. Frequency is specified in kHz.
func SetUncoreMaxFreq(pkg, die ID, freqKhz int) error {
	return setUncoreFreqValue(pkg, die, "max_freq_khz", freqKhz)
}

func uncoreFreqPath(pkg, die ID, attribute string) string {
	return fmt.Sprintf(SysfsUncoreBasepath+"package_%02d_die_%02d/%s", pkg, die, attribute)
}

func getUncoreFreqValue(pkg, die ID, attribute string) (int, error) {
	return readFileInt(uncoreFreqPath(pkg, die, attribute))
}

func setUncoreFreqValue(pkg, die ID, attribute string, value int) error {
	// Bounds checking
	if hwMinFreq, err := getUncoreFreqValue(pkg, die, "initial_min_freq_khz"); err != nil {
		return err
	} else if value < hwMinFreq {
		value = hwMinFreq
	}
	if hwMaxFreq, err := getUncoreFreqValue(pkg, die, "initial_max_freq_khz"); err != nil {
		return err
	} else if value > hwMaxFreq {
		value = hwMaxFreq
	}

	return writeFileInt(uncoreFreqPath(pkg, die, attribute), value)
}

func writeFileInt(path string, value int) error {
	return ioutil.WriteFile(path, []byte(strconv.Itoa(value)), 0644)
}

func readFileInt(path string) (int, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return 0, err
	}
	i, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 32)
	return int(i), err
}
