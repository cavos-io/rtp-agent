package math

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

func ShortUUID(prefix string) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return prefix + "unknown"
	}
	return prefix + hex.EncodeToString(b)
}

func TimeMS() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

