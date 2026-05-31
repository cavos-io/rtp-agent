package math

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

var timeNow = time.Now

func ShortUUID(prefix string) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return prefix + "unknown"
	}
	return prefix + hex.EncodeToString(b)
}

func TimeMS() int64 {
	return (timeNow().UnixNano() + int64(time.Millisecond)/2) / int64(time.Millisecond)
}
