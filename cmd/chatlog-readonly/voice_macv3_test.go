package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDarwinVoicePath(t *testing.T) {
	key := "v3_" + testImageKey + "_42_1234567890"
	want := "Message/MessageTemp/" + testImageKey + "/Audio/42f82d132f9bb018ca.aud.silk"
	if got, ok := darwinVoicePath(key); !ok || got != want {
		t.Fatalf("unexpected derived path: %q", got)
	}
	for _, suffix := range []string{"_0_1", "_01_1", "_-1_2", "_4294967296_1", "_1_0", "_1_01", "_1_18446744073709551616", "_1_2/secret", "_1_2_extra"} {
		if _, ok := darwinVoicePath("v3_" + testImageKey + suffix); ok {
			t.Fatal("unsafe key accepted")
		}
	}
}

func TestDarwinVoiceReadsDerivedPathOnly(t *testing.T) {
	for _, status := range []int{200, 404, 302} {
		calls := 0
		client := backendClient()
		client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Method != "GET" || r.URL.Host != "127.0.0.1:5030" || r.URL.Path != "/data/Message/MessageTemp/"+testImageKey+"/Audio/42f82d132f9bb018ca.aud.silk" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" {
				t.Fatal("unexpected upstream request")
			}
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("\x02#!SILK_V3audio"))}, nil
		})
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/voice/v3_"+testImageKey+"_42_1234567890", nil)
		r.Header.Set("Authorization", "not-forwarded")
		readOnlyHandler(client).ServeHTTP(w, r)
		want := status
		if status == 302 {
			want = 502
		}
		if w.Code != want || calls != 1 {
			t.Fatalf("status %d calls %d", w.Code, calls)
		}
		if status == 200 && w.Header().Get("Content-Type") != "audio/silk" {
			t.Fatal("wrong audio type")
		}
	}
}

func TestDarwinVoiceRejectsQueriesAndWrites(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unsafe request forwarded"); return nil, nil })}
	path := "/voice/v3_" + testImageKey + "_42_1234567890"
	for _, tc := range []struct{ method, path string }{{"POST", path}, {"GET", path + "?info=1"}, {"GET", path + "/../file"}, {"GET", strings.Replace(path, "v3_", "%763_", 1)}} {
		w := httptest.NewRecorder()
		readOnlyHandler(client).ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code < 400 {
			t.Fatal("unsafe request accepted")
		}
	}
}
