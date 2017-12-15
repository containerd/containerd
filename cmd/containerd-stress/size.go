package main

import (
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

var binaries = []string{
	"ctr",
	"containerd",
	"containerd-shim",
}

// checkBinarySizes checks and reports the binary sizes for the containerd compiled binaries to prometheus
func checkBinarySizes() {
	for _, name := range binaries {
		fi, err := os.Stat(filepath.Join("/usr/local/bin", name))
		if err != nil {
			logrus.WithError(err).Error("stat binary")
			continue
		}
		binarySizeGauge.WithValues(name).Set(float64(fi.Size()))
	}
}
