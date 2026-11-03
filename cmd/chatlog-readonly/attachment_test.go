package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAttachmentsRejectUnsafeRequests(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unsafe request forwarded"); return nil, nil })}
	for _, kind := range []string{"file", "voice", "video"} {
		for _, suffix := range []string{"", "../secret", testImageKey + "/file", testImageKey + "," + testImageKey, testImageKey + "?info=1", testImageKey + "?path=x", "%30" + testImageKey[1:]} {
			w := httptest.NewRecorder()
			readOnlyHandler(client).ServeHTTP(w, httptest.NewRequest("GET", "/"+kind+"/"+suffix, nil))
			if w.Code < 400 {
				t.Fatal("unsafe path accepted")
			}
		}
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "HEAD"} {
			w := httptest.NewRecorder()
			readOnlyHandler(client).ServeHTTP(w, httptest.NewRequest(method, "/"+kind+"/"+testImageKey, nil))
			if w.Code != 405 {
				t.Fatal("non-GET accepted")
			}
		}
	}
}

func TestAttachmentResponses(t *testing.T) {
	for _, tc := range []struct {
		kind, body, contentType string
		status                  int
	}{
		{"voice", "\x02#!SILK_V3audio", "audio/silk", 200},
		{"voice", "#!AMR\naudio", "audio/amr", 200},
		{"voice", "<html>error</html>", "", 502},
		{"file", "<html>attachment</html>", "application/octet-stream", 200},
		{"video", "\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom", "video/mp4", 200},
		{"video", "<html>error</html>", "", 502},
	} {
		t.Run(tc.kind+tc.contentType, func(t *testing.T) {
			calls := 0
			client := backendClient()
			client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				body := tc.body
				if calls == 1 {
					body = `{"path":"Message/MessageTemp/a/attachment"}`
					if r.URL.Path != "/"+tc.kind+"/"+testImageKey || r.URL.RawQuery != "info=1" {
						t.Fatal("wrong lookup")
					}
				} else if r.URL.Path != "/data/Message/MessageTemp/a/attachment" {
					t.Fatal("wrong path")
				}
				if r.Method != "GET" || r.URL.Host != "127.0.0.1:5030" || r.Header.Get("Authorization") != "" {
					t.Fatal("unsafe backend request")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/"+tc.kind+"/"+testImageKey, nil)
			r.Header.Set("Authorization", "not-forwarded")
			readOnlyHandler(client).ServeHTTP(w, r)
			if w.Code != tc.status || calls != 2 {
				t.Fatalf("status %d calls %d", w.Code, calls)
			}
			if tc.status == 200 && (w.Body.String() != tc.body || w.Header().Get("Content-Type") != tc.contentType) {
				t.Fatal("unexpected response")
			}
			if tc.kind == "file" && (!strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") || w.Header().Get("Content-Security-Policy") == "") {
				t.Fatal("attachment protection missing")
			}
		})
	}
}

func TestMediaPathRestrictions(t *testing.T) {
	for _, kind := range []string{"file", "voice", "video"} {
		for _, p := range []string{"/etc/passwd", "Message/MessageTemp/../secret", "Message/MessageTemp/a/../../secret", "Message/MessageTemp/a\\secret", "Message/MessageTemp/a\x00secret", "other/secret", "Message/MessageTemp//a"} {
			if validMediaPath(p, kind) {
				t.Fatalf("accepted unsafe path for %s", kind)
			}
		}
		if !validMediaPath("Message/MessageTemp/a/attachment", kind) {
			t.Fatal("valid path rejected")
		}
	}
}
