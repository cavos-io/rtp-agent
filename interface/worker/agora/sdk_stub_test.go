package agora

import (
	"strings"
	"testing"
)

func TestNewSDKChannelClientReportsBuildTagRequirement(t *testing.T) {
	client, err := NewSDKChannelClient()
	if err == nil {
		t.Fatal("NewSDKChannelClient() error = nil, want build-tag requirement")
	}
	if client != nil {
		t.Fatalf("NewSDKChannelClient() client = %#v, want nil", client)
	}
	if !strings.Contains(err.Error(), "agora_sdk") {
		t.Fatalf("NewSDKChannelClient() error = %v, want agora_sdk build tag mention", err)
	}
}
