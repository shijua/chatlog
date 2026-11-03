package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
)

const maxImageBytes = 32 << 20
const maxFileBytes = 64 << 20
const maxVideoBytes = 128 << 20

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

func validMediaPath(p, kind string) bool {
	if strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return false
	}
	switch kind {
	case "image":
		return validImagePath(p)
	case "file":
		return strings.HasPrefix(p, "Message/MessageTemp/") || strings.HasPrefix(p, "FileStorage/File/") || strings.HasPrefix(p, "msg/file/")
	case "video":
		return strings.HasPrefix(p, "Message/MessageTemp/") || strings.HasPrefix(p, "FileStorage/Video/") || strings.HasPrefix(p, "msg/video/")
	case "voice":
		return strings.HasPrefix(p, "Message/MessageTemp/")
	}
	return false
}

// Resolve a media hash or a strictly validated message key; never expose arbitrary paths.
func serveIndexedMedia(w http.ResponseWriter, r *http.Request, client *http.Client, kind string) {
	if kind == "image" {
		serveImageWithFallback(w, r, client)
		return
	}
	fetch := func(target string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		return client.Do(req)
	}
	mediaPath, direct := "", false
	switch kind {
	case "voice":
		mediaPath, direct = darwinVoicePath(strings.TrimPrefix(r.URL.Path, "/voice/"))
	case "video":
		mediaPath, direct = darwinVideoPath(strings.TrimPrefix(r.URL.Path, "/video/"))
	}
	if !direct {
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
			http.Error(w, "media lookup failed", status)
			return
		}
		var media struct {
			Path string `json:"path"`
		}
		if json.NewDecoder(io.LimitReader(info.Body, 64<<10)).Decode(&media) != nil || !validMediaPath(media.Path, kind) {
			http.Error(w, "media unavailable", 502)
			return
		}
		mediaPath = media.Path
	}
	target := &url.URL{Scheme: "http", Host: "127.0.0.1:5030", Path: "/data/" + mediaPath}
	resp, err := fetch(target.String())
	if err != nil {
		http.Error(w, "backend unavailable", 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		status := http.StatusBadGateway
		if resp.StatusCode == http.StatusNotFound {
			status = http.StatusNotFound
		}
		http.Error(w, "media unavailable", status)
		return
	}
	maxBytes := maxImageBytes
	if kind == "file" {
		maxBytes = maxFileBytes
	} else if kind == "video" {
		maxBytes = maxVideoBytes
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil || len(data) > maxBytes {
		http.Error(w, "media unavailable or too large", 502)
		return
	}
	contentType := http.DetectContentType(data)
	switch kind {
	case "image":
		switch contentType {
		case "image/jpeg", "image/png", "image/gif", "image/webp", "image/bmp":
		default:
			http.Error(w, "backend did not return a supported image", 502)
			return
		}
	case "video":
		switch contentType {
		case "video/mp4", "video/webm":
		default:
			http.Error(w, "backend did not return supported video", 502)
			return
		}
	case "voice":
		if bytes.HasPrefix(data, []byte("#!SILK_V3")) || bytes.HasPrefix(data, []byte("\x02#!SILK_V3")) {
			contentType = "audio/silk"
		}
		if bytes.HasPrefix(data, []byte("#!AMR\n")) {
			contentType = "audio/amr"
		}
		switch contentType {
		case "audio/silk", "audio/amr", "audio/mpeg", "audio/wave", "audio/x-wav", "audio/ogg", "application/ogg":
		default:
			http.Error(w, "backend did not return supported audio", 502)
			return
		}
	case "file":
		contentType = "application/octet-stream"
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(mediaPath)}))
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
