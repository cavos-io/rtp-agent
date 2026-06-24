package worker

import "testing"

func TestResolveCompatibilityDefaultsLiveKitToConsoleProfile(t *testing.T) {
	info, err := ResolveCompatibility(CompatibilityResolveOptions{
		Transport: WorkerTransportLiveKit,
	})
	if err != nil {
		t.Fatalf("ResolveCompatibility error = %v", err)
	}
	if info.Profile != CompatibilityProfileLiveKitConsole152Basic {
		t.Fatalf("Profile = %q, want %q", info.Profile, CompatibilityProfileLiveKitConsole152Basic)
	}
	if info.AdvertisedFamily != CompatibilityFamilyLiveKitAgentsPython {
		t.Fatalf("AdvertisedFamily = %q, want %q", info.AdvertisedFamily, CompatibilityFamilyLiveKitAgentsPython)
	}
	if info.AdvertisedVersion != "1.5.2" {
		t.Fatalf("AdvertisedVersion = %q, want 1.5.2", info.AdvertisedVersion)
	}
	if !hasCompatibilityCapability(info, CompatibilityCapabilityWorkerRegistration) {
		t.Fatalf("Capabilities = %#v, want worker registration", info.Capabilities)
	}
}

func TestResolveCompatibilityHonorsAdvertisedVersionOverride(t *testing.T) {
	info, err := ResolveCompatibility(CompatibilityResolveOptions{
		Transport:                 WorkerTransportLiveKit,
		AdvertisedVersionOverride: "1.5.15",
		RuntimeVersion:            "0.7.0",
	})
	if err != nil {
		t.Fatalf("ResolveCompatibility error = %v", err)
	}
	if info.AdvertisedVersion != "1.5.15" {
		t.Fatalf("AdvertisedVersion = %q, want override", info.AdvertisedVersion)
	}
	if info.RuntimeVersion != "0.7.0" {
		t.Fatalf("RuntimeVersion = %q, want 0.7.0", info.RuntimeVersion)
	}
}

func TestResolveCompatibilityDefaultsAgoraToRTCProfile(t *testing.T) {
	info, err := ResolveCompatibility(CompatibilityResolveOptions{
		Transport: WorkerTransportAgora,
	})
	if err != nil {
		t.Fatalf("ResolveCompatibility error = %v", err)
	}
	if info.Profile != CompatibilityProfileAgoraRTCBasic {
		t.Fatalf("Profile = %q, want %q", info.Profile, CompatibilityProfileAgoraRTCBasic)
	}
	if info.AdvertisedFamily != CompatibilityFamilyAgoraRTCTransport {
		t.Fatalf("AdvertisedFamily = %q, want %q", info.AdvertisedFamily, CompatibilityFamilyAgoraRTCTransport)
	}
	if info.AdvertisedVersion == "1.5.2" {
		t.Fatalf("AdvertisedVersion = %q, want non-LiveKit compatibility", info.AdvertisedVersion)
	}
	if !hasCompatibilityCapability(info, CompatibilityCapabilityAgoraChannelJoin) {
		t.Fatalf("Capabilities = %#v, want Agora channel join", info.Capabilities)
	}
}

func TestResolveCompatibilityRejectsUnknownProfile(t *testing.T) {
	_, err := ResolveCompatibility(CompatibilityResolveOptions{
		Transport: WorkerTransportLiveKit,
		Profile:   "unknown-profile",
	})
	if err == nil {
		t.Fatal("ResolveCompatibility error = nil, want unknown profile error")
	}
}

func TestResolveCompatibilityRejectsTransportMismatch(t *testing.T) {
	_, err := ResolveCompatibility(CompatibilityResolveOptions{
		Transport: WorkerTransportAgora,
		Profile:   CompatibilityProfileLiveKitConsole152Basic,
	})
	if err == nil {
		t.Fatal("ResolveCompatibility error = nil, want transport mismatch error")
	}
}
