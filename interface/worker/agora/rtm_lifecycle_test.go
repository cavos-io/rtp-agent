package agora

import (
	"errors"
	"strings"
	"testing"
)

func TestCloseRTMClientLogsOutAndReleasesAfterUnsubscribeError(t *testing.T) {
	client := &recordingRTMLifecycleClient{unsubscribeErr: errors.New("unsubscribe failed")}

	err := closeRTMClient(client, "support")
	if err == nil || !strings.Contains(err.Error(), "unsubscribe failed") {
		t.Fatalf("closeRTMClient() error = %v, want unsubscribe failure", err)
	}
	if got := strings.Join(client.calls, ","); got != "unsubscribe:support,logout,release" {
		t.Fatalf("calls = %s, want unsubscribe, logout, release", got)
	}
}

func TestCloseRTMClientReportsLogoutErrorAfterRelease(t *testing.T) {
	client := &recordingRTMLifecycleClient{logoutErr: errors.New("logout failed")}

	err := closeRTMClient(client, "support")
	if err == nil || !strings.Contains(err.Error(), "logout failed") {
		t.Fatalf("closeRTMClient() error = %v, want logout failure", err)
	}
	if got := strings.Join(client.calls, ","); got != "unsubscribe:support,logout,release" {
		t.Fatalf("calls = %s, want unsubscribe, logout, release", got)
	}
}

type recordingRTMLifecycleClient struct {
	calls          []string
	unsubscribeErr error
	logoutErr      error
}

func (r *recordingRTMLifecycleClient) Unsubscribe(channel string) error {
	r.calls = append(r.calls, "unsubscribe:"+channel)
	return r.unsubscribeErr
}

func (r *recordingRTMLifecycleClient) Logout() error {
	r.calls = append(r.calls, "logout")
	return r.logoutErr
}

func (r *recordingRTMLifecycleClient) Release() {
	r.calls = append(r.calls, "release")
}
