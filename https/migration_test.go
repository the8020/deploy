package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const migrationConfig = `[limits]
new_names_per_hour = 0
max_names = 7
concurrent_checks = 3
check_timeout_seconds = 2.5
[tls]
acme_ca = "https://acme-v02.api.letsencrypt.org/directory"
ip_address = "::ffff:192.168.1.2"
`

func migrationWrite(t *testing.T, root, path string, data []byte) {
	t.Helper()
	path = filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func migrationCertificate(t *testing.T, name string) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
}

func TestLegacyMigrationPreservesMaterialAndBudgets(t *testing.T) {
	root := t.TempDir()
	config, directory := filepath.Join(root, "https.toml"), filepath.Join(root, "state")
	if raw, err := readLegacy(config, directory); err != nil || raw != nil {
		t.Fatal("fresh image unexpectedly has migration state", err)
	}
	migrationWrite(t, root, "https.toml", []byte(migrationConfig))
	migrationWrite(t, directory, "admissions.json", []byte(`{"names":{"example.test":true,"pending.test":true},"attempts":[1700000000,1700000001]}`))
	chain, key := migrationCertificate(t, "example.test")
	issuer := "acme-v02.api.letsencrypt.org-directory"
	base := "caddy/certificates/" + issuer + "/example.test/example.test"
	migrationWrite(t, directory, base+".crt", chain)
	migrationWrite(t, directory, base+".key", key)
	migrationWrite(t, directory, base+".json", []byte(`{"sans":["example.test"]}`))
	account := "caddy/acme/" + issuer + "/users/default/default"
	migrationWrite(t, directory, account+".key", key)
	migrationWrite(t, directory, account+".json", []byte(`{"status":"valid","contact":["mailto:admin@example.test"],"location":"https://acme-v02.api.letsencrypt.org/acme/acct/123"}`))
	provided, providedKey := migrationCertificate(t, "*.provided.test")
	migrationWrite(t, directory, "provided/custom.pem", append(append([]byte{}, provided...), providedKey...))
	before := migrationHashes(t, directory)
	raw, err := readLegacy(config, directory)
	if err != nil {
		t.Fatal(err)
	}
	var state legacyImport
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	p := state.Configuration
	if !p.Enabled || !p.LetsEncrypt || p.AttemptsPerHour != 0 || p.MaximumNames != 7 || p.ConcurrentChecks != 3 || p.CheckTimeoutSeconds != 2.5 || p.IPAddress != "192.168.1.2" || p.Email != "admin@example.test" {
		t.Fatal("migration changed policy")
	}
	if len(state.Attempts) != 2 || state.Attempts[0] != 1700000000000 || len(state.Admitted) != 2 || state.Admitted[1] != "pending.test" {
		t.Fatal("migration reset admission state")
	}
	if len(state.Accounts) != 1 || state.Accounts[0].Key != string(key) || state.Accounts[0].AccountURL != "https://acme-v02.api.letsencrypt.org/acme/acct/123" {
		t.Fatal("migration replaced ACME identity")
	}
	if len(state.Certificates) != 2 || state.Certificates[0].Chain != string(chain) || state.Certificates[0].Key != string(key) || state.Certificates[0].Source != "acme" || state.Certificates[1].Source != "provided" || state.Certificates[1].Name != "*.provided.test" {
		t.Fatal("migration changed certificates")
	}
	again, err := readLegacy(config, directory)
	if err != nil || !bytes.Equal(again, raw) || !bytes.Equal(before, migrationHashes(t, directory)) {
		t.Fatal("migration is not deterministic and read-only", err)
	}
	// A matching explicitly supplied copy retains manual ownership.
	migrationWrite(t, directory, "provided/example.pem", append(append([]byte{}, chain...), key...))
	raw, err = readLegacy(config, directory)
	if err != nil || json.Unmarshal(raw, &state) != nil || len(state.Certificates) != 2 || state.Certificates[0].Source != "provided" {
		t.Fatal("identical supplied certificate was not retained", err)
	}
}

func TestLegacyExportUsesPrivateFileAndDoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	config, state, output := filepath.Join(root, "config.toml"), filepath.Join(root, "state"), filepath.Join(root, "output.json")
	if err := exportLegacy(config, state, output); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("fresh system produced migration input")
	}
	migrationWrite(t, root, "config.toml", []byte(migrationConfig))
	if err := exportLegacy(config, state, output); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(output)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("migration input is not private", err)
	}
	before, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := exportLegacy(config, state, output); err == nil {
		t.Fatal("migration replaced an existing output")
	}
	after, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("existing output changed", err)
	}
}

func migrationHashes(t *testing.T, directory string) []byte {
	t.Helper()
	files, err := legacyFiles(directory)
	if err != nil {
		t.Fatal(err)
	}
	hashes := map[string][32]byte{}
	for name := range files {
		raw, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		hashes[name] = sha256.Sum256(raw)
	}
	raw, _ := json.Marshal(hashes)
	return raw
}

func TestLegacyMigrationRejectsUnsupportedState(t *testing.T) {
	for _, scenario := range []string{"missing-config", "http-ca", "invalid-state", "oversized-state", "unknown-ca", "missing-key", "multiple-accounts", "symlink", "mismatched-key", "partial-ca"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			config := filepath.Join(root, "config.toml")
			directory := filepath.Join(root, "state")
			migrationWrite(t, root, "config.toml", []byte(migrationConfig))
			migrationWrite(t, directory, "admissions.json", []byte(`{"names":{},"attempts":[]}`))
			switch scenario {
			case "missing-config":
				if err := os.Remove(config); err != nil {
					t.Fatal(err)
				}
			case "http-ca":
				migrationWrite(t, root, "config.toml", []byte(strings.ReplaceAll(migrationConfig, "https://", "http://")))
			case "invalid-state":
				migrationWrite(t, directory, "admissions.json", []byte(`{"names":null}`))
			case "oversized-state":
				migrationWrite(t, directory, "admissions.json", bytes.Repeat([]byte{'x'}, 1<<20+1))
			case "unknown-ca", "missing-key", "mismatched-key":
				issuer := "acme-v02.api.letsencrypt.org-directory"
				if scenario == "unknown-ca" {
					issuer = "unknown-directory"
				}
				base := "caddy/certificates/" + issuer + "/example.test/example.test"
				chain, key := migrationCertificate(t, "example.test")
				if scenario == "mismatched-key" {
					_, key = migrationCertificate(t, "other.test")
				}
				migrationWrite(t, directory, base+".crt", chain)
				if scenario != "missing-key" {
					migrationWrite(t, directory, base+".key", key)
				}
			case "multiple-accounts":
				for _, user := range []string{"first", "second"} {
					base := "caddy/acme/acme-v02.api.letsencrypt.org-directory/users/" + user + "/" + user
					migrationWrite(t, directory, base+".json", []byte(`{"status":"valid","location":"https://ca.test/account"}`))
					migrationWrite(t, directory, base+".key", []byte("not logged"))
				}
			case "symlink":
				if err := os.Symlink(config, filepath.Join(directory, "link")); err != nil {
					t.Fatal(err)
				}
			case "partial-ca":
				migrationWrite(t, directory, "caddy/pki/authorities/local/root.crt", []byte("not logged"))
			}
			if _, err := readLegacy(config, directory); err == nil || strings.Contains(err.Error(), "not logged") {
				t.Fatal("unsupported state was accepted or private input entered the error")
			}
		})
	}
}

func TestLegacyMigrationReadsRealCaddyLocalState(t *testing.T) {
	binary, err := exec.LookPath("caddy")
	if err != nil {
		t.Skip("Caddy fixture binary unavailable")
	}
	root := t.TempDir()
	directory := filepath.Join(root, "state")
	migrationWrite(t, root, "config.toml", []byte(migrationConfig))
	config := map[string]any{
		"admin":   map[string]any{"disabled": true},
		"storage": map[string]any{"module": "file_system", "root": filepath.Join(directory, "caddy")},
		"apps": map[string]any{
			"pki": map[string]any{"certificate_authorities": map[string]any{"local": map[string]any{"install_trust": false}}},
			"tls": map[string]any{
				"certificates": map[string]any{"automate": []string{"localhost"}},
				"automation":   map[string]any{"policies": []any{map[string]any{"subjects": []string{"localhost"}, "issuers": []any{map[string]any{"module": "internal"}}}}},
			},
		},
	}
	raw, _ := json.Marshal(config)
	migrationWrite(t, root, "caddy.json", raw)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "run", "--config", filepath.Join(root, "caddy.json"))
	command.Env = append(os.Environ(), "GOMEMLIMIT=128MiB", "GOMAXPROCS=1", "XDG_DATA_HOME="+root, "XDG_CONFIG_HOME="+root)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Signal(os.Interrupt)
		if err := command.Wait(); err != nil {
			t.Errorf("Caddy fixture failed: %v\n%s", err, output.String())
		}
	}()
	certPath := filepath.Join(directory, "caddy/certificates/local/localhost/localhost.crt")
	for deadline := time.Now().Add(10 * time.Second); ; {
		if _, err := os.Stat(certPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Caddy did not persist its local certificate")
		}
		time.Sleep(50 * time.Millisecond)
	}
	before := migrationHashes(t, directory)
	raw, err = readLegacy(filepath.Join(root, "config.toml"), directory)
	if err != nil {
		t.Fatal(err)
	}
	var state legacyImport
	if json.Unmarshal(raw, &state) != nil || state.Authority == nil || len(state.Certificates) != 1 || state.Certificates[0].Source != "local" || state.Certificates[0].Name != "localhost" {
		t.Fatal("real Caddy identity was not imported")
	}
	if !bytes.Equal(before, migrationHashes(t, directory)) {
		t.Fatal("migration modified the original Caddy state")
	}
}
