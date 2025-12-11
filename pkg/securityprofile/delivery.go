package securityprofile

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Delivery struct {
	labelPrefix string
	targetDir   string
}

type Errors struct {
	NotFound    error
	InvalidData error
}

func New(labelPrefix, targetDir string) (*Delivery, error) {
	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return nil, fmt.Errorf("resolve target directory: %w", err)
	}
	return &Delivery{
		labelPrefix: labelPrefix,
		targetDir:   absTarget,
	}, nil
}

func (d *Delivery) Materialize(ref string, labels map[string]string, errs Errors) (string, error) {
	ref = strings.TrimPrefix(ref, d.targetDir+"/")
	key := d.labelPrefix + ref
	value := strings.TrimSpace(labels[key])
	if value == "" {
		if errs.NotFound != nil {
			return "", fmt.Errorf("%w: %s", errs.NotFound, key)
		}
		return "", fmt.Errorf("profile not found: %s", key)
	}
	data, err := decodeBase64(value)
	if err != nil {
		if errs.InvalidData != nil {
			return "", fmt.Errorf("%w: %s", errs.InvalidData, "expected base64")
		}
		return "", fmt.Errorf("invalid profile data: %w", err)
	}
	dest := filepath.Join(d.targetDir, ref)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", fmt.Errorf("create profile directory: %w", err)
	}
	if err := os.WriteFile(dest, data, defaultFileMode); err != nil {
		return "", fmt.Errorf("write profile %q: %w", dest, err)
	}
	return dest, nil
}

const defaultFileMode = 0o640

func decodeBase64(value string) ([]byte, error) {
	if data, err := base64.StdEncoding.DecodeString(value); err == nil {
		return data, nil
	}
	return nil, fmt.Errorf("decode base64")
}
