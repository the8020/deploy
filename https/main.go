// One-time conversion of the bundled Caddy deployment's private state.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "Usage: https-migrate <legacy-config.toml> <legacy-state-directory> <new-private-output-file>")
		os.Exit(2)
	}
	if err := exportLegacy(os.Args[1], os.Args[2], os.Args[3]); err != nil {
		fmt.Fprintln(os.Stderr, "HTTPS migration:", err)
		fmt.Fprintln(os.Stderr, "Original files are unchanged. Retain the old deployment until migration succeeds.")
		os.Exit(1)
	}
}

func exportLegacy(config, directory, output string) error {
	raw, err := readLegacy(config, directory)
	if err != nil || raw == nil {
		return err
	}
	f, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	if closeError := f.Close(); err == nil {
		err = closeError
	}
	if err != nil {
		_ = os.Remove(output)
	}
	return err
}
