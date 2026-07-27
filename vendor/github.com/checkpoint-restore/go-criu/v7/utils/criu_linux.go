package utils

import (
	"fmt"

	"github.com/checkpoint-restore/go-criu/v7"
	"github.com/checkpoint-restore/go-criu/v7/rpc"
	"google.golang.org/protobuf/proto"
)

// CheckForCRIU checks if CRIU is available and if it is as least the
// version as specified in the "version" parameter.
func CheckForCriu(version int) error {
	criuVersion, err := GetCriuVersion()
	if err != nil {
		return fmt.Errorf("failed to check for criu version: %w", err)
	}

	if criuVersion >= version {
		return nil
	}
	return fmt.Errorf("checkpoint/restore requires at least CRIU %d, current version is %d", version, criuVersion)
}

// Convenience function to easily check if memory tracking is supported.
func IsMemTrack() bool {
	features, err := criu.MakeCriu().FeatureCheck(
		&rpc.CriuFeatures{
			MemTrack: proto.Bool(true),
		},
	)
	if err != nil {
		return false
	}

	return features.GetMemTrack()
}

func GetCriuVersion() (int, error) {
	c := criu.MakeCriu()
	return c.GetCriuVersion()
}
