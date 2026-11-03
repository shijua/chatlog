package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type imageCandidate struct{ path, variant string }

func darwinImagePath(key string) (string, bool) {
	// Image and video keys share the same strict numeric bounds.
	if _, ok := darwinVideoPath(key); !ok {
		return "", false
	}
	parts := strings.Split(key, "_")
	return "Message/MessageTemp/" + strings.ToLower(parts[1]) + "/Image/" + parts[2] + parts[3] + "_.pic.jpg", true
}

func imageCandidates(p string) []imageCandidate {
	if strings.HasPrefix(p, "Message/MessageTemp/") && strings.Contains(p, "/Image/") {
		for _, suffix := range []string{".pic_hd.jpg", ".pic.jpg", ".pic_thumb.jpg", ".pic_thumb_preview.jpg"} {
			if strings.HasSuffix(p, suffix) {
				stem := strings.TrimSuffix(p, suffix)
				return []imageCandidate{{stem + ".pic_hd.jpg", "hd"}, {stem + ".pic.jpg", "original"}, {stem + ".pic_thumb.jpg", "thumbnail"}, {stem + ".pic_thumb_preview.jpg", "thumbnail"}}
			}
		}
	}
	return []imageCandidate{{p, "indexed"}}
}

func serveImageWithFallback(w http.ResponseWriter, r *http.Request, client *http.Client) {
	fetch := func(target string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		return client.Do(req)
	}
	key := strings.TrimPrefix(r.URL.Path, "/image/")
	mediaPath, direct := darwinImagePath(key)
	if !direct {
		if !validImageKey(key) {
			http.NotFound(w, r)
			return
		}
		info, err := fetch(upstream + r.URL.Path + "?info=1")
		if err != nil {
			http.Error(w, "backend unavailable", 502)
			return
		}
		defer info.Body.Close()
		if info.StatusCode != 200 {
			status := 502
			if info.StatusCode == 404 {
				status = 404
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
		mediaPath = media.Path
	}
	for _, candidate := range imageCandidates(mediaPath) {
		if !validImagePath(candidate.path) {
			http.Error(w, "image unavailable", 502)
			return
		}
		target := &url.URL{Scheme: "http", Host: "127.0.0.1:5030", Path: "/data/" + candidate.path}
		resp, err := fetch(target.String())
		if err != nil {
			http.Error(w, "backend unavailable", 502)
			return
		}
		if resp.StatusCode == 404 {
			resp.Body.Close()
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			http.Error(w, "image unavailable", 502)
			return
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
		resp.Body.Close()
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
		w.Header().Set("X-Chatlog-Media-Variant", candidate.variant)
		w.WriteHeader(200)
		_, _ = w.Write(data)
		return
	}
	http.Error(w, "image not found", 404)
}
