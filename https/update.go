package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// The old proxy keeps its ports until the enclosing container restarts.
func prepareUpdate(root string) error {
	path := filepath.Join(root, "kernel.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var config map[string]any
	if err := toml.Unmarshal(raw, &config); err != nil {
		return err
	}
	if config == nil {
		config = map[string]any{}
	}
	network, ok := config["network"].(map[string]any)
	if !ok {
		if config["network"] != nil {
			return fmt.Errorf("invalid network settings")
		}
		network = map[string]any{}
		config["network"] = network
	}
	state := filepath.Join(root, "node/docker/native-https-upgrade")
	if err := os.MkdirAll(state, 0700); err != nil {
		return err
	}
	backup := filepath.Join(state, "kernel.toml")
	if _, err := os.Stat(backup); os.IsNotExist(err) {
		if network["https_port"] != nil {
			return fmt.Errorf("native HTTPS already has a port override; migrate this custom deployment explicitly")
		}
		if err := writeUpdateFile(backup, raw); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	network["https_port"] = 0
	encoded, err := toml.Marshal(config)
	if err != nil {
		return err
	}
	return writeUpdateFile(path, encoded)
}

func writeUpdateFile(path string, encoded []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".https-update-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if _, err = temporary.Write(encoded); err != nil {
		return err
	}
	if err = temporary.Sync(); err != nil {
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	if err = os.Rename(temporary.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
