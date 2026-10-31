package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRejectedRequestsNeverReachBackend(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("rejected request reached backend")
		return nil, nil
	})}
	h := readOnlyHandler(client)
	cases := []struct{ method, path, body string }{
		{"POST", "/api/v1/chatlog", ""},
		{"PUT", "/api/v1/session", ""},
		{"PATCH", "/api/v1/session", ""},
		{"DELETE", "/api/v1/session", ""},
		{"HEAD", "/api/v1/session", ""},
		{"OPTIONS", "/api/v1/session", ""},
		{"GET", "/mcp", ""},
		{"GET", "/sse", ""},
		{"GET", "/message", ""},
		{"GET", "/data/example", ""},
		{"GET", "/api/v1/contact", ""},
		{"GET", "/api/v1/session/../chatlog", ""},
		{"GET", "/api/v1/%73ession", ""},
		{"GET", "/api/v1/session?method=POST", ""},
		{"GET", "/api/v1/session?limit=1&limit=2", ""},
		{"GET", "/api/v1/session?format=csv", ""},
		{"GET", "/api/v1/session?keyword=%zz", ""},
		{"GET", "/api/v1/session", "payload"},
		{"GET", "http://example.org/api/v1/session", ""},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path+c.body, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(c.method, c.path, strings.NewReader(c.body)))
			if w.Code < 400 {
				t.Fatalf("unexpected status %d", w.Code)
			}
		})
	}
}

func TestAllowedRequestsAreRebuiltAsReads(t *testing.T) {
	for _, path := range []string{"/api/v1/session", "/api/v1/chatlog"} {
		t.Run(path, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" || r.URL.Host != "127.0.0.1:5030" || r.URL.Path != path || r.URL.Query().Get("format") != "json" || r.Body != nil {
					t.Fatal("unexpected backend request")
				}
				if r.Header.Get("X-HTTP-Method-Override") != "" || r.Header.Get("Authorization") != "" {
					t.Fatal("client headers forwarded")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader("[]"))}, nil
			})}
			r := httptest.NewRequest("GET", path+"?limit=1", nil)
			r.Header.Set("X-HTTP-Method-Override", "POST")
			r.Header.Set("Authorization", "test-value")
			w := httptest.NewRecorder()
			readOnlyHandler(client).ServeHTTP(w, r)
			if w.Code != 200 || w.Body.String() != "[]" || calls != 1 {
				t.Fatal("read failed")
			}
		})
	}
}

func TestListenAddrRejectsOtherInterfaces(t *testing.T) {
	for _, addr := range []string{":5030", "0.0.0.0:5030", "[::]:5030", "192.168.1.2:5030", "example.org:5030"} {
		if validListenAddr(addr) {
			t.Errorf("accepted %s", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:5031", "100.64.0.1:5030", "100.127.255.254:5030"} {
		if !validListenAddr(addr) {
			t.Errorf("rejected %s", addr)
		}
	}
}

func TestBackendRedirectIsNotFollowed(t *testing.T) {
	calls := 0
	client := backendClient()
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls > 1 {
			t.Fatal("backend redirect followed")
		}
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"/forbidden"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	w := httptest.NewRecorder()
	readOnlyHandler(client).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/session", nil))
	if w.Code != http.StatusBadGateway || calls != 1 {
		t.Fatal("redirect was not rejected")
	}
}

func TestPeerOnlyAllowsActualSource(t *testing.T) {
	for _, c := range []struct {
		remote string
		status int
	}{
		{"100.127.17.115:12345", 204},
		{"100.127.17.116:12345", 403},
		{"127.0.0.1:12345", 403},
		{"invalid", 403},
	} {
		t.Run(c.remote, func(t *testing.T) {
			calls := 0
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) })
			h := peerOnlyHandler(next, netip.MustParseAddr("100.127.17.115"))
			r := httptest.NewRequest("GET", "/api/v1/session", nil)
			r.RemoteAddr = c.remote
			r.Header.Set("X-Forwarded-For", "100.127.17.115")
			r.Header.Set("X-Real-IP", "100.127.17.115")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != c.status {
				t.Fatalf("status %d, want %d", w.Code, c.status)
			}
			if c.status == 403 && calls != 0 {
				t.Fatal("unauthorized source reached backend")
			}
		})
	}
}
