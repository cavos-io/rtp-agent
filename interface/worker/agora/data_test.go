package agora

import (
	"testing"

	accesstoken "github.com/AgoraIO/Tools/DynamicKey/AgoraDynamicKey/go/src/accesstoken2"
)

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

func TestDataEnabledAcceptsPublishDataOrRTMEnabled(t *testing.T) {
	enabled := true
	disabled := false
	for _, tc := range []struct {
		name string
		opts Options
		want bool
	}{
		{name: "unset", opts: Options{}, want: true},
		{name: "publish_data", opts: Options{PublishData: &enabled}, want: true},
		{name: "rtm_enabled", opts: Options{RTMEnabled: &enabled}, want: true},
		{name: "rtm_disabled", opts: Options{RTMEnabled: &disabled}, want: false},
		{name: "disabled", opts: Options{PublishData: &disabled, RTMEnabled: &disabled}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DataEnabled(tc.opts); got != tc.want {
				t.Fatalf("DataEnabled() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestResolveDataOptionsUsesAppIDTokenWithoutCertificate(t *testing.T) {
	opts, err := ResolveDataOptions(Options{
		AppID:   "app",
		Channel: "support",
		UID:     "agent",
	})
	if err != nil {
		t.Fatalf("ResolveDataOptions() error = %v, want nil", err)
	}
	if opts.Token != "app" {
		t.Fatalf("Token = %q, want AppID token without app certificate", opts.Token)
	}
	if opts.UID != "agent" {
		t.Fatalf("UID = %q, want RTC UID fallback", opts.UID)
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

func TestResolveDataOptionsBuildsRTMTokenForResolvedRTMUser(t *testing.T) {
	opts, err := ResolveDataOptions(Options{
		AppID:          "970CA35de60c44645bbae8a215061b33",
		AppCertificate: "5CFd2fd1755d40ecb72977518be15d3b",
		Channel:        "support",
		UID:            "rtc-agent",
		RTMUserID:      "rtm-agent",
	})
	if err != nil {
		t.Fatalf("ResolveDataOptions() error = %v, want nil", err)
	}

	token := accesstoken.CreateAccessToken()
	ok, err := token.Parse(opts.Token)
	if err != nil {
		t.Fatalf("parse generated RTM token: %v", err)
	}
	if !ok {
		t.Fatal("generated RTM token did not parse")
	}
	rtmService, ok := token.Services[accesstoken.ServiceTypeRtm].(*accesstoken.ServiceRtm)
	if !ok {
		t.Fatalf("generated token RTM service = %#v, want RTM service", token.Services[accesstoken.ServiceTypeRtm])
	}
	if rtmService.UserId != "rtm-agent" {
		t.Fatalf("RTM token user id = %q, want resolved RTM user id", rtmService.UserId)
	}
}

func TestResolveDataOptionsRegeneratesRTMTokenWhenRTMUserOverridesRTCUser(t *testing.T) {
	opts, err := ResolveDataOptions(Options{
		AppID:          "970CA35de60c44645bbae8a215061b33",
		AppCertificate: "5CFd2fd1755d40ecb72977518be15d3b",
		Channel:        "support",
		UID:            "rtc-agent",
		Token:          "rtc-token",
		RTMUserID:      "rtm-agent",
	})
	if err != nil {
		t.Fatalf("ResolveDataOptions() error = %v, want nil", err)
	}
	if opts.Token == "rtc-token" {
		t.Fatal("ResolveDataOptions() reused RTC token after RTM user override")
	}
	token := accesstoken.CreateAccessToken()
	ok, err := token.Parse(opts.Token)
	if err != nil {
		t.Fatalf("parse generated RTM token: %v", err)
	}
	if !ok {
		t.Fatal("generated RTM token did not parse")
	}
	rtmService, ok := token.Services[accesstoken.ServiceTypeRtm].(*accesstoken.ServiceRtm)
	if !ok {
		t.Fatalf("generated token RTM service = %#v, want RTM service", token.Services[accesstoken.ServiceTypeRtm])
	}
	if rtmService.UserId != "rtm-agent" {
		t.Fatalf("RTM token user id = %q, want resolved RTM user id", rtmService.UserId)
	}
}

func TestResolveDataOptionsDoesNotReuseRTCTokenWhenRTMUserOverridesRTCUserWithoutCertificate(t *testing.T) {
	opts, err := ResolveDataOptions(Options{
		AppID:     "app",
		Channel:   "support",
		UID:       "rtc-agent",
		Token:     "rtc-token",
		RTMUserID: "rtm-agent",
	})
	if err != nil {
		t.Fatalf("ResolveDataOptions() error = %v, want nil", err)
	}
	if opts.UID != "rtm-agent" {
		t.Fatalf("UID = %q, want resolved RTM user id", opts.UID)
	}
	if opts.Token != "app" {
		t.Fatalf("Token = %q, want AppID token fallback instead of RTC token reuse", opts.Token)
	}
}
