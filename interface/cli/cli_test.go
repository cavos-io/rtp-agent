package cli

import (
	"strings"
	"testing"

	"github.com/cavos-io/conversation-worker/interface/worker/ipc"
	"github.com/livekit/protocol/livekit"
)

func TestParseConnectArgsUsesProvidedIdentity(t *testing.T) {
	args, err := parseConnectArgs([]string{"worker", "connect", "room-a", "agent-custom"})
	if err != nil {
		t.Fatalf("parseConnectArgs() error = %v", err)
	}

	if args.RoomName != "room-a" {
		t.Fatalf("RoomName = %q, want room-a", args.RoomName)
	}
	if args.ParticipantIdentity != "agent-custom" {
		t.Fatalf("ParticipantIdentity = %q, want agent-custom", args.ParticipantIdentity)
	}
}

func TestParseConnectArgsDefaultsAgentIdentity(t *testing.T) {
	args, err := parseConnectArgs([]string{"worker", "connect", "room-a"})
	if err != nil {
		t.Fatalf("parseConnectArgs() error = %v", err)
	}

	if !strings.HasPrefix(args.ParticipantIdentity, "agent-") {
		t.Fatalf("ParticipantIdentity = %q, want agent- prefix", args.ParticipantIdentity)
	}
	if args.ParticipantIdentity == "agent-" {
		t.Fatal("ParticipantIdentity is only the prefix")
	}
}

func TestParseConnectArgsRequiresRoom(t *testing.T) {
	_, err := parseConnectArgs([]string{"worker", "connect"})
	if err == nil {
		t.Fatal("parseConnectArgs() error = nil, want missing room error")
	}
}

func TestCliArgsCarriesReferenceReloadState(t *testing.T) {
	args := CliArgs{
		LogLevel:    "debug",
		URL:         "wss://livekit.example",
		APIKey:      "api-key",
		APISecret:   "api-secret",
		DevMode:     true,
		Reload:      true,
		ReloadCount: 2,
	}

	if !args.Reload {
		t.Fatal("Reload = false, want true")
	}
	if args.ReloadCount != 2 {
		t.Fatalf("ReloadCount = %d, want 2", args.ReloadCount)
	}
}

func TestWatcherReloadStateIgnoresOverlappingReloads(t *testing.T) {
	watcher := NewWatcher(nil, nil)

	if !watcher.beginReload() {
		t.Fatal("first beginReload() = false, want true")
	}
	if watcher.beginReload() {
		t.Fatal("second beginReload() = true, want false while reload is active")
	}

	watcher.Reloaded()
	if !watcher.beginReload() {
		t.Fatal("beginReload() after Reloaded() = false, want true")
	}
}

func TestWatcherBeginReloadIncrementsCliReloadCount(t *testing.T) {
	args := &CliArgs{ReloadCount: 2}
	watcher := NewWatcher(nil, nil, args)

	if !watcher.beginReload() {
		t.Fatal("beginReload() = false, want true")
	}
	if args.ReloadCount != 3 {
		t.Fatalf("ReloadCount after beginReload() = %d, want 3", args.ReloadCount)
	}

	if watcher.beginReload() {
		t.Fatal("overlapping beginReload() = true, want false")
	}
	if args.ReloadCount != 3 {
		t.Fatalf("ReloadCount after overlapping beginReload() = %d, want unchanged 3", args.ReloadCount)
	}
}

func TestWatcherTriggerReloadKeepsReloadingUntilReloaded(t *testing.T) {
	args := &CliArgs{}
	calls := 0
	watcher := NewWatcher(nil, func() {
		calls++
	}, args)

	if !watcher.triggerReload() {
		t.Fatal("triggerReload() = false, want true")
	}
	if calls != 1 {
		t.Fatalf("onChange calls = %d, want 1", calls)
	}
	if args.ReloadCount != 1 {
		t.Fatalf("ReloadCount = %d, want 1", args.ReloadCount)
	}
	if watcher.triggerReload() {
		t.Fatal("overlapping triggerReload() = true, want false")
	}
	if calls != 1 {
		t.Fatalf("onChange calls after overlapping reload = %d, want 1", calls)
	}

	watcher.Reloaded()
	if !watcher.triggerReload() {
		t.Fatal("triggerReload() after Reloaded() = false, want true")
	}
	if calls != 2 {
		t.Fatalf("onChange calls after Reloaded() = %d, want 2", calls)
	}
}

func TestWatcherStoresActiveJobsForCurrentReload(t *testing.T) {
	args := &CliArgs{ReloadCount: 2}
	watcher := NewWatcher(nil, nil, args)

	staleJobs := []ipc.RunningJobInfo{{Job: &livekit.Job{Id: "job-stale"}, Token: "stale-token"}}
	if watcher.recordActiveJobsResponse(ipc.ActiveJobsResponse{Jobs: staleJobs, ReloadCount: 1}) {
		t.Fatal("recordActiveJobsResponse() accepted stale reload count")
	}
	if got := watcher.reloadJobsResponse(); len(got.Jobs) != 0 {
		t.Fatalf("reloadJobsResponse() stale jobs len = %d, want 0", len(got.Jobs))
	}

	currentJobs := []ipc.RunningJobInfo{{Job: &livekit.Job{Id: "job-current"}, Token: "current-token"}}
	if !watcher.recordActiveJobsResponse(ipc.ActiveJobsResponse{Jobs: currentJobs, ReloadCount: 2}) {
		t.Fatal("recordActiveJobsResponse() rejected current reload count")
	}

	resp := watcher.reloadJobsResponse()
	if resp.ReloadCount != 2 {
		t.Fatalf("ReloadJobsResponse.ReloadCount = %d, want 2", resp.ReloadCount)
	}
	if len(resp.Jobs) != 1 {
		t.Fatalf("ReloadJobsResponse.Jobs len = %d, want 1", len(resp.Jobs))
	}
	if resp.Jobs[0].Job.GetId() != "job-current" {
		t.Fatalf("ReloadJobsResponse.Jobs[0].Job.Id = %q, want job-current", resp.Jobs[0].Job.GetId())
	}

	resp.Jobs[0].Token = "mutated"
	if got := watcher.reloadJobsResponse(); got.Jobs[0].Token != "current-token" {
		t.Fatalf("mutating ReloadJobsResponse changed stored token to %q", got.Jobs[0].Token)
	}
}
