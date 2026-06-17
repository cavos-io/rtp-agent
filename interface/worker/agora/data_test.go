package agora

import "testing"

func TestResolveDataOptionsUsesRTMIdentityAndToken(t *testing.T) {
	opts, err := ResolveDataOptions(Options{
		AppID:     " app ",
		Channel:   " support ",
		UID:       " rtc-agent ",
		Token:     " rtc-token ",
		RTMUserID: " rtm-agent ",
		RTMToken:  " rtm-token ",
	})
	if err != nil {
		t.Fatalf("ResolveDataOptions() error = %v, want nil", err)
	}
	if opts.UID != "rtm-agent" {
		t.Fatalf("UID = %q, want RTM user id", opts.UID)
	}
	if opts.Token != "rtm-token" {
		t.Fatalf("Token = %q, want RTM token", opts.Token)
	}
}

func TestResolveDataOptionsDoesNotGenerateRTCToken(t *testing.T) {
	opts, err := ResolveDataOptions(Options{
		AppID:          "app",
		AppCertificate: "cert",
		Channel:        "support",
		UID:            "agent",
	})
	if err != nil {
		t.Fatalf("ResolveDataOptions() error = %v, want nil", err)
	}
	if opts.Token != "" {
		t.Fatalf("Token = %q, want empty token without RTM token", opts.Token)
	}
}
