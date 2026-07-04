package aws

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestAWSRealtimeDefaultsMatchReference(t *testing.T) {
	provider := NewAWSRealtimeModel("")

	if provider.Model() != "amazon.nova-2-sonic-v1:0" {
		t.Fatalf("Model = %q, want Nova Sonic 2 reference default", provider.Model())
	}
	if provider.Provider() != "Amazon" {
		t.Fatalf("Provider = %q, want Amazon", provider.Provider())
	}
	if provider.Region() != "us-east-1" {
		t.Fatalf("Region = %q, want us-east-1", provider.Region())
	}
	if provider.Voice() != "tiffany" {
		t.Fatalf("Voice = %q, want tiffany", provider.Voice())
	}
	if provider.Modalities() != "mixed" {
		t.Fatalf("Modalities = %q, want mixed", provider.Modalities())
	}
	if provider.TurnDetection() != "MEDIUM" {
		t.Fatalf("TurnDetection = %q, want MEDIUM", provider.TurnDetection())
	}
	if got, _ := provider.MaxTokens(); got != 1024 {
		t.Fatalf("MaxTokens = %d, want reference default 1024", got)
	}
	if got, _ := provider.TopP(); got != 0.9 {
		t.Fatalf("TopP = %v, want reference default 0.9", got)
	}
	if got, _ := provider.Temperature(); got != 0.7 {
		t.Fatalf("Temperature = %v, want reference default 0.7", got)
	}

	caps := provider.Capabilities()
	want := llm.RealtimeCapabilities{
		MessageTruncation:       false,
		TurnDetection:           true,
		UserTranscription:       true,
		AutoToolReplyGeneration: true,
		AudioOutput:             true,
		ManualFunctionCalls:     false,
		MutableChatContext:      true,
		MutableInstructions:     true,
		MutableTools:            true,
		PerResponseToolChoice:   false,
	}
	if caps != want {
		t.Fatalf("Capabilities = %+v, want %+v", caps, want)
	}
}

func TestAWSRealtimeOptionsAllowReferenceEmptyVoiceAndTurnDetection(t *testing.T) {
	provider := NewAWSRealtimeModel("",
		WithAWSRealtimeVoice(""),
		WithAWSRealtimeTurnDetection(""),
	)

	if provider.Voice() != "" {
		t.Fatalf("Voice = %q, want explicit empty reference voice", provider.Voice())
	}
	if provider.TurnDetection() != "" {
		t.Fatalf("TurnDetection = %q, want explicit empty reference turn detection", provider.TurnDetection())
	}
}

func TestAWSRealtimeOptionsAllowReferenceEmptyModel(t *testing.T) {
	provider := NewAWSRealtimeModel("custom-sonic", WithAWSRealtimeModel(""))

	if provider.Model() != "" {
		t.Fatalf("Model = %q, want explicit empty reference model", provider.Model())
	}
}

func TestAWSRealtimeOptionsAllowReferenceEmptyRegion(t *testing.T) {
	provider := NewAWSRealtimeModel("", WithAWSRealtimeRegion(""))

	if provider.Region() != "" {
		t.Fatalf("Region = %q, want explicit empty reference region", provider.Region())
	}
}

func TestAWSRealtimeMaxSessionDurationUsesReferenceEnv(t *testing.T) {
	t.Setenv("LK_SESSION_MAX_DURATION", "45")

	provider := NewAWSRealtimeModel("")

	if provider.maxSession != 45*time.Second {
		t.Fatalf("maxSession = %s, want 45s from LK_SESSION_MAX_DURATION", provider.maxSession)
	}
}

func TestAWSRealtimeSessionDurationUsesReferenceCredentialExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	expiry := now.Add(4 * time.Minute)
	provider := NewAWSRealtimeModel("",
		WithAWSRealtimeMaxSessionDuration(6*time.Minute),
		WithAWSRealtimeCredentialExpiry(func() (time.Time, bool) {
			return expiry, true
		}),
	)

	got := provider.sessionRecycleDuration(now)

	if got != time.Minute {
		t.Fatalf("session duration = %v, want reference credential expiry minus 3m buffer", got)
	}
}

func TestAWSRealtimeCredentialExpiryReadsSDKCredentials(t *testing.T) {
	expiry := time.Unix(2000, 0)
	getExpiry := awsRealtimeCredentialExpiry(context.Background(), fakeAWSRealtimeCredentialsProvider{
		creds: aws.Credentials{CanExpire: true, Expires: expiry},
	})

	got, ok := getExpiry()

	if !ok {
		t.Fatal("credential expiry ok = false, want true for expiring SDK credentials")
	}
	if !got.Equal(expiry) {
		t.Fatalf("credential expiry = %v, want %v", got, expiry)
	}

	getStaticExpiry := awsRealtimeCredentialExpiry(context.Background(), fakeAWSRealtimeCredentialsProvider{
		creds: aws.Credentials{CanExpire: false},
	})
	if _, ok := getStaticExpiry(); ok {
		t.Fatal("static credential expiry ok = true, want false")
	}

	getErrorExpiry := awsRealtimeCredentialExpiry(context.Background(), fakeAWSRealtimeCredentialsProvider{
		err: errors.New("credential load failed"),
	})
	if _, ok := getErrorExpiry(); ok {
		t.Fatal("errored credential expiry ok = true, want false")
	}
}

func TestAWSRealtimeNovaSonicConstructorsMatchReference(t *testing.T) {
	sonic1 := NewAWSRealtimeModelWithNovaSonic1(
		WithAWSRealtimeVoice("matthew"),
		WithAWSRealtimeRegion("us-west-2"),
	)
	if sonic1.Model() != "amazon.nova-sonic-v1:0" {
		t.Fatalf("Sonic 1 model = %q, want reference model", sonic1.Model())
	}
	if sonic1.Modalities() != "audio" {
		t.Fatalf("Sonic 1 modalities = %q, want audio", sonic1.Modalities())
	}
	if sonic1.Voice() != "matthew" {
		t.Fatalf("Sonic 1 voice = %q, want matthew", sonic1.Voice())
	}
	if sonic1.Region() != "us-west-2" {
		t.Fatalf("Sonic 1 region = %q, want us-west-2", sonic1.Region())
	}

	sonic2 := NewAWSRealtimeModelWithNovaSonic2(
		WithAWSRealtimeModel("custom-sonic"),
		WithAWSRealtimeTurnDetection("HIGH"),
	)
	if sonic2.Model() != "custom-sonic" {
		t.Fatalf("Sonic 2 model = %q, want custom override", sonic2.Model())
	}
	if sonic2.Modalities() != "mixed" {
		t.Fatalf("Sonic 2 modalities = %q, want mixed", sonic2.Modalities())
	}
	if sonic2.TurnDetection() != "HIGH" {
		t.Fatalf("Sonic 2 turn detection = %q, want HIGH", sonic2.TurnDetection())
	}
}

func TestAWSRealtimeNovaSonicSurfaceConstantsMatchReference(t *testing.T) {
	if AWSRealtimeModelNovaSonic1 != "amazon.nova-sonic-v1:0" {
		t.Fatalf("AWSRealtimeModelNovaSonic1 = %q, want reference Sonic 1 model", AWSRealtimeModelNovaSonic1)
	}
	if AWSRealtimeModelNovaSonic2 != "amazon.nova-2-sonic-v1:0" {
		t.Fatalf("AWSRealtimeModelNovaSonic2 = %q, want reference Sonic 2 model", AWSRealtimeModelNovaSonic2)
	}
	if AWSRealtimeModalitiesAudio != "audio" || AWSRealtimeModalitiesMixed != "mixed" {
		t.Fatalf("modalities = %q/%q, want audio/mixed", AWSRealtimeModalitiesAudio, AWSRealtimeModalitiesMixed)
	}
	if AWSRealtimeTurnDetectionHigh != "HIGH" ||
		AWSRealtimeTurnDetectionMedium != "MEDIUM" ||
		AWSRealtimeTurnDetectionLow != "LOW" {
		t.Fatalf("turn detection constants = %q/%q/%q, want HIGH/MEDIUM/LOW",
			AWSRealtimeTurnDetectionHigh,
			AWSRealtimeTurnDetectionMedium,
			AWSRealtimeTurnDetectionLow,
		)
	}

	wantSonic1 := []string{
		"matthew", "tiffany", "amy", "lupe", "carlos", "ambre",
		"florian", "greta", "lennart", "beatrice", "lorenzo",
	}
	if !stringSlicesEqual(AWSRealtimeSonic1Voices(), wantSonic1) {
		t.Fatalf("AWSRealtimeSonic1Voices = %#v, want %#v", AWSRealtimeSonic1Voices(), wantSonic1)
	}
	wantSonic2 := []string{
		"matthew", "tiffany", "amy", "olivia", "lupe", "carlos", "ambre",
		"florian", "tina", "lennart", "beatrice", "lorenzo", "carolina",
		"leo", "arjun", "kiara",
	}
	if !stringSlicesEqual(AWSRealtimeSonic2Voices(), wantSonic2) {
		t.Fatalf("AWSRealtimeSonic2Voices = %#v, want %#v", AWSRealtimeSonic2Voices(), wantSonic2)
	}

	voices := AWSRealtimeSonic2Voices()
	voices[0] = "mutated"
	if AWSRealtimeSonic2Voices()[0] != "matthew" {
		t.Fatal("AWSRealtimeSonic2Voices returned mutable backing slice")
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type fakeAWSRealtimeCredentialsProvider struct {
	creds aws.Credentials
	err   error
}

func (p fakeAWSRealtimeCredentialsProvider) Retrieve(context.Context) (aws.Credentials, error) {
	return p.creds, p.err
}
