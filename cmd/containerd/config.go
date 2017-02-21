package main

import "github.com/BurntSushi/toml"

func defaultConfig() *config {
	return &config{
		Root:  "/var/lib/containerd",
		State: "/run/containerd",
		GRPC: grpcConfig{
			Socket: "/run/containerd/containerd.sock",
		},
		Debug: debug{
			Level:  "info",
			Socket: "/run/containerd/debug.sock",
		},
	}
}

// loadConfig loads the config from the provided path
func loadConfig(path string) error {
	_, err := toml.DecodeFile(path, conf)
	if err != nil {
		return err
	}
	return nil
}

// config specifies the containerd configuration file in the TOML format.
// It contains fields to configure various subsystems and containerd as a whole.
type config struct {
	// State is the path to a directory where containerd will store runtime state
	State string `toml:"state"`
	// Root is the path to a directory where containerd will store persistent data
	Root string `toml:"root"`
	// GRPC configuration settings
	GRPC grpcConfig `toml:"grpc"`
	// Debug and profiling settings
	Debug debug `toml:"debug"`
	// Metrics and monitoring settings
	Metrics metricsConfig `toml:"metrics"`
}

type grpcConfig struct {
	Socket string `toml:"socket"`
}

type debug struct {
	Socket string `toml:"socket"`
	Level  string `toml:"level"`
}

type metricsConfig struct {
	Address string `toml:"address"`
}
