// The deployment entrypoint runs stock Caddy and bounds new certificate names.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const challenge = "/.well-known/the8020-https/"

var localNames = []string{"localhost", "127.0.0.1", "::1"}
var label = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var noncePattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type configuration struct {
	Limits struct {
		NewNames   int     `toml:"new_names_per_hour"`
		MaxNames   int     `toml:"max_names"`
		Concurrent int     `toml:"concurrent_checks"`
		Timeout    float64 `toml:"check_timeout_seconds"`
	} `toml:"limits"`
	TLS struct {
		CA      string `toml:"acme_ca"`
		Address string `toml:"ip_address"`
	} `toml:"tls"`
}

func settings(path string) (configuration, error) {
	var c configuration
	c.Limits.NewNames, c.Limits.MaxNames = -1, -1
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil {
		return c, err
	}
	if len(raw) > 16384 {
		return c, errors.New("HTTPS TOML exceeds 16 KiB")
	}
	if err := toml.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	if c.Limits.NewNames < 0 || c.Limits.MaxNames < 0 || c.Limits.Concurrent < 1 || c.Limits.Concurrent > 16 ||
		math.IsNaN(c.Limits.Timeout) || c.Limits.Timeout <= 0 || c.Limits.Timeout > 30 {
		return c, errors.New("invalid HTTPS limits")
	}
	ca, err := url.Parse(c.TLS.CA)
	if err != nil || (ca.Scheme != "http" && ca.Scheme != "https") || ca.Hostname() == "" || ca.User != nil {
		return c, errors.New("invalid ACME directory URL")
	}
	if c.TLS.Address != "" {
		ip, err := netip.ParseAddr(c.TLS.Address)
		if err != nil || ip.Zone() != "" {
			return c, errors.New("invalid certificate IP address")
		}
		c.TLS.Address = ip.Unmap().String()
	}
	return c, nil
}

func hostname(name string) (string, error) {
	name = strings.TrimRight(strings.ToLower(name), ".")
	if ip, err := netip.ParseAddr(name); err == nil && ip.Zone() == "" {
		return ip.Unmap().String(), nil
	}
	if name == "localhost" {
		return name, nil
	}
	if len(name) > 253 || !strings.Contains(name, ".") {
		return "", errors.New("invalid public hostname")
	}
	for _, part := range strings.Split(name, ".") {
		if !label.MatchString(part) {
			return "", errors.New("invalid public hostname")
		}
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".home.arpa"} {
		if strings.HasSuffix(name, suffix) {
			return "", errors.New("local hostname")
		}
	}
	return name, nil
}

// IsGlobalUnicast also includes private and reserved destinations. Exclude them
// before dialing so an untrusted name cannot probe the container/VPS network.
var reserved = func() []netip.Prefix {
	var result []netip.Prefix
	for _, block := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32",
		"2002::/16", "3fff::/20", "5f00::/16", "fec0::/10",
	} {
		result = append(result, netip.MustParsePrefix(block))
	}
	return result
}()

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	for _, block := range reserved {
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

func publicAddresses(ctx context.Context, name string) ([]netip.Addr, error) {
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", name)
	if err != nil {
		return nil, err
	}
	if len(addresses) == 0 {
		return nil, errors.New("hostname has no addresses")
	}
	for _, ip := range addresses {
		if !publicIP(ip) {
			return nil, errors.New("hostname must resolve exclusively to public addresses")
		}
	}
	if len(addresses) > 16 {
		addresses = addresses[:16]
	}
	return addresses, nil
}

func checkHost(ctx context.Context, name, nonce string, expected []byte) bool {
	addresses, err := publicAddresses(ctx, name)
	if err != nil {
		return false
	}
	return checkAddresses(ctx, name, nonce, expected, addresses, "80")
}

func checkAddresses(ctx context.Context, name, nonce string, expected []byte, addresses []netip.Addr, port string) bool {
	for _, ip := range addresses {
		address := net.JoinHostPort(ip.String(), port)
		transport := &http.Transport{DisableKeepAlives: true, MaxResponseHeaderBytes: 16384}
		defer transport.CloseIdleConnections()
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", address)
		}
		client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		host := name
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		request, err := http.NewRequestWithContext(ctx, "GET", "http://"+host+challenge+nonce, nil)
		if err != nil {
			return false
		}
		response, err := client.Do(request)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 65))
		response.Body.Close()
		if err == nil && response.StatusCode == 200 && hmac.Equal(body, expected) {
			return true
		}
	}
	return false
}

type admissionState struct {
	Names    map[string]bool `json:"names"`
	Attempts []int64         `json:"attempts"`
}

type admission struct {
	config, path string
	local        map[string]bool
	key          [32]byte
	mu           sync.Mutex
	active       int
	state        admissionState
	check        func(context.Context, string, string, []byte) bool
}

func newAdmission(config, directory string) (*admission, error) {
	c, err := settings(config)
	if err != nil {
		return nil, err
	}
	a := &admission{config: config, path: filepath.Join(directory, "admissions.json"),
		local: map[string]bool{}, state: admissionState{Names: map[string]bool{}}, check: checkHost}
	for _, name := range localNames {
		a.local[name] = true
	}
	if c.TLS.Address != "" && !publicIP(netip.MustParseAddr(c.TLS.Address)) {
		a.local[c.TLS.Address] = true
	}
	if _, err := rand.Read(a.key[:]); err != nil {
		return nil, err
	}
	f, err := os.Open(a.path)
	if err == nil {
		defer f.Close()
		raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		if err != nil {
			return nil, err
		}
		if len(raw) > 1<<20 {
			return nil, errors.New("HTTPS admission state exceeds 1 MiB")
		}
		if err := json.Unmarshal(raw, &a.state); err != nil {
			return nil, err
		}
		if a.state.Names == nil {
			return nil, errors.New("invalid HTTPS admission state")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return a, nil
}

func (a *admission) proof(nonce string) []byte {
	mac := hmac.New(sha256.New, a.key[:])
	mac.Write([]byte(nonce))
	return []byte(hex.EncodeToString(mac.Sum(nil)))
}

func (a *admission) save() error {
	raw, err := json.Marshal(a.state)
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return errors.New("HTTPS admission state exceeds 1 MiB")
	}
	f, err := os.CreateTemp(filepath.Dir(a.path), ".admissions-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	if closed := f.Close(); err == nil {
		err = closed
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), a.path)
}

func (a *admission) allow(ctx context.Context, name string) bool {
	name, err := hostname(name)
	if err != nil {
		return false
	}
	a.mu.Lock()
	known := a.local[name] || a.state.Names[name]
	a.mu.Unlock()
	if known {
		return true
	}
	c, err := settings(a.config)
	if err != nil {
		return false
	}
	a.mu.Lock()
	// ponytail: one small state file/lock; SQLite only if enrollment volume warrants it.
	now := time.Now().Unix()
	kept := a.state.Attempts[:0]
	for _, at := range a.state.Attempts {
		if at >= now-3600 {
			kept = append(kept, at)
		}
	}
	a.state.Attempts = kept
	if a.active >= c.Limits.Concurrent || len(a.state.Names) >= c.Limits.MaxNames || len(kept) >= c.Limits.NewNames {
		a.mu.Unlock()
		return false
	}
	a.state.Attempts = append(a.state.Attempts, now)
	if err := a.save(); err != nil {
		a.mu.Unlock()
		return false
	}
	a.active++
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.active--; a.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.Limits.Timeout*float64(time.Second)))
	defer cancel()
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return false
	}
	nonce := hex.EncodeToString(random[:])
	if !a.check(ctx, name, nonce, a.proof(nonce)) {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Names[name] {
		return true
	}
	if len(a.state.Names) >= c.Limits.MaxNames {
		return false
	}
	a.state.Names[name] = true
	if err := a.save(); err != nil {
		delete(a.state.Names, name)
		return false
	}
	log.Printf("https: admitted %s", name)
	return true
}

func (a *admission) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, challenge) {
		nonce := strings.TrimPrefix(r.URL.Path, challenge)
		if noncePattern.MatchString(nonce) && r.URL.RawQuery == "" {
			w.Write(a.proof(nonce))
			return
		}
	} else if r.URL.Path == "/allow" {
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err == nil && len(query) == 1 && len(query["domain"]) == 1 && a.allow(r.Context(), query["domain"][0]) {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		return
	}
	http.NotFound(w, r)
}

type boundedListener struct {
	net.Listener
	slots chan struct{}
}
type boundedConnection struct {
	net.Conn
	once  sync.Once
	slots chan struct{}
}

func (c *boundedConnection) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { <-c.slots })
	return err
}
func (l *boundedListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &boundedConnection{Conn: c, slots: l.slots}, nil
		default:
			c.Close()
		}
	}
}

type object = map[string]any

func caddyConfig(c configuration, directory, gate string, pair bool, mainPort int) object {
	local := append([]string{}, localNames...)
	if c.TLS.Address != "" && !publicIP(netip.MustParseAddr(c.TLS.Address)) {
		local = append(local, c.TLS.Address)
	}
	issuer := object{"module": "acme", "ca": c.TLS.CA}
	if c.TLS.Address != "" && publicIP(netip.MustParseAddr(c.TLS.Address)) {
		// No-SNI clients select the default policy before DefaultSNI is applied.
		issuer["profile"] = "shortlived"
	}
	defaultName := c.TLS.Address
	if defaultName == "" {
		defaultName = "127.0.0.1"
	}
	proxy := func(address string) object {
		return object{"handler": "reverse_proxy", "upstreams": []object{{"dial": address}}}
	}
	servers := object{}
	ports := [][3]int{{9080, 443, mainPort}}
	if pair {
		ports = append(ports, [3]int{9081, 8443, 8080})
	}
	for i, port := range ports {
		suffix := ""
		if i == 1 {
			suffix = "_prod"
		}
		upstream := proxy(fmt.Sprintf("127.0.0.1:%d", port[2]))
		servers["http"+suffix] = object{
			"listen": []string{fmt.Sprintf(":%d", port[0])}, "read_header_timeout": "5s",
			"automatic_https": object{"disable": true},
			"routes":          []object{{"match": []object{{"path": []string{challenge + "*"}}}, "handle": []object{proxy(gate)}}, {"handle": []object{upstream}}},
		}
		servers["https"+suffix] = object{
			"listen": []string{fmt.Sprintf(":%d", port[1])}, "read_header_timeout": "30s", "protocols": []string{"h1", "h2"},
			"automatic_https":         object{"disable_redirects": true},
			"tls_connection_policies": []object{{"default_sni": defaultName}}, "routes": []object{{"handle": []object{upstream}}},
		}
	}
	return object{
		"admin":   object{"disabled": true, "config": object{"persist": false}},
		"storage": object{"module": "file_system", "root": filepath.Join(directory, "caddy")},
		"apps": object{
			"pki": object{"certificate_authorities": object{"local": object{"install_trust": false}}},
			"tls": object{
				"certificates": object{"automate": local, "load_folders": []string{filepath.Join(directory, "provided")}},
				"automation": object{
					"on_demand": object{"permission": object{"module": "http", "endpoint": "http://" + gate + "/allow"}},
					"policies":  []object{{"subjects": local, "issuers": []object{{"module": "internal"}}}, {"on_demand": true, "issuers": []object{issuer}}},
				},
			},
			"http": object{"http_port": 9080, "https_port": 443, "servers": servers},
		},
	}
}

func childEnvironment() []string {
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name != "THE8020_USERNAME" && name != "THE8020_PASSWORD" && name != "THE8020_SIGNING_KEY" {
			env = append(env, entry)
		}
	}
	return env
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func run() (int, error) {
	args := os.Args[1:]
	if len(args) == 0 || (args[0] != "single" && args[0] != "dev-prod") {
		return 1, errors.New("expected single or dev-prod")
	}
	pair := args[0] == "dev-prod"
	if len(args) > 1 && (len(args) != 2 || args[1] != "serve") {
		binary, err := exec.LookPath(args[1])
		if err != nil {
			return 1, err
		}
		return 1, syscall.Exec(binary, args[1:], os.Environ())
	}
	syscall.Umask(0077)
	directory, err := filepath.Abs(environment("THE8020_HTTPS_DIR", "/var/lib/the8020/https"))
	if err != nil {
		return 1, err
	}
	runtime := filepath.Join(environment("XDG_RUNTIME_DIR", "/run"), "the8020-https")
	for _, path := range []string{directory, filepath.Join(directory, "provided"), runtime} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return 1, err
		}
	}
	configPath := environment("THE8020_HTTPS_CONFIG", "/etc/the8020/https.toml")
	c, err := settings(configPath)
	if err != nil {
		return 1, err
	}
	mainPort := 80
	if !pair {
		mainPort, err = strconv.Atoi(environment("THE8020_NETWORK_MAIN_PORT", "80"))
	}
	if err != nil || mainPort < 1 || mainPort > 65535 || mainPort == 443 || mainPort == 9080 {
		return 1, errors.New("invalid kernel main port or proxy port collision")
	}
	a, err := newAdmission(configPath, directory)
	if err != nil {
		return 1, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 1, err
	}
	defer listener.Close()
	generated := filepath.Join(runtime, "caddy.json")
	raw, err := json.Marshal(caddyConfig(c, directory, listener.Addr().String(), pair, mainPort))
	if err != nil {
		return 1, err
	}
	if err := os.WriteFile(generated, raw, 0600); err != nil {
		return 1, err
	}
	server := &http.Server{Handler: a, ReadTimeout: 5 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 16384}
	exits := make(chan error, 3)
	go func() { exits <- server.Serve(&boundedListener{Listener: listener, slots: make(chan struct{}, 32)}) }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(signals)
	var children []*exec.Cmd
	var completed []chan struct{}
	defer func() {
		deadline, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		for _, child := range children {
			child.Process.Signal(syscall.SIGTERM)
		}
		server.Shutdown(deadline)
		for i, child := range children {
			select {
			case <-completed[i]:
			case <-deadline.Done():
				child.Process.Kill()
				<-completed[i]
			}
		}
	}()
	entrypoint := "docker-entrypoint.sh"
	if pair {
		entrypoint = "dev-prod.sh"
	}
	for _, command := range [][]string{{"caddy", "run", "--config", generated}, {entrypoint, "serve"}} {
		child := exec.Command(command[0], command[1:]...)
		child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
		if command[0] == "caddy" {
			child.Env = childEnvironment()
		}
		if err := child.Start(); err != nil {
			return 1, err
		}
		children = append(children, child)
		done := make(chan struct{})
		completed = append(completed, done)
		go func() { exits <- child.Wait(); close(done) }()
	}
	select {
	case <-signals:
		return 0, nil
	case err := <-exits:
		var failure *exec.ExitError
		if errors.As(err, &failure) && failure.ExitCode() > 0 {
			return failure.ExitCode(), err
		}
		return 1, err
	}
}

func main() {
	code, err := run()
	if err != nil {
		log.Print(err)
	}
	os.Exit(code)
}
