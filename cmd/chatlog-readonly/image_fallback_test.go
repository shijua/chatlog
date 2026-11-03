package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestImageFallbackOrder(t *testing.T) {
	png := "\x89PNG\r\n\x1a\n"
	for _, tc := range []struct {
		name     string
		statuses []int
		body     string
		want     int
		variant  string
	}{
		{"hd", []int{200}, png, 200, "hd"},
		{"original", []int{404, 200}, png, 200, "original"},
		{"thumbnail", []int{404, 404, 200}, png, 200, "thumbnail"},
		{"preview", []int{404, 404, 404, 200}, png, 200, "thumbnail"},
		{"missing", []int{404, 404, 404, 404}, "", 404, ""},
		{"failure_is_not_missing", []int{500}, "", 502, ""},
		{"redirect_rejected", []int{302}, "", 502, ""},
		{"invalid_thumbnail", []int{404, 404, 200}, "<html>error</html>", 502, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := []string{".pic_hd.jpg", ".pic.jpg", ".pic_thumb.jpg", ".pic_thumb_preview.jpg"}
			calls := 0
			client := backendClient()
			client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if calls >= len(tc.statuses) {
					t.Fatal("unexpected fallback")
				}
				want := "/data/Message/MessageTemp/" + testImageKey + "/Image/421700000000_" + paths[calls]
				if r.URL.Host != "127.0.0.1:5030" || r.URL.Path != want || r.URL.RawQuery != "" || r.Method != "GET" || r.Header.Get("Authorization") != "" {
					t.Fatal("unexpected upstream request")
				}
				status := tc.statuses[calls]
				calls++
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/image/v3_"+testImageKey+"_42_1700000000", nil)
			r.Header.Set("Authorization", "not-forwarded")
			readOnlyHandler(client).ServeHTTP(w, r)
			if w.Code != tc.want || calls != len(tc.statuses) || w.Header().Get("X-Chatlog-Media-Variant") != tc.variant {
				t.Fatalf("status %d calls %d variant %q", w.Code, calls, w.Header().Get("X-Chatlog-Media-Variant"))
			}
		})
	}
}

func TestMD5ImageIsRejectedBeforeBackend(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("MD5 image request forwarded"); return nil, nil })}
	w := httptest.NewRecorder()
	readOnlyHandler(client).ServeHTTP(w, httptest.NewRequest("GET", "/image/"+testImageKey, nil))
	if w.Code != 404 {
		t.Fatalf("status %d", w.Code)
	}
}

func TestImageMessageKeyRejectsUnsafeRequests(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unsafe request forwarded"); return nil, nil })}
	path := "/image/v3_" + testImageKey + "_42_1700000000"
	for _, tc := range []struct{ method, path string }{{"POST", path}, {"GET", path + "?path=x"}, {"GET", path + "/../secret"}, {"GET", strings.Replace(path, "v3_", "%763_", 1)}, {"GET", strings.Replace(path, "_42_", "_0_", 1)}} {
		w := httptest.NewRecorder()
		readOnlyHandler(client).ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code < 400 {
			t.Fatal("unsafe request accepted")
		}
	}
}
