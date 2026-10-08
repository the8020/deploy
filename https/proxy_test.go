package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
)

func require(t *testing.T, ok bool, message any) {
	t.Helper()
	if !ok {
		t.Fatal(message)
	}
}
func checked(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func requireSockets(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Skip("this environment blocks loopback listeners")
	}
	checked(t, err)
	listener.Close()
}
func writeConfig(t *testing.T, path string, c configuration) {
	t.Helper()
	raw, err := toml.Marshal(c)
	checked(t, err)
	checked(t, os.WriteFile(path, raw, 0600))
}

func TestAdmission(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "https.toml")
	c, err := settings("https.toml")
	checked(t, err)
	writeConfig(t, path, c)
	a, err := newAdmission(path, directory)
	checked(t, err)
	checks := 0
	a.check = func(context.Context, string, string, []byte) bool { checks++; return false }
	ctx := context.Background()
	for _, name := range []string{"first.example.com", "second.example.com", "third.example.com"} {
		require(t, !a.allow(ctx, name), "failed check admitted a name")
	}
	require(t, checks == 2, "attempt quota must precede network work")
	a, err = newAdmission(path, directory)
	checked(t, err)
	a.check = func(context.Context, string, string, []byte) bool { checks++; return true }
	require(t, !a.allow(ctx, "third.example.com"), "quota was lost on restart")
	c.Limits.NewNames, c.Limits.MaxNames = 4, 1
	writeConfig(t, path, c)
	require(t, a.allow(ctx, "UPPER.example.com."), "hot configuration/canonical name failed")
	require(t, !a.allow(ctx, "other.example.com"), "maximum names was exceeded")
	c.Limits.NewNames, c.Limits.MaxNames = 0, 0
	writeConfig(t, path, c)
	require(t, a.allow(ctx, "upper.example.com"), "pause blocked renewal")
	checked(t, os.WriteFile(path, []byte("invalid toml ["), 0600))
	require(t, a.allow(ctx, "upper.example.com"), "invalid configuration blocked renewal")
	require(t, !a.allow(ctx, "other.example.com"), "invalid configuration admitted a new name")
	require(t, checks == 3, "known names retriggered network checks")
	for _, name := range []string{"localhost", "LOCALHOST.", "127.0.0.1", "::1"} {
		require(t, a.allow(ctx, name), "local certificate was denied")
	}
	for _, name := range []string{"*.example.com", "example.com/path", "a..com", "x.local", strings.Repeat("a", 254)} {
		require(t, !a.allow(ctx, name), "malformed name was admitted")
	}
	c.Limits.NewNames, c.Limits.MaxNames, c.Limits.Concurrent = 4, 10, 1
	writeConfig(t, path, c)
	entered, release, result := make(chan struct{}), make(chan struct{}), make(chan bool, 1)
	a.check = func(context.Context, string, string, []byte) bool { close(entered); <-release; return true }
	go func() { result <- a.allow(ctx, "waiting.example.com") }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("check did not start")
	}
	require(t, !a.allow(ctx, "concurrent.example.com"), "concurrent check limit was exceeded")
	close(release)
	require(t, <-result, "pending check was denied")

	// A corrupt state file must fail closed rather than reset enrollment quotas.
	checked(t, os.WriteFile(a.path, []byte("{"), 0600))
	_, err = newAdmission(path, directory)
	require(t, err != nil, "corrupt admission state was accepted")
}

func TestNetworkGuards(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "224.0.0.1", "240.0.0.1", "100.64.0.1",
		"192.0.2.1", "::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "2001:db8::1", "64:ff9b::a00:1"} {
		require(t, !publicIP(netip.MustParseAddr(address)), "unsafe address accepted: "+address)
	}
	for _, address := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		require(t, publicIP(netip.MustParseAddr(address)), "public address rejected: "+address)
	}
	original := net.DefaultResolver
	t.Cleanup(func() { net.DefaultResolver = original })
	// Return both public/private DNS answers through the actual Go resolver.
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			var length [2]byte
			if _, err := io.ReadFull(server, length[:]); err != nil {
				return
			}
			query := make([]byte, binary.BigEndian.Uint16(length[:]))
			if _, err := io.ReadFull(server, query); err != nil || len(query) < 16 {
				return
			}
			end := 12
			for end < len(query) && query[end] != 0 {
				end += int(query[end]) + 1
			}
			end += 5
			if end > len(query) {
				return
			}
			response := append([]byte{}, query[:end]...)
			binary.BigEndian.PutUint16(response[2:4], 0x8180)
			binary.BigEndian.PutUint16(response[10:12], 0)
			if binary.BigEndian.Uint16(query[end-4:end-2]) == 1 {
				binary.BigEndian.PutUint16(response[6:8], 2)
				for _, ip := range [][4]byte{{8, 8, 8, 8}, {10, 0, 0, 1}} {
					response = append(response, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4)
					response = append(response, ip[:]...)
				}
			}
			binary.BigEndian.PutUint16(length[:], uint16(len(response)))
			server.Write(append(length[:], response...))
		}()
		return client, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := publicAddresses(ctx, "mixed.example.com")
	require(t, err != nil && strings.Contains(err.Error(), "exclusively"), err)
	// DNS itself shares the total deadline; no subprocess or unbounded lookup.
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	require(t, !checkHost(ctx, "slow.example.com", "nonce", nil), "slow DNS succeeded")
	require(t, time.Since(started) < time.Second, "DNS ignored the deadline")
	t.Setenv("THE8020_USERNAME", "test-only")
	t.Setenv("THE8020_PASSWORD", "test-only")
	t.Setenv("THE8020_SIGNING_KEY", "test-only")
	for _, entry := range childEnvironment() {
		require(t, !strings.HasPrefix(entry, "THE8020_USERNAME=") && !strings.HasPrefix(entry, "THE8020_PASSWORD=") && !strings.HasPrefix(entry, "THE8020_SIGNING_KEY="), "proxy inherited bootstrap credentials")
	}
}

func TestHTTPProof(t *testing.T) {
	requireSockets(t)
	c, err := settings("https.toml")
	checked(t, err)
	directory := t.TempDir()
	path := filepath.Join(directory, "https.toml")
	writeConfig(t, path, c)
	a, err := newAdmission(path, directory)
	checked(t, err)
	gate := httptest.NewServer(a)
	defer gate.Close()
	_, port, err := net.SplitHostPort(gate.Listener.Addr().String())
	checked(t, err)
	nonce := strings.Repeat("b", 64)
	addresses := []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require(t, checkAddresses(ctx, "public.example.com", nonce, a.proof(nonce), addresses, port), "HTTP HMAC proof failed")
	require(t, !checkAddresses(ctx, "public.example.com", nonce, []byte("wrong"), addresses, port), "wrong HTTP proof accepted")
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, gate.URL+challenge+nonce, 302) }))
	defer redirect.Close()
	_, port, err = net.SplitHostPort(redirect.Listener.Addr().String())
	checked(t, err)
	require(t, !checkAddresses(ctx, "public.example.com", nonce, a.proof(nonce), addresses, port), "probe followed a redirect")
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	_, port, err = net.SplitHostPort(slow.Listener.Addr().String())
	checked(t, err)
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	require(t, !checkAddresses(ctx, "public.example.com", nonce, a.proof(nonce), addresses, port), "slow HTTP probe succeeded")
}

func TestCaddyConfiguration(t *testing.T) {
	caddy, _ := exec.LookPath("caddy")
	c, err := settings("https.toml")
	checked(t, err)
	for _, address := range []string{"", "8.8.8.8", "10.0.0.1"} {
		c.TLS.Address = address
		directory := t.TempDir()
		checked(t, os.Mkdir(filepath.Join(directory, "provided"), 0700))
		config := caddyConfig(c, directory, "127.0.0.1:2018", true, 80)
		raw, err := json.Marshal(config)
		checked(t, err)
		path := filepath.Join(directory, "caddy.json")
		checked(t, os.WriteFile(path, raw, 0600))
		if caddy != "" {
			cmd := exec.Command(caddy, "validate", "--config", path)
			cmd.Env = childEnvironment()
			output, err := cmd.CombinedOutput()
			require(t, err == nil, string(output))
		}
	}
}

func startProcess(t *testing.T, binary string, arguments []string, env []string, directory string) (*exec.Cmd, func()) {
	t.Helper()
	log, err := os.CreateTemp(directory, "process-*.log")
	checked(t, err)
	t.Cleanup(func() { log.Close() })
	cmd := exec.Command(binary, arguments...)
	cmd.Env, cmd.Stdout, cmd.Stderr = env, log, log
	checked(t, cmd.Start())
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	stop := func() {
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	}
	t.Cleanup(stop)
	return cmd, stop
}

func waitUntil(t *testing.T, check func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal(message)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func certificates(t *testing.T, directory string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	checked(t, filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[path] = raw
		}
		return nil
	}))
	return files
}

func TestCaddy(t *testing.T) {
	requireSockets(t)
	caddy, err := exec.LookPath("caddy")
	if err != nil {
		t.Skip("put checksum-verified Caddy on PATH for real TLS checks")
	}
	directory := t.TempDir()
	checked(t, os.Mkdir(filepath.Join(directory, "provided"), 0700))
	c, err := settings("https.toml")
	checked(t, err)
	path := filepath.Join(directory, "https.toml")
	writeConfig(t, path, c)
	a, err := newAdmission(path, directory)
	checked(t, err)
	gate := httptest.NewServer(a)
	defer gate.Close()
	backends := []*httptest.Server{}
	for _, name := range []string{"dev", "prod"} {
		name := name
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Upgrade") == "websocket" {
				conn, buffer, err := w.(http.Hijacker).Hijack()
				if err != nil {
					return
				}
				defer conn.Close()
				buffer.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n")
				buffer.Flush()
				payload := make([]byte, 5)
				if _, err := io.ReadFull(buffer, payload); err == nil {
					conn.Write(payload)
				}
				return
			}
			w.Header().Set("Set-Cookie", "session=test; Secure; HttpOnly")
			json.NewEncoder(w).Encode(object{"system": name, "host": r.Host, "scheme": r.Header.Get("X-Forwarded-Proto")})
		}))
		defer backend.Close()
		backends = append(backends, backend)
	}
	c.TLS.CA = "http://127.0.0.1:1/directory" // Accidental public issuance fails locally.
	generated := caddyConfig(c, directory, gate.Listener.Addr().String(), true, 80)
	servers := generated["apps"].(object)["http"].(object)["servers"].(object)
	ports := map[string]string{}
	for name, value := range servers {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		checked(t, err)
		address := listener.Addr().String()
		listener.Close()
		_, port, err := net.SplitHostPort(address)
		checked(t, err)
		ports[name] = port
		server := value.(object)
		server["listen"] = []string{address}
		index := 0
		if strings.HasSuffix(name, "_prod") {
			index = 1
		}
		routes := server["routes"].([]object)
		routes[len(routes)-1]["handle"].([]object)[0]["upstreams"].([]object)[0]["dial"] = backends[index].Listener.Addr().String()
	}
	httpApp := generated["apps"].(object)["http"].(object)
	httpApp["http_port"], _ = strconv.Atoi(ports["http"])
	httpApp["https_port"], _ = strconv.Atoi(ports["https"])
	config := filepath.Join(directory, "caddy.json")
	start := func() func() {
		raw, err := json.Marshal(generated)
		checked(t, err)
		checked(t, os.WriteFile(config, raw, 0600))
		_, stop := startProcess(t, caddy, []string{"run", "--config", config}, childEnvironment(), directory)
		return stop
	}
	stop := start()
	caPath := filepath.Join(directory, "caddy/pki/authorities/local/root.crt")
	waitUntil(t, func() bool { _, err := os.Stat(caPath); return err == nil }, "Caddy local CA did not start")
	root, err := os.ReadFile(caPath)
	checked(t, err)
	pool := x509.NewCertPool()
	require(t, pool.AppendCertsFromPEM(root), "invalid local CA")
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	waitUntil(t, func() bool {
		r, err := client.Get("https://localhost:" + ports["https"])
		if err != nil {
			return false
		}
		r.Body.Close()
		return r.StatusCode == 200
	}, "Caddy HTTPS did not start")
	for _, suffix := range []string{"", "_prod"} {
		name := "dev"
		if suffix != "" {
			name = "prod"
		}
		for _, scheme := range []string{"http", "https"} {
			host := "localhost:" + ports[scheme+suffix]
			r, err := http.NewRequest("GET", scheme+"://"+host, nil)
			checked(t, err)
			r.Header.Set("X-Forwarded-Proto", "forged")
			response, err := client.Do(r)
			checked(t, err)
			var data map[string]string
			checked(t, json.NewDecoder(response.Body).Decode(&data))
			response.Body.Close()
			require(t, data["system"] == name && data["host"] == host && data["scheme"] == scheme, data)
			require(t, strings.Contains(response.Header.Get("Set-Cookie"), "Secure"), "cookie was changed")
		}
	}
	nonce := strings.Repeat("a", 64)
	response, err := client.Get("http://localhost:" + ports["http"] + challenge + nonce)
	checked(t, err)
	proof, err := io.ReadAll(response.Body)
	checked(t, err)
	response.Body.Close()
	require(t, bytes.Equal(proof, a.proof(nonce)), "public proof route failed")
	response, err = client.Get("http://localhost:" + ports["http"] + "/allow?domain=evil.example.com")
	checked(t, err)
	var backend map[string]string
	checked(t, json.NewDecoder(response.Body).Decode(&backend))
	response.Body.Close()
	require(t, backend["system"] == "dev", "private permission route became public")
	peer, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", "127.0.0.1:"+ports["https"], &tls.Config{RootCAs: pool, ServerName: "localhost"})
	checked(t, err)
	checked(t, peer.SetDeadline(time.Now().Add(3*time.Second)))
	fmt.Fprintf(peer, "GET /connect HTTP/1.1\r\nHost: localhost:%s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", ports["https"])
	reader := bufio.NewReader(peer)
	status, err := reader.ReadString('\n')
	checked(t, err)
	require(t, strings.Contains(status, "101 Switching Protocols"), status)
	for {
		line, err := reader.ReadString('\n')
		checked(t, err)
		if line == "\r\n" {
			break
		}
	}
	_, err = peer.Write([]byte("hello"))
	checked(t, err)
	payload := make([]byte, 5)
	_, err = io.ReadFull(reader, payload)
	checked(t, err)
	peer.Close()
	require(t, string(payload) == "hello", "WebSocket transport failed")
	certDirectory := filepath.Join(directory, "caddy/certificates/local")
	cached := certificates(t, certDirectory)
	require(t, len(cached) > 0, "local certificates were not cached")
	stop()
	var certificate, privateKey []byte
	var certificatePath string
	for path, raw := range cached {
		if filepath.Base(path) == "localhost.crt" {
			certificate, certificatePath = raw, path
		}
		if filepath.Base(path) == "localhost.key" {
			privateKey = raw
		}
	}
	require(t, len(certificate) > 0 && len(privateKey) > 0, "localhost certificate is missing")
	checked(t, os.WriteFile(filepath.Join(directory, "provided/localhost.pem"), append(append([]byte{}, certificate...), privateKey...), 0600))
	stop = start()
	transport.CloseIdleConnections()
	waitUntil(t, func() bool {
		r, err := client.Get("https://localhost:" + ports["https"])
		if err != nil {
			return false
		}
		r.Body.Close()
		return r.StatusCode == 200
	}, "supplied certificate restart failed")
	for path, raw := range cached {
		current, err := os.ReadFile(path)
		checked(t, err)
		require(t, bytes.Equal(raw, current), "restart changed cached certificate")
	}
	if pebble, err := exec.LookPath("pebble"); err == nil {
		t.Run("ACME", func(t *testing.T) {
			testACME(t, pebble, directory, path, caPath, certificatePath, client, a, c, generated, servers, ports, stop, start)
		})
	} else {
		t.Log("local ACME check skipped: put Pebble on PATH")
	}
}

func testACME(t *testing.T, pebble, directory, settingsPath, caPath, certificatePath string, client *http.Client, a *admission, c configuration, generated, servers object, ports map[string]string, stop func(), start func() func()) {
	caPorts := []string{}
	for i := 0; i < 2; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		checked(t, err)
		caPorts = append(caPorts, listener.Addr().String())
		listener.Close()
	}
	configPath := filepath.Join(directory, "pebble.json")
	raw, err := json.Marshal(object{"pebble": object{
		"listenAddress": caPorts[0], "managementListenAddress": caPorts[1],
		"certificate": certificatePath, "privateKey": strings.TrimSuffix(certificatePath, ".crt") + ".key",
		"httpPort": generated["apps"].(object)["http"].(object)["http_port"], "tlsPort": generated["apps"].(object)["http"].(object)["https_port"],
		"keyAlgorithm": "ecdsa", "retryAfter": object{"authz": 1, "order": 1},
		"profiles": object{"default": object{"validityPeriod": 7776000}, "shortlived": object{"validityPeriod": 576000}},
	}})
	checked(t, err)
	checked(t, os.WriteFile(configPath, raw, 0600))
	_, stopCA := startProcess(t, pebble, []string{"-config", configPath}, append(childEnvironment(), "GOMAXPROCS=2", "PEBBLE_VA_ALWAYS_VALID=1", "PEBBLE_VA_NOSLEEP=1", "PEBBLE_WFE_NONCEREJECT=0", "PEBBLE_ALTERNATE_ROOTS=0"), directory)
	defer stopCA()
	_, port, err := net.SplitHostPort(caPorts[1])
	checked(t, err)
	var root []byte
	waitUntil(t, func() bool {
		r, err := client.Get("https://localhost:" + port + "/roots/0")
		if err != nil {
			return false
		}
		defer r.Body.Close()
		root, err = io.ReadAll(r.Body)
		return err == nil && r.StatusCode == 200
	}, "Pebble did not start")
	pool := x509.NewCertPool()
	require(t, pool.AppendCertsFromPEM(root), "invalid ACME root")
	c.TLS.Address = "8.8.8.8"
	tlsApp := generated["apps"].(object)["tls"].(object)
	defaults := caddyConfig(c, directory, "127.0.0.1:2018", true, 80)["apps"].(object)["tls"].(object)
	automation := tlsApp["automation"].(object)
	automation["policies"] = defaults["automation"].(object)["policies"]
	issuer := automation["policies"].([]object)[1]["issuers"].([]object)[0]
	_, port, err = net.SplitHostPort(caPorts[0])
	checked(t, err)
	issuer["ca"], issuer["trusted_roots_pem_files"] = "https://localhost:"+port+"/dir", []string{caPath}
	for name, value := range servers {
		if strings.HasPrefix(name, "https") {
			value.(object)["listen"] = []string{"127.0.0.2:" + ports[name]}
			value.(object)["tls_connection_policies"].([]object)[0]["default_sni"] = "8.8.8.8"
		}
	}
	stop()
	a.check = func(ctx context.Context, name, nonce string, expected []byte) bool {
		return checkAddresses(ctx, name, nonce, expected, []netip.Addr{netip.MustParseAddr("127.0.0.1")}, ports["http"])
	}
	defer start()()
	deadline := time.Now().Add(10 * time.Second)
	dial := func(name string) (*tls.Conn, error) {
		return tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", "127.0.0.2:"+ports["https"], &tls.Config{RootCAs: pool, ServerName: name})
	}
	for _, name := range []string{"first.example.com", "8.8.8.8"} {
		var peer *tls.Conn
		for {
			peer, err = dial(name)
			if err == nil {
				break
			}
			require(t, time.Now().Before(deadline), err)
			time.Sleep(20 * time.Millisecond)
		}
		certificate := peer.ConnectionState().PeerCertificates[0]
		checked(t, certificate.VerifyHostname(name))
		lifetime := certificate.NotAfter.Sub(certificate.NotBefore)
		// Pebble's configured validity includes both certificate endpoints.
		require(t, lifetime == 160*time.Hour-time.Second, lifetime)
		peer.Close()
	}
	c.Limits.NewNames, c.Limits.MaxNames = 0, 0
	writeConfig(t, settingsPath, c)
	peer, err := dial("first.example.com")
	checked(t, err)
	peer.Close()
	_, err = dial("denied.example.com")
	require(t, err != nil, "paused enrollment issued a certificate")
	a.mu.Lock()
	count := len(a.state.Names)
	a.mu.Unlock()
	require(t, count == 2, "ACME enrolled an unexpected number of names")
	t.Log("local ACME domain/no-SNI IP certificates, short-lived profile and admission proof passed")
}

func TestLifecycleAndPackaging(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "https-entrypoint")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin/go"), "build", "-p", "1", "-mod=readonly", "-trimpath", "-ldflags=-s -w", "-o", binary, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	raw, err := build.CombinedOutput()
	require(t, err == nil, string(raw))
	info, err := os.Stat(binary)
	checked(t, err)
	t.Logf("standalone helper: %.2f MiB", float64(info.Size())/(1<<20))
	for _, file := range []string{"../Dockerfile", "../Dockerfile.dev-prod"} {
		raw, err := os.ReadFile(file)
		checked(t, err)
		stages := strings.Split(string(raw), "FROM debian:trixie-slim")
		require(t, len(stages) == 3 && !strings.Contains(stages[2], "python"), "runtime Docker stage installs Python")
		require(t, strings.Contains(stages[1], "bootstrap.sh build") && strings.Contains(stages[2], "https/bootstrap.sh"), "shared HTTPS bootstrap is missing")
	}
	raw, err = os.ReadFile("bootstrap.sh")
	checked(t, err)
	require(t, !bytes.Contains(raw, []byte("apt-get")) && !bytes.Contains(raw, []byte("python")), "HTTPS bootstrap adds a language runtime")
	command := exec.Command(binary, "single", "sh", "-c", "exit 23")
	err = command.Run()
	require(t, err != nil && command.ProcessState.ExitCode() == 23, "custom command contract changed")
	t.Run("Lifecycle", func(t *testing.T) {
		requireSockets(t)
		tools := filepath.Join(directory, "bin")
		checked(t, os.Mkdir(tools, 0700))
		stub := `#!/bin/sh
set -eu
name=${0##*/}
if [ "$name" = caddy ]; then
  test -z "${THE8020_PASSWORD+x}"
  if [ "${FAIL_CADDY:-}" = 1 ]; then exit 7; fi
else test "$THE8020_PASSWORD" = test-only; fi
touch "$TEST_ROOT/$name.started"
trap 'touch "$TEST_ROOT/$name.stopped"; exit 0' TERM INT
while :; do sleep .1; done
`
		for _, name := range []string{"caddy", "docker-entrypoint.sh", "dev-prod.sh"} {
			checked(t, os.WriteFile(filepath.Join(tools, name), []byte(stub), 0700))
		}
		c, err := settings("https.toml")
		checked(t, err)
		path := filepath.Join(directory, "https.toml")
		writeConfig(t, path, c)
		env := append(childEnvironment(), "PATH="+tools+":"+os.Getenv("PATH"), "TEST_ROOT="+directory, "THE8020_PASSWORD=test-only",
			"THE8020_HTTPS_DIR="+directory+"/state", "THE8020_HTTPS_CONFIG="+path, "XDG_RUNTIME_DIR="+directory, "THE8020_NETWORK_MAIN_PORT=8123")
		for _, mode := range []string{"single", "dev-prod"} {
			entrypoint := "docker-entrypoint.sh"
			if mode == "dev-prod" {
				entrypoint = "dev-prod.sh"
			}
			_, stop := startProcess(t, binary, []string{mode, "serve"}, env, directory)
			waitUntil(t, func() bool { _, err := os.Stat(filepath.Join(directory, entrypoint+".started")); return err == nil }, "entrypoint did not start")
			raw, err := os.ReadFile(filepath.Join(directory, "the8020-https/caddy.json"))
			checked(t, err)
			upstream := "127.0.0.1:8123"
			if mode == "dev-prod" {
				upstream = "127.0.0.1:80"
			}
			require(t, bytes.Contains(raw, []byte(upstream)), "kernel port contract changed")
			stop()
			for _, name := range []string{"caddy", entrypoint} {
				_, err := os.Stat(filepath.Join(directory, name+".stopped"))
				checked(t, err)
				checked(t, os.Remove(filepath.Join(directory, name+".started")))
				checked(t, os.Remove(filepath.Join(directory, name+".stopped")))
			}
		}
		for _, scenario := range []struct {
			arguments []string
			env       []string
			code      int
		}{
			{[]string{"single", "serve"}, append(env, "FAIL_CADDY=1"), 7},
			{[]string{"single", "sh", "-c", "exit 23"}, env, 23},
		} {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			cmd := exec.CommandContext(ctx, binary, scenario.arguments...)
			cmd.Env = scenario.env
			raw, err := cmd.CombinedOutput()
			cancel()
			require(t, err != nil && cmd.ProcessState.ExitCode() == scenario.code, string(raw))
		}
	})
}
