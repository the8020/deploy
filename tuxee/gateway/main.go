package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

const node = "http://127.0.0.1:18080"
const port = "18791"
const mainHost = "app.preview.localhost:" + port
const origin = "http://" + mainHost

var service = node + "/the8020/dev-core/desktop-access/"
var ownerID = regexp.MustCompile("^sbx-[a-z0-9]{10}$")
var langs = regexp.MustCompile("^[a-zA-Z0-9-]{1,30}$")
var mu sync.Mutex
var views = map[string]*view{}
var client = &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}

type owner struct{ User, Sandbox string }
type view struct {
	id, host, code, browser, platform, upstreamCookie, csrf string
	who                                                     owner
	endpoint                                                *url.URL
	transport                                               *http.Transport
	lease                                                   *websocket.Conn
	ctx                                                     context.Context
	cancel                                                  context.CancelFunc
	attached                                                bool
	created                                                 time.Time
}

func random() string {
	var v [24]byte
	if _, err := rand.Read(v[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(v[:])
}
func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}
func verify(ctx context.Context, cookie string) (owner, int) {
	var result owner
	if cookie == "" {
		return result, 401
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", service, nil)
	req.Header.Set("Cookie", cookie)
	resp, err := client.Do(req)
	if err != nil {
		return result, 503
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return result, resp.StatusCode
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 2048)).Decode(&result) != nil || !ownerID.MatchString(result.Sandbox) || result.User == "" {
		return result, 502
	}
	return result, 200
}

// The ordinary console owns the idle reservation. Only this fixed helper runs on it.
func open(ctx context.Context, who owner, cookie, language string) (v *view, err error) {
	cfg, _ := websocket.NewConfig("ws://127.0.0.1:18080/_the8020/console", node)
	cfg.Protocol = []string{"the8020.console.v1"}
	cfg.Header.Set("Cookie", cookie)
	lease, err := cfg.DialContext(ctx)
	if err != nil {
		return nil, errors.New("sandbox console unavailable")
	}
	defer func() {
		if err != nil {
			lease.Close()
		}
	}()
	lease.SetDeadline(time.Now().Add(25 * time.Second))
	err = websocket.JSON.Send(lease, map[string]any{"type": "open", "target": map[string]string{"kind": "development", "sandboxId": who.Sandbox}, "arguments": []string{"/bin/bash", "-c", "printf '\nDESKTOP-BEGIN\n'; /opt/development/desktop-prepare && printf '\nDESKTOP-END\n' && exec /usr/bin/sleep infinity"}, "columns": 120, "rows": 20, "workingDirectory": "/workspace"})
	if err != nil {
		return nil, errors.New("sandbox console open failed")
	}
	var data bytes.Buffer
	for !strings.Contains(data.String(), "DESKTOP-END") {
		var b []byte
		if err := websocket.Message.Receive(lease, &b); err != nil || len(b)+data.Len() > 8192 {
			return nil, errors.New("sandbox desktop startup failed")
		}
		data.Write(b)
	}
	lease.SetDeadline(time.Time{})
	raw := strings.ReplaceAll(data.String(), "\r", "")
	_, raw, found := strings.Cut(raw, "DESKTOP-BEGIN\n")
	if !found {
		return nil, errors.New("desktop descriptor missing")
	}
	raw, _, _ = strings.Cut(raw, "DESKTOP-END")
	parts := strings.SplitN(strings.TrimSpace(raw), "\n", 3)
	if len(parts) != 3 {
		return nil, errors.New("invalid desktop descriptor")
	}
	number, e := strconv.Atoi(parts[0])
	roots := x509.NewCertPool()
	if e != nil || number < 1024 || number > 65535 || !roots.AppendCertsFromPEM([]byte(parts[2])) {
		return nil, fmt.Errorf("invalid private desktop endpoint (port valid %v, certificate bytes %d, leading PEM %v)", e == nil, len(parts[2]), strings.HasPrefix(parts[2], "-----BEGIN CERTIFICATE-----"))
	}
	target, _ := url.Parse("https://127.0.0.1:" + strconv.Itoa(number))
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: who.Sandbox + ".localhost", MinVersion: tls.VersionTLS12}, MaxIdleConnsPerHost: 8, ResponseHeaderTimeout: 20 * time.Second}
	id := random()
	host := who.Sandbox + "-" + id[:12] + ".preview.localhost:" + port
	body, _ := json.Marshal(map[string]string{"username": "root", "password": parts[1], "language": language})
	login, _ := http.NewRequestWithContext(ctx, "POST", target.String()+"/api/login", bytes.NewReader(body))
	login.Host = host
	login.Header.Set("X-Tuxee-Client", "web")
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", "http://"+host)
	resp, err := transport.RoundTrip(login)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, fmt.Errorf("private desktop TLS connection: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		transport.CloseIdleConnections()
		return nil, fmt.Errorf("desktop login returned %d", resp.StatusCode)
	}
	var result struct{ CSRF string }
	json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result)
	var upstream string
	for _, c := range resp.Cookies() {
		if strings.HasPrefix(c.Name, "tuxee_session_") {
			upstream = c.Name + "=" + c.Value
		}
	}
	if upstream == "" || result.CSRF == "" {
		return nil, errors.New("desktop login did not establish a session")
	}
	life, cancel := context.WithCancel(context.Background())
	return &view{id: id, host: host, code: random(), browser: random(), platform: cookie, upstreamCookie: upstream, csrf: result.CSRF, who: who, endpoint: target, transport: transport, lease: lease, created: time.Now(), ctx: life, cancel: cancel}, nil
}

func retire(v *view) {
	mu.Lock()
	if views[v.id] != v {
		mu.Unlock()
		return
	}
	delete(views, v.id)
	mu.Unlock()
	v.cancel()
	v.lease.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", v.endpoint.String()+"/api/logout", nil)
	req.Host = v.host
	req.Header.Set("Origin", "http://"+v.host)
	req.Header.Set("Cookie", v.upstreamCookie)
	req.Header.Set("X-Tuxee-CSRF", v.csrf)
	if r, e := v.transport.RoundTrip(req); e == nil {
		r.Body.Close()
	}
	v.transport.CloseIdleConnections()
}
func app(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || r.Header.Get("Origin") != origin {
		http.Error(w, "Access denied", 403)
		return
	}
	cookie := r.Header.Get("Cookie")
	switch r.URL.Path {
	case "/desktop/open":
		who, status := verify(r.Context(), cookie)
		if status != 200 {
			http.Error(w, "Desktop access denied", status)
			return
		}
		var input struct{ Language string }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 512)).Decode(&input) != nil {
			http.Error(w, "Invalid request", 400)
			return
		}
		if !langs.MatchString(input.Language) {
			input.Language = "en"
		}
		mu.Lock()
		full := len(views) >= 24
		mu.Unlock()
		if full {
			http.Error(w, "Desktop busy", 429)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		v, e := open(ctx, who, cookie, input.Language)
		if e != nil {
			log.Printf("Desktop open: %v", e)
			http.Error(w, "Desktop unavailable", 503)
			return
		}
		code := v.code
		mu.Lock()
		views[v.id] = v
		mu.Unlock()
		time.AfterFunc(45*time.Second, func() {
			mu.Lock()
			expired := !v.attached
			mu.Unlock()
			if expired {
				retire(v)
			}
		})
		jsonOut(w, map[string]string{"url": "http://" + v.host + "/_open", "code": code, "view": v.id})
	case "/desktop/attached":
		raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 96))
		if e != nil {
			http.Error(w, "Invalid view", 400)
			return
		}
		mu.Lock()
		v := views[string(raw)]
		ok := v != nil && v.platform == cookie && !v.attached
		if ok {
			v.attached = true
		}
		mu.Unlock()
		if !ok {
			http.Error(w, "Unknown view", 403)
			return
		}
		defer retire(v)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(200)
		http.NewResponseController(w).Flush()
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-v.ctx.Done():
				return
			// One session check covers all HTTP requests and upgraded streams for this viewer.
			case <-tick.C:
				who, status := verify(r.Context(), cookie)
				if status != 200 || who != v.who {
					return
				}
				if _, e = w.Write([]byte("\n")); e != nil {
					return
				}
				http.NewResponseController(w).Flush()
			}
		}
	default:
		http.NotFound(w, r)
	}
}
func desktop(w http.ResponseWriter, r *http.Request) {
	var code string
	if r.URL.Path == "/_open" && r.Method == http.MethodPost {
		if r.Header.Get("Origin") != "http://"+r.Host {
			http.Error(w, "Access denied", http.StatusForbidden)
			return
		}
		controller := http.NewResponseController(w)
		if err := controller.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			http.Error(w, "Launch unavailable", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		code = r.PostFormValue("code")
		controller.SetReadDeadline(time.Time{})
	}
	mu.Lock()
	var v *view
	for _, item := range views {
		if item.host == r.Host {
			v = item
			break
		}
	}
	if v == nil || !v.attached {
		mu.Unlock()
		http.Error(w, "Desktop closed", 403)
		return
	}
	if r.URL.Path == "/_open" {
		if r.Method == "GET" {
			mu.Unlock()
			nonce := random()
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Referrer-Policy", "strict-origin")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; frame-ancestors "+origin+"; form-action 'self'")
			fmt.Fprintf(w, `<!doctype html><title>Tuxee</title><form method="post" action="/_open"><input type="hidden" name="code"></form><script nonce="%s">const form=document.forms[0];form.elements.code.value=location.hash.slice(1);history.replaceState(null,"","/_open");form.submit()</script>`, nonce)
			return
		}
		if r.Method != http.MethodPost {
			mu.Unlock()
			http.Error(w, "Access denied", 403)
			return
		}
		if code == "" || code != v.code || time.Since(v.created) > 45*time.Second {
			mu.Unlock()
			http.Error(w, "Launch expired", 403)
			return
		}
		v.code = ""
		mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "desktop_session", Value: v.browser, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		http.Redirect(w, r, "/", 303)
		return
	}
	mu.Unlock()
	c, e := r.Cookie("desktop_session")
	if e != nil || c.Value != v.browser {
		http.Error(w, "Access denied", 403)
		return
	}
	supplied := r.Header.Get("Origin")
	if (supplied != "" && supplied != "http://"+v.host) || ((r.Method != "GET" && r.Method != "HEAD" || r.Header.Get("Upgrade") != "") && supplied == "") {
		http.Error(w, "Access denied", 403)
		return
	}
	if r.URL.Path == "/api/login" {
		http.Error(w, "Use Development to sign in", 403)
		return
	}
	h := &httputil.ReverseProxy{Transport: v.transport, Rewrite: func(p *httputil.ProxyRequest) {
		p.SetURL(v.endpoint)
		p.Out.Host = v.host
		for k := range p.Out.Header {
			switch strings.ToLower(k) {
			case "origin", "content-type", "content-length", "accept", "accept-language", "accept-encoding", "range", "if-none-match", "if-modified-since", "upgrade", "connection", "sec-websocket-key", "sec-websocket-version", "sec-websocket-protocol", "sec-websocket-extensions", "x-tuxee-client", "x-tuxee-csrf", "x-tuxee-size":
			default:
				p.Out.Header.Del(k)
			}
		}
		p.Out.Header.Set("Cookie", v.upstreamCookie)
		p.Out.Header.Set("X-Forwarded-Proto", "http")
	}, ModifyResponse: func(resp *http.Response) error {
		for k := range resp.Header {
			l := strings.ToLower(k)
			if l == "set-cookie" || l == "clear-site-data" || strings.HasPrefix(l, "access-control-") {
				resp.Header.Del(k)
			}
		}
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			resp.Header.Del("X-Frame-Options")
			resp.Header.Set("Content-Security-Policy", strings.ReplaceAll(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'", "frame-ancestors "+origin))
		}
		resp.Header.Set("Referrer-Policy", "no-referrer")
		return nil
	}}
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(v.ctx, cancel)
	defer cancel()
	defer stop()
	h.ServeHTTP(w, r.WithContext(ctx))
}
func main() {
	target, _ := url.Parse(node)
	public := httputil.NewSingleHostReverseProxy(target)
	public.ModifyResponse = func(resp *http.Response) error {
		if csp := resp.Header.Get("Content-Security-Policy"); csp != "" && strings.HasPrefix(resp.Request.URL.Path, "/the8020/uui/shell/") {
			resp.Header.Set("Content-Security-Policy", csp+"; frame-src 'self' http://*.preview.localhost:"+port)
		}
		return nil
	}
	listen := os.Getenv("PREVIEW_LISTEN")
	if listen == "" {
		listen = "127.0.0.1"
	}
	server := &http.Server{Addr: listen + ":" + port, ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Host == "localhost:"+port || r.Host == "127.0.0.1:"+port:
			http.Redirect(w, r, origin+"/", 302)
		case r.Host == mainHost:
			if strings.HasPrefix(r.URL.Path, "/desktop/") {
				app(w, r)
				return
			}
			if r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/the8020/") {
				http.NotFound(w, r)
				return
			}
			public.ServeHTTP(w, r)
		case strings.HasSuffix(r.Host, ".preview.localhost:"+port):
			desktop(w, r)
		default:
			http.Error(w, "Unrecognized host", 421)
		}
	})}
	log.Fatal(server.ListenAndServe())
}
