package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func build() error {
	out, err := exec.Command("make").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, out)
	}
	return nil
}

const tarFormat = "%s-%s.%s-%s.tar.gz"

func tarRelease(projectName, tag string) (string, error) {
	path := fmt.Sprintf(tarFormat, strings.ToLower(projectName), tag, runtime.GOOS, runtime.GOARCH)
	out, err := exec.Command("tar", "-zcf", path, "bin/").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %s", err, out)
	}
	return path, nil
}

func hash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	s := sha256.New()
	if _, err := io.Copy(s, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(s.Sum(nil)), nil
}
