package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testImageKey = "0123456789abcdef0123456789abcdef"

func TestImageRejectsUnsafeRequests(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unsafe request forwarded"); return nil, nil })}
	for _, p := range []string{"/image/", "/image/../secret", "/image/" + testImageKey + "/file", "/image/" + testImageKey + "," + testImageKey, "/image/" + testImageKey + "?info=1", "/image/" + testImageKey + "?format=json", "/image/%30" + testImageKey[1:]} {
		w := httptest.NewRecorder()
		readOnlyHandler(client).ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code < 400 {
			t.Fatal("unsafe path accepted")
		}
	}
	w := httptest.NewRecorder()
	readOnlyHandler(client).ServeHTTP(w, httptest.NewRequest("POST", "/image/"+testImageKey, nil))
	if w.Code != 405 {
		t.Fatal("write accepted")
	}
}

func TestImageResolutionAndContentValidation(t *testing.T) {
	for _, tc := range []struct {
		name, metadata, body string
		status               int
		calls                int
	}{
		{"png", `{"path":"Message/MessageTemp/a/pic.dat"}`, "\x89PNG\r\n\x1a\n", 200, 2},
		{"traversal", `{"path":"Message/MessageTemp/../secret"}`, "", 502, 1},
		{"absolute", `{"path":"/etc/passwd"}`, "", 502, 1},
		{"nonimage", `{"path":"Message/MessageTemp/a/file"}`, "private text", 502, 2},
		{"redirect", `{"path":"Message/MessageTemp/a/pic.dat"}`, "", 502, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := backendClient()
			client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" || r.URL.Host != "127.0.0.1:5030" || r.Header.Get("Authorization") != "" {
					t.Fatal("unsafe upstream request")
				}
				body := tc.body
				status := 200
				if calls == 1 {
					body = tc.metadata
					if r.URL.Path != "/image/"+testImageKey || r.URL.RawQuery != "info=1" {
						t.Fatal("wrong lookup")
					}
				} else if r.URL.Path != "/data/Message/MessageTemp/a/pic.dat" && tc.name != "nonimage" {
					t.Fatal("wrong image path")
				}
				if calls == 2 && tc.name == "redirect" {
					status = 302
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/image/"+testImageKey, nil)
			req.Header.Set("Authorization", "secret")
			readOnlyHandler(client).ServeHTTP(w, req)
			if w.Code != tc.status || calls != tc.calls {
				t.Fatalf("status %d calls %d", w.Code, calls)
			}
			if tc.status == 200 && w.Header().Get("Content-Type") != "image/png" {
				t.Fatal("wrong MIME")
			}
		})
	}
}
