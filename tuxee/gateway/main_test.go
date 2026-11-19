package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type checkedBody struct {
	io.ReadCloser
	t *testing.T
}

func (b checkedBody) Read(p []byte) (int, error) {
	if mu.TryLock() {
		mu.Unlock()
	} else {
		b.t.Error("launch body read while the shared view lock is held")
	}
	return b.ReadCloser.Read(p)
}

func TestLaunchConsumesCodeOnceAndReadsOutsideViewLock(t *testing.T) {
	v := &view{id: "test", host: "desktop.test", code: "once", browser: "test-session", attached: true, created: time.Now()}
	views[v.id] = v
	defer delete(views, v.id)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = checkedBody{r.Body, t}
		desktop(w, r)
	}))
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, want := range []int{http.StatusSeeOther, http.StatusForbidden} {
		form := url.Values{"code": {"once"}}
		req, err := http.NewRequest(http.MethodPost, server.URL+"/_open", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = v.host
		req.Header.Set("Origin", "http://"+v.host)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("launch returned %d, want %d", resp.StatusCode, want)
		}
	}
}
