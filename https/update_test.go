package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestPrepareUpdatePreservesSettingsAndBackupOnRetry(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "kernel.toml")
	original := "[node]\nid = 'keep-node'\n[network]\nmain_port = 8080\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := prepareUpdate(root); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := os.ReadFile(path)
	var config map[string]any
	if err := toml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	network := config["network"].(map[string]any)
	if network["https_port"] != int64(0) || network["main_port"] != int64(8080) || config["node"].(map[string]any)["id"] != "keep-node" {
		t.Fatalf("settings changed: %#v", config)
	}
	backup, _ := os.ReadFile(filepath.Join(root, "node/docker/native-https-upgrade/kernel.toml"))
	if string(backup) != original {
		t.Fatal("original configuration was not preserved")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("configuration is not private")
	}
}

func TestPrepareUpdateRejectsCustomTLSBeforeChangingConfiguration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "kernel.toml")
	original := "[network]\nhttps_port = 9443\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if prepareUpdate(root) == nil {
		t.Fatal("custom configuration accepted")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != original {
		t.Fatal("rejected configuration changed")
	}
}
