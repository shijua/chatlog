package main

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

const maxImageBytes = 32 << 20

func validImageKey(key string) bool {
	if len(key) != 32 {
		return false
	}
	_, err := hex.DecodeString(key)
	return err == nil
}

func validImagePath(p string) bool {
	if strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return false
	}
	return strings.HasPrefix(p, "Message/MessageTemp/") ||
		(strings.HasPrefix(p, "FileStorage/MsgAttach/") && strings.Contains(p, "/Image/")) ||
		strings.HasPrefix(p, "msg/attach/") && strings.Contains(p, "/Img/")
}

// Resolve only a media hash; never expose the backend's general file route.
func serveImage(w http.ResponseWriter, r *http.Request, client *http.Client) {
	fetch := func(target string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		return client.Do(req)
	}
	info, err := fetch(upstream + r.URL.Path + "?info=1")
	if err != nil {
		http.Error(w, "backend unavailable", 502)
		return
	}
	defer info.Body.Close()
	if info.StatusCode != http.StatusOK {
		status := http.StatusBadGateway
		if info.StatusCode == 404 {
			status = http.StatusNotFound
		}
		http.Error(w, "image lookup failed", status)
		return
	}
	var media struct {
		Path string `json:"path"`
	}
	if json.NewDecoder(io.LimitReader(info.Body, 64<<10)).Decode(&media) != nil || !validImagePath(media.Path) {
		http.Error(w, "image unavailable", 502)
		return
	}
	target := &url.URL{Scheme: "http", Host: "127.0.0.1:5030", Path: "/data/" + media.Path}
	resp, err := fetch(target.String())
	if err != nil {
		http.Error(w, "backend unavailable", 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "image unavailable", 502)
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil || len(data) > maxImageBytes {
		http.Error(w, "image unavailable or too large", 502)
		return
	}
	mime := http.DetectContentType(data)
	switch mime {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/bmp":
	default:
		http.Error(w, "backend did not return a supported image", 502)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
