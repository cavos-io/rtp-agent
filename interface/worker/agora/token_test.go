package agora

import (
	"testing"
	"time"
)

func TestDefaultTokenTTLFollowsTENServerExpiration(t *testing.T) {
	if defaultTokenTTL != 24*time.Hour {
		t.Fatalf("defaultTokenTTL = %s, want TEN server default 24h", defaultTokenTTL)
	}
}

func TestResolveJoinOptionsPreservesExplicitToken(t *testing.T) {
	opts := Options{
		AppID:          "app",
		AppCertificate: "cert",
		Channel:        "support",
		UID:            "agent",
		Token:          "provided-token",
	}

	resolved, err := ResolveJoinOptions(opts)
	if err != nil {
		t.Fatalf("ResolveJoinOptions() error = %v", err)
	}

	if resolved.Token != "provided-token" {
		t.Fatalf("resolved token = %q, want provided-token", resolved.Token)
	}
}

func TestResolveJoinOptionsTrimsJoinFields(t *testing.T) {
	opts := Options{
		AppID:          " app ",
		AppCertificate: " cert ",
		Channel:        " support ",
		UID:            " agent ",
		Token:          " token ",
		RemoteStreamID: " remote-42 ",
	}

	resolved, err := ResolveJoinOptions(opts)
	if err != nil {
		t.Fatalf("ResolveJoinOptions() error = %v", err)
	}

	if resolved.AppID != "app" {
		t.Fatalf("resolved AppID = %q, want app", resolved.AppID)
	}
	if resolved.AppCertificate != "cert" {
		t.Fatalf("resolved AppCertificate = %q, want cert", resolved.AppCertificate)
	}
	if resolved.Channel != "support" {
		t.Fatalf("resolved Channel = %q, want support", resolved.Channel)
	}
	if resolved.UID != "agent" {
		t.Fatalf("resolved UID = %q, want agent", resolved.UID)
	}
	if resolved.Token != "token" {
		t.Fatalf("resolved Token = %q, want token", resolved.Token)
	}
	if resolved.RemoteStreamID != "remote-42" {
		t.Fatalf("resolved RemoteStreamID = %q, want remote-42", resolved.RemoteStreamID)
	}
}

func TestResolveJoinOptionsBuildsTokenFromCertificate(t *testing.T) {
	opts := Options{
		AppID:          "970CA35de60c44645bbae8a215061b33",
		AppCertificate: "5CFd2fd1755d40ecb72977518be15d3b",
		Channel:        "support",
		UID:            "agent",
	}

	resolved, err := ResolveJoinOptions(opts)
	if err != nil {
		t.Fatalf("ResolveJoinOptions() error = %v", err)
	}

	if resolved.Token == "" {
		t.Fatal("resolved token is empty, want generated RTC token")
	}
	if resolved.Token == opts.Token {
		t.Fatal("resolved token was not generated")
	}
}

func TestResolveJoinOptionsUsesAppIDTokenWithoutCertificate(t *testing.T) {
	opts := Options{
		AppID:   "app",
		Channel: "support",
		UID:     "agent",
	}

	resolved, err := ResolveJoinOptions(opts)
	if err != nil {
		t.Fatalf("ResolveJoinOptions() error = %v", err)
	}

	if resolved.Token != "app" {
		t.Fatalf("resolved Token = %q, want AppID token without app certificate", resolved.Token)
	}
	if resolved.UID != "agent" {
		t.Fatalf("resolved UID = %q, want explicit UID", resolved.UID)
	}
}

func TestResolveJoinOptionsDefaultsEmptyUIDForTokenGeneration(t *testing.T) {
	opts := Options{
		AppID:          "970CA35de60c44645bbae8a215061b33",
		AppCertificate: "5CFd2fd1755d40ecb72977518be15d3b",
		Channel:        "support",
	}

	resolved, err := ResolveJoinOptions(opts)
	if err != nil {
		t.Fatalf("ResolveJoinOptions() error = %v", err)
	}

	if resolved.UID != "0" {
		t.Fatalf("resolved UID = %q, want 0", resolved.UID)
	}
	if resolved.Token == "" {
		t.Fatal("resolved token is empty, want generated RTC token")
	}
}

func TestResolveJoinOptionsDefaultsPublishAudioToTENEnabled(t *testing.T) {
	opts := Options{
		AppID:   "app",
		Channel: "support",
	}

	resolved, err := ResolveJoinOptions(opts)
	if err != nil {
		t.Fatalf("ResolveJoinOptions() error = %v", err)
	}

	if resolved.PublishAudio == nil || !*resolved.PublishAudio {
		t.Fatalf("resolved PublishAudio = %#v, want enabled by default", resolved.PublishAudio)
	}
}

func TestResolveJoinOptionsPreservesPublishAudioDisabled(t *testing.T) {
	disabled := false
	opts := Options{
		AppID:        "app",
		Channel:      "support",
		PublishAudio: &disabled,
	}

	resolved, err := ResolveJoinOptions(opts)
	if err != nil {
		t.Fatalf("ResolveJoinOptions() error = %v", err)
	}

	if resolved.PublishAudio == nil || *resolved.PublishAudio {
		t.Fatalf("resolved PublishAudio = %#v, want disabled", resolved.PublishAudio)
	}
}

func TestResolveJoinOptionsDefaultsSubscribeAudioToTENEnabled(t *testing.T) {
	opts := Options{
		AppID:   "app",
		Channel: "support",
	}

	resolved, err := ResolveJoinOptions(opts)
	if err != nil {
		t.Fatalf("ResolveJoinOptions() error = %v", err)
	}

	if resolved.SubscribeAudio == nil || !*resolved.SubscribeAudio {
		t.Fatalf("resolved SubscribeAudio = %#v, want enabled by default", resolved.SubscribeAudio)
	}
}

func TestResolveJoinOptionsPreservesSubscribeAudioDisabled(t *testing.T) {
	disabled := false
	opts := Options{
		AppID:          "app",
		Channel:        "support",
		SubscribeAudio: &disabled,
	}

	resolved, err := ResolveJoinOptions(opts)
	if err != nil {
		t.Fatalf("ResolveJoinOptions() error = %v", err)
	}

	if resolved.SubscribeAudio == nil || *resolved.SubscribeAudio {
		t.Fatalf("resolved SubscribeAudio = %#v, want disabled", resolved.SubscribeAudio)
	}
}

func TestOptionsValidateRequiresAppIDAndChannel(t *testing.T) {
	if err := (Options{}).Validate(); err == nil {
		t.Fatal("Options.Validate() error = nil, want missing app ID")
	}
	if err := (Options{AppID: "app"}).Validate(); err == nil {
		t.Fatal("Options.Validate() error = nil, want missing channel")
	}
	if err := (Options{AppID: "app", Channel: "support"}).Validate(); err != nil {
		t.Fatalf("Options.Validate() error = %v, want nil", err)
	}
}

func TestAcceptRemoteStreamMatchesConfiguredStream(t *testing.T) {
	if !acceptRemoteStream("", "caller-1") {
		t.Fatal("acceptRemoteStream(empty, caller-1) = false, want true")
	}
	if !acceptRemoteStream("caller-1", "caller-1") {
		t.Fatal("acceptRemoteStream(caller-1, caller-1) = false, want true")
	}
	if acceptRemoteStream("caller-1", "caller-2") {
		t.Fatal("acceptRemoteStream(caller-1, caller-2) = true, want false")
	}
}

func TestAcceptChannelMatchesConfiguredChannel(t *testing.T) {
	if !acceptChannel("support", "") {
		t.Fatal("acceptChannel(support, empty) = false, want true for SDK callbacks without channel metadata")
	}
	if !acceptChannel(" support ", "support") {
		t.Fatal("acceptChannel(trimmed support, support) = false, want true")
	}
	if acceptChannel("support", "sales") {
		t.Fatal("acceptChannel(support, sales) = true, want false")
	}
}
