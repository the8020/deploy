package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const migrationLimit = 256 * 1024

type legacyPolicy struct {
	Enabled             bool     `json:"enabled"`
	LetsEncrypt         bool     `json:"letsEncrypt"`
	AllowedDomains      []string `json:"allowedDomains"`
	AttemptsPerHour     int      `json:"attemptsPerHour"`
	MaximumNames        int      `json:"maximumNames"`
	ConcurrentChecks    int      `json:"concurrentChecks"`
	CheckTimeoutSeconds float64  `json:"checkTimeoutSeconds"`
	Directory           string   `json:"directory"`
	Email               string   `json:"email"`
	IPAddress           string   `json:"ipAddress"`
}

type legacyPair struct {
	Certificate string `json:"certificate"`
	Key         string `json:"key"`
}

type legacyAccount struct {
	Directory  string `json:"directory"`
	AccountURL string `json:"accountUrl"`
	Email      string `json:"email"`
	Key        string `json:"key"`
}

type legacyCertificate struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Chain  string `json:"chain"`
	Key    string `json:"key"`
}

type legacyImport struct {
	Configuration legacyPolicy        `json:"configuration"`
	Attempts      []int64             `json:"attempts"`
	Admitted      []string            `json:"admitted"`
	Authority     *legacyPair         `json:"authority"`
	Accounts      []legacyAccount     `json:"accounts"`
	Certificates  []legacyCertificate `json:"certificates"`
}

// Read only: retain the original deployment state until its owner accepts import.
func readLegacy(configPath, directory string) ([]byte, error) {
	files, err := legacyFiles(directory)
	if err != nil {
		return nil, err
	}
	raw, err := legacyFile(configPath, 16384)
	if errors.Is(err, fs.ErrNotExist) && len(files) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("legacy HTTPS configuration is required: %w", err)
	}
	var config struct {
		Limits struct {
			Attempts   int     `toml:"new_names_per_hour"`
			Names      int     `toml:"max_names"`
			Concurrent int     `toml:"concurrent_checks"`
			Timeout    float64 `toml:"check_timeout_seconds"`
		} `toml:"limits"`
		TLS struct {
			CA      string `toml:"acme_ca"`
			Address string `toml:"ip_address"`
		} `toml:"tls"`
	}
	config.Limits.Attempts, config.Limits.Names = -1, -1
	if toml.NewDecoder(bytes.NewReader(raw)).DisallowUnknownFields().Decode(&config) != nil {
		return nil, errors.New("unsupported legacy HTTPS TOML configuration")
	}
	limits := config.Limits
	if limits.Attempts < 0 || limits.Attempts > 10000 || limits.Names < 0 || limits.Names > 10000 ||
		limits.Concurrent < 1 || limits.Concurrent > 16 || !(limits.Timeout >= 0.1 && limits.Timeout <= 30) {
		return nil, errors.New("legacy HTTPS limits cannot be represented; migrate explicitly")
	}
	if !legacyHTTPSURL(config.TLS.CA) {
		return nil, errors.New("legacy ACME directory must be an HTTPS URL")
	}
	if config.TLS.Address != "" {
		ip, err := netip.ParseAddr(config.TLS.Address)
		if err != nil || ip.Zone() != "" {
			return nil, errors.New("invalid legacy HTTPS IP address")
		}
		config.TLS.Address = ip.Unmap().String()
	}
	state := legacyImport{
		Configuration: legacyPolicy{true, true, []string{"*"}, limits.Attempts, limits.Names, limits.Concurrent, limits.Timeout, config.TLS.CA, "", config.TLS.Address},
		Attempts:      []int64{}, Admitted: []string{}, Accounts: []legacyAccount{}, Certificates: []legacyCertificate{},
	}
	read := func(name string, limit int) ([]byte, error) {
		return legacyFile(filepath.Join(directory, filepath.FromSlash(name)), limit)
	}
	if files["admissions.json"] {
		raw, err := read("admissions.json", 1<<20)
		if err != nil {
			return nil, err
		}
		var admission struct {
			Names    map[string]bool `json:"names"`
			Attempts []int64         `json:"attempts"`
		}
		if json.Unmarshal(raw, &admission) != nil || admission.Names == nil || len(admission.Names) > 10000 || len(admission.Attempts) > 10000 {
			return nil, errors.New("invalid or oversized legacy admission state")
		}
		for name, admitted := range admission.Names {
			if !admitted {
				return nil, errors.New("legacy admission state contains an unaccepted name")
			}
			state.Admitted = append(state.Admitted, name)
		}
		sort.Strings(state.Admitted)
		for _, seconds := range admission.Attempts {
			if seconds < 0 || seconds > 9007199254740 {
				return nil, errors.New("invalid legacy admission timestamp")
			}
			state.Attempts = append(state.Attempts, seconds*1000)
		}
	}
	const authority = "caddy/pki/authorities/local/root"
	if files[authority+".crt"] || files[authority+".key"] {
		cert, err := read(authority+".crt", migrationLimit)
		if err != nil {
			return nil, err
		}
		key, err := read(authority+".key", 65536)
		if err != nil {
			return nil, err
		}
		state.Authority = &legacyPair{string(cert), string(key)}
	}
	directories := map[string]string{}
	for _, ca := range []string{config.TLS.CA, "https://acme-v02.api.letsencrypt.org/directory", "https://acme-staging-v02.api.letsencrypt.org/directory"} {
		directories[legacyIssuerKey(ca)] = ca
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	accounts := map[string]bool{}
	for _, name := range names {
		parts := strings.Split(name, "/")
		switch {
		case strings.HasPrefix(name, "caddy/acme/") && strings.Contains(name, "/users/"):
			if len(parts) != 6 || (filepath.Ext(name) != ".key" && filepath.Ext(name) != ".json") {
				return nil, fmt.Errorf("unsupported legacy account file: %s", name)
			}
			base := strings.TrimSuffix(name, filepath.Ext(name))
			if !files[base+".key"] || !files[base+".json"] {
				return nil, fmt.Errorf("incomplete legacy account: %s", name)
			}
			if filepath.Ext(name) != ".json" {
				continue
			}
			ca := directories[parts[2]]
			if ca == "" || accounts[ca] || len(state.Accounts) >= 16 {
				return nil, errors.New("legacy accounts require one account per known ACME directory; migrate explicitly")
			}
			metadata, err := read(name, 16384)
			if err != nil {
				return nil, err
			}
			var account struct {
				Status   string   `json:"status"`
				Contact  []string `json:"contact"`
				Location string   `json:"location"`
			}
			if json.Unmarshal(metadata, &account) != nil || account.Status != "valid" || !legacyHTTPSURL(account.Location) || len(account.Contact) > 1 {
				return nil, errors.New("invalid or unsupported legacy ACME account")
			}
			email := ""
			if len(account.Contact) == 1 {
				if !strings.HasPrefix(account.Contact[0], "mailto:") {
					return nil, errors.New("unsupported legacy ACME account contact")
				}
				email = strings.TrimPrefix(account.Contact[0], "mailto:")
			}
			key, err := read(base+".key", 65536)
			if err != nil {
				return nil, err
			}
			state.Accounts = append(state.Accounts, legacyAccount{ca, account.Location, email, string(key)})
			accounts[ca] = true
			if ca == config.TLS.CA {
				state.Configuration.Email = email
			}
		case strings.HasPrefix(name, "caddy/certificates/"):
			ext := filepath.Ext(name)
			base := strings.TrimSuffix(name, ext)
			if len(parts) != 5 || (ext != ".crt" && ext != ".key" && ext != ".json") || !files[base+".crt"] || !files[base+".key"] {
				return nil, fmt.Errorf("unsupported or incomplete cached certificate: %s", name)
			}
			if ext != ".crt" {
				continue
			}
			source := "acme"
			if parts[2] == "local" {
				source = "local"
			} else if directories[parts[2]] == "" {
				return nil, errors.New("cached certificate uses an unknown ACME directory; migrate explicitly")
			}
			chain, err := read(name, migrationLimit)
			if err != nil {
				return nil, err
			}
			key, err := read(base+".key", 65536)
			if err != nil {
				return nil, err
			}
			if err := state.addCertificate(source, chain, key); err != nil {
				return nil, err
			}
		case strings.HasPrefix(name, "provided/"):
			if len(parts) != 2 || filepath.Ext(name) != ".pem" {
				return nil, fmt.Errorf("unsupported supplied certificate file: %s", name)
			}
			raw, err := read(name, migrationLimit)
			if err != nil {
				return nil, err
			}
			chain, key, err := splitLegacyBundle(raw)
			if err != nil {
				return nil, err
			}
			if err := state.addCertificate("provided", chain, key); err != nil {
				return nil, err
			}
		case strings.HasPrefix(name, "caddy/pki/"):
			if name != authority+".crt" && name != authority+".key" && name != "caddy/pki/authorities/local/intermediate.crt" && name != "caddy/pki/authorities/local/intermediate.key" {
				return nil, errors.New("unsupported legacy local authority; migrate explicitly")
			}
			if state.Authority == nil {
				return nil, errors.New("legacy local authority is missing its root certificate or key")
			}
		}
	}
	if len(state.Certificates) > 100 {
		return nil, errors.New("more than 100 legacy certificates; migrate explicitly")
	}
	if !files["admissions.json"] {
		for _, cert := range state.Certificates {
			if cert.Source == "acme" {
				return nil, errors.New("cached ACME certificates are missing their admission history; migrate explicitly")
			}
		}
	}
	raw, err = json.Marshal(state)
	if err == nil && len(raw) > migrationLimit {
		err = errors.New("legacy HTTPS state exceeds 256 KiB; migrate explicitly")
	}
	return raw, err
}

func (s *legacyImport) addCertificate(source string, chain, key []byte) error {
	pair, err := tls.X509KeyPair(chain, key)
	if err != nil {
		return errors.New("invalid legacy certificate or mismatched private key")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return errors.New("invalid legacy certificate")
	}
	var name string
	if len(leaf.DNSNames) > 0 {
		name = leaf.DNSNames[0]
	} else if len(leaf.IPAddresses) > 0 {
		name = leaf.IPAddresses[0].String()
	} else {
		return errors.New("legacy certificate has no supported SAN identity")
	}
	for i, previous := range s.Certificates {
		if previous.Name != name {
			continue
		}
		if previous.Chain == string(chain) && previous.Key == string(key) {
			if source == "provided" {
				s.Certificates[i].Source = source
			}
			return nil
		}
		return errors.New("competing legacy certificates for one name; migrate explicitly")
	}
	s.Certificates = append(s.Certificates, legacyCertificate{name, source, string(chain), string(key)})
	return nil
}

func splitLegacyBundle(raw []byte) ([]byte, []byte, error) {
	var chain, key []byte
	for len(bytes.TrimSpace(raw)) != 0 {
		raw = bytes.TrimSpace(raw)
		if !bytes.HasPrefix(raw, []byte("-----BEGIN ")) {
			return nil, nil, errors.New("invalid legacy PEM bundle")
		}
		block, rest := pem.Decode(raw)
		if block == nil || len(block.Headers) != 0 {
			return nil, nil, errors.New("invalid or encrypted legacy PEM bundle")
		}
		switch block.Type {
		case "CERTIFICATE":
			chain = append(chain, pem.EncodeToMemory(block)...)
		case "PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY":
			if key != nil {
				return nil, nil, errors.New("multiple private keys in legacy PEM bundle")
			}
			key = pem.EncodeToMemory(block)
		default:
			return nil, nil, errors.New("unsupported legacy PEM block")
		}
		raw = rest
	}
	return chain, key, nil
}

func legacyHTTPSURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && len(value) <= 2048 && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

// CertMagic's issuer directory encodes host and path, not the URL scheme.
func legacyIssuerKey(directory string) string {
	u, _ := url.Parse(directory)
	key := u.Host
	path := strings.Trim(strings.NewReplacer("/", "-", "\\", "-").Replace(u.Path), "-")
	if path != "" {
		key += "-" + path
	}
	key = strings.NewReplacer(" ", "_", "+", "_plus_", "*", "wildcard_", ":", "-", "..", "").Replace(strings.ToLower(strings.TrimSpace(key)))
	return regexp.MustCompile(`[^\w@.-]`).ReplaceAllString(key, "")
}

func legacyFile(path string, limit int) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return nil, fmt.Errorf("legacy file is not a bounded regular file: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err == nil && len(raw) > limit {
		err = fmt.Errorf("legacy file exceeds size limit: %s", path)
	}
	return raw, err
}

func legacyFiles(directory string) (map[string]bool, error) {
	files := map[string]bool{}
	entries := 0
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == directory {
			return nil
		}
		if err != nil {
			return err
		}
		entries++
		if entries > 2000 || (entry.Type()&fs.ModeSymlink) != 0 || (!entry.IsDir() && !entry.Type().IsRegular()) {
			return errors.New("unsupported or oversized legacy HTTPS directory")
		}
		if !entry.IsDir() {
			name, err := filepath.Rel(directory, path)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(name)] = true
		}
		return nil
	})
	return files, err
}
