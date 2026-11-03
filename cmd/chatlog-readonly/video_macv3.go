package main

import (
	"strconv"
	"strings"
)

// Verified against Mac WeChat 3.x local videos; the suffix is message epoch seconds.
func darwinVideoPath(key string) (string, bool) {
	parts := strings.Split(key, "_")
	if len(parts) != 4 || parts[0] != "v3" || !validImageKey(parts[1]) {
		return "", false
	}
	local, err := strconv.ParseUint(parts[2], 10, 32)
	if err != nil || local == 0 || strconv.FormatUint(local, 10) != parts[2] {
		return "", false
	}
	timestamp, err := strconv.ParseUint(parts[3], 10, 32)
	if err != nil || timestamp == 0 || strconv.FormatUint(timestamp, 10) != parts[3] {
		return "", false
	}
	return "Message/MessageTemp/" + strings.ToLower(parts[1]) + "/Video/" + parts[2] + "_" + parts[3] + ".mp4", true
}
