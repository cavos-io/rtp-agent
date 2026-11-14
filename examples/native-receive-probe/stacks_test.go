package main

import (
	"strings"
	"testing"
)

// Deliberately narrow: do not hide arbitrary syscall/cgo or locked goroutines.
// These exact stack-only contexts are created by runtime.oneNewExtraM.
func classifyStacks(data string) (foreign, regular []string) {
	for _, block := range strings.Split(strings.TrimSpace(data), "\n\n") {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) < 1 || !strings.HasPrefix(lines[0], "goroutine ") {
			continue
		}
		id := strings.Fields(lines[0])[1]
		if len(lines) == 3 && strings.Contains(lines[0], "[syscall, locked to thread]") && lines[1] == "runtime.goexit({})" {
			foreign = append(foreign, id)
		} else {
			regular = append(regular, id)
		}
	}
	return
}
func TestClassifyForeignCallbackContexts(t *testing.T) {
	f, g := classifyStacks("goroutine 10 [syscall, locked to thread]:\nruntime.goexit({})\n\truntime/asm_arm64.s:1\n\ngoroutine 11 [syscall, locked to thread]:\nmain.blocked()\n\tnative.go:1\n\ngoroutine 12 [chan receive]:\nmain.wait()\n\tnative.go:2\n")
	if strings.Join(f, ",") != "10" || strings.Join(g, ",") != "11,12" {
		t.Fatalf("foreign=%v regular=%v", f, g)
	}
}
