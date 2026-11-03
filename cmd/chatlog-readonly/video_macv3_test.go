package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDarwinVideoPath(t *testing.T) {
	key := "v3_" + testImageKey + "_42_1700000000"
	want := "Message/MessageTemp/" + testImageKey + "/Video/42_1700000000.mp4"
	if got, ok := darwinVideoPath(key); !ok || got != want {
		t.Fatalf("unexpected path %q", got)
	}
	for _, suffix := range []string{"_0_1", "_01_1", "_-1_1", "_4294967296_1", "_1_0", "_1_01", "_1_1700000000000", "_1_2/secret", "_1_2_extra"} {
		if _, ok := darwinVideoPath("v3_" + testImageKey + suffix); ok {
			t.Fatal("unsafe key accepted")
		}
	}
}

func TestDarwinVideoReadsDerivedPathOnly(t *testing.T) {
	mp4 := "\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"
	for _, tc := range []struct {
		status int
		body   string
		want   int
	}{{200, mp4, 200}, {404, "", 404}, {302, "", 502}, {200, "\x89PNG\r\n\x1a\n", 502}} {
		calls := 0
		client := backendClient()
		client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Method != "GET" || r.URL.Host != "127.0.0.1:5030" || r.URL.Path != "/data/Message/MessageTemp/"+testImageKey+"/Video/42_1700000000.mp4" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" {
				t.Fatal("unexpected upstream request")
			}
			return &http.Response{StatusCode: tc.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/video/v3_"+testImageKey+"_42_1700000000", nil)
		r.Header.Set("Authorization", "not-forwarded")
		readOnlyHandler(client).ServeHTTP(w, r)
		if w.Code != tc.want || calls != 1 {
			t.Fatalf("status %d calls %d", w.Code, calls)
		}
		if tc.want == 200 && w.Header().Get("Content-Type") != "video/mp4" {
			t.Fatal("wrong video type")
		}
	}
}

func TestDarwinVideoRejectsUnsafeRequests(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unsafe request forwarded"); return nil, nil })}
	path := "/video/v3_" + testImageKey + "_42_1700000000"
	for _, tc := range []struct{ method, path string }{{"POST", path}, {"GET", path + "?path=x"}, {"GET", path + "/../secret"}, {"GET", strings.Replace(path, "v3_", "%763_", 1)}} {
		w := httptest.NewRecorder()
		readOnlyHandler(client).ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code < 400 {
			t.Fatal("unsafe request accepted")
		}
	}
}
