package math

import (
	"strings"
	"testing"
)

func TestShortUUID(t *testing.T) {
	prefix := "test-"
	uuid := ShortUUID(prefix)
	if !strings.HasPrefix(uuid, prefix) {
		t.Errorf("Expected prefix %s, got %s", prefix, uuid)
	}
	if len(uuid) != len(prefix)+12 {
		t.Errorf("Unexpected length: %d", len(uuid))
	}
}

func TestTimeMS(t *testing.T) {
	t1 := TimeMS()
	if t1 <= 0 {
		t.Error("Expected positive time")
	}
}
