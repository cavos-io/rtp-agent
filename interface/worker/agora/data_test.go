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

func TestResolveDataOptionsDoesNotGenerateTokenWithoutCertificate(t *testing.T) {
	opts, err := ResolveDataOptions(Options{
		AppID:   "app",
		Channel: "support",
		UID:     "agent",
	})
	if err != nil {
		t.Fatalf("ResolveDataOptions() error = %v, want nil", err)
	}
	if opts.Token != "" {
		t.Fatalf("Token = %q, want empty token without RTM token or app certificate", opts.Token)
	}
}

func TestResolveDataOptionsBuildsRTMTokenFromCertificate(t *testing.T) {
	opts, err := ResolveDataOptions(Options{
		AppID:          "970CA35de60c44645bbae8a215061b33",
		AppCertificate: "5CFd2fd1755d40ecb72977518be15d3b",
		Channel:        "support",
		UID:            "agent",
	})
	if err != nil {
		t.Fatalf("ResolveDataOptions() error = %v, want nil", err)
	}
	if opts.Token == "" {
		t.Fatal("Token is empty, want generated RTM token")
	}
}
