package main

import (
	"net/http"
	"net/http/httptest"
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
