package main

import (
	"crypto/md5"
	"encoding/hex"
	"strconv"
	"strings"
)

// Mac WeChat 3.x uses localID + MD5(decimal serverID)[8:24], not voicemd5.
func darwinVoicePath(key string) (string, bool) {
	parts := strings.Split(key, "_")
	if len(parts) != 4 || parts[0] != "v3" || !validImageKey(parts[1]) {
		return "", false
	}
	local, err := strconv.ParseUint(parts[2], 10, 32)
	if err != nil || local == 0 || strconv.FormatUint(local, 10) != parts[2] {
		return "", false
	}
	server, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil || server == 0 || strconv.FormatUint(server, 10) != parts[3] {
		return "", false
	}
	sum := md5.Sum([]byte(parts[3]))
	short := hex.EncodeToString(sum[:])[8:24]
	return "Message/MessageTemp/" + strings.ToLower(parts[1]) + "/Audio/" + parts[2] + short + ".aud.silk", true
}
