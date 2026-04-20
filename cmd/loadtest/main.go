package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
)

// ─── Configuration ──────────────────────────────────────────────────────────

type Config struct {
	NumRooms       int
	RampUpDuration time.Duration
	TestDuration   time.Duration
	AgentName      string
	RoomPrefix     string
	LivekitURL     string
	APIKey         string
	APISecret      string
	HTTPUrl        string
	Verbose        bool
}

// ─── Metrics ────────────────────────────────────────────────────────────────

type RoomMetrics struct {
	RoomName        string
	DispatchLatency time.Duration // Time from dispatch to agent joining
	AgentJoinTime   time.Time
	FirstAudioTime  time.Time     // Time agent publishes first audio track
	AudioLatency    time.Duration // Time from agent join to first audio
	DataReceived    int64         // Number of data packets received
	TracksReceived  int32         // Number of tracks subscribed
	Error           string
}

type TestResult struct {
	Config           Config
	StartTime        time.Time
	EndTime          time.Time
	TotalDuration    time.Duration
	RoomsCreated     int
	RoomsSucceeded   int
	RoomsFailed      int
	AgentsJoined     int
	AgentsWithAudio  int
	DispatchLatencies []time.Duration
	AudioLatencies    []time.Duration
	Errors           []string
	Rooms            []RoomMetrics
}

// ─── Room Test Runner ───────────────────────────────────────────────────────

type RoomTest struct {
	config    Config
	roomName  string
	metrics   RoomMetrics
	room      *lksdk.Room
	dispatch  *lksdk.AgentDispatchClient
	svc       *lksdk.RoomServiceClient
	mu        sync.Mutex
}

func NewRoomTest(config Config, index int) *RoomTest {
	roomName := fmt.Sprintf("%s-%d-%s", config.RoomPrefix, index, randomSuffix())
	return &RoomTest{
		config:   config,
		roomName: roomName,
		metrics:  RoomMetrics{RoomName: roomName},
		dispatch: lksdk.NewAgentDispatchServiceClient(config.HTTPUrl, config.APIKey, config.APISecret),
		svc:      lksdk.NewRoomServiceClient(config.HTTPUrl, config.APIKey, config.APISecret),
	}
}

func (rt *RoomTest) Run(ctx context.Context) RoomMetrics {
	defer func() {
		// Cleanup: disconnect and delete room
		if rt.room != nil {
			rt.room.Disconnect()
		}
		rt.svc.DeleteRoom(context.Background(), &livekit.DeleteRoomRequest{Room: rt.roomName})
	}()

	// Step 1: Connect as a test participant
	dispatchStart := time.Now()

	cb := lksdk.NewRoomCallback()

	agentJoinCh := make(chan struct{}, 1)
	audioTrackCh := make(chan struct{}, 1)

	cb.OnParticipantConnected = func(p *lksdk.RemoteParticipant) {
		if p.Kind() == lksdk.ParticipantAgent {
			rt.mu.Lock()
			rt.metrics.AgentJoinTime = time.Now()
			rt.metrics.DispatchLatency = time.Since(dispatchStart)
			rt.mu.Unlock()
			select {
			case agentJoinCh <- struct{}{}:
			default:
			}
			if rt.config.Verbose {
				fmt.Printf("  [%s] Agent joined (%.1fs)\n", rt.roomName, rt.metrics.DispatchLatency.Seconds())
			}
		}
	}

	cb.OnTrackSubscribed = func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
		atomic.AddInt32(&rt.metrics.TracksReceived, 1)
		if track.Kind() == webrtc.RTPCodecTypeAudio && rp.Kind() == lksdk.ParticipantAgent {
			rt.mu.Lock()
			if rt.metrics.FirstAudioTime.IsZero() {
				rt.metrics.FirstAudioTime = time.Now()
				if !rt.metrics.AgentJoinTime.IsZero() {
					rt.metrics.AudioLatency = rt.metrics.FirstAudioTime.Sub(rt.metrics.AgentJoinTime)
				}
			}
			rt.mu.Unlock()
			select {
			case audioTrackCh <- struct{}{}:
			default:
			}
			if rt.config.Verbose {
				fmt.Printf("  [%s] Agent audio track received (%.1fs after join)\n", rt.roomName, rt.metrics.AudioLatency.Seconds())
			}
		}
	}

	cb.ParticipantCallback.OnDataPacket = func(data lksdk.DataPacket, params lksdk.DataReceiveParams) {
		atomic.AddInt64(&rt.metrics.DataReceived, 1)
	}

	var err error
	rt.room, err = lksdk.ConnectToRoom(rt.config.LivekitURL, lksdk.ConnectInfo{
		APIKey:              rt.config.APIKey,
		APISecret:           rt.config.APISecret,
		RoomName:            rt.roomName,
		ParticipantIdentity: fmt.Sprintf("loadtest-user-%s", randomSuffix()),
	}, cb)
	if err != nil {
		rt.metrics.Error = fmt.Sprintf("connect failed: %v", err)
		return rt.metrics
	}

	// Register text stream handlers
	for _, topic := range []string{"lk.chat", "lk.transcription"} {
		rt.room.RegisterTextStreamHandler(topic, func(reader *lksdk.TextStreamReader, pid string) {
			atomic.AddInt64(&rt.metrics.DataReceived, 1)
		})
	}

	// Step 2: Dispatch agent
	dispatch, err := rt.dispatch.CreateDispatch(ctx, &livekit.CreateAgentDispatchRequest{
		AgentName: rt.config.AgentName,
		Room:      rt.roomName,
	})
	if err != nil {
		rt.metrics.Error = fmt.Sprintf("dispatch failed: %v", err)
		return rt.metrics
	}
	_ = dispatch // Ensure dispatch is used to avoid 'declared and not used' error

	// Step 3: Wait for agent to join (with timeout)
	joinTimeout := 30 * time.Second
	select {
	case <-agentJoinCh:
		// Agent joined
	case <-time.After(joinTimeout):
		rt.metrics.Error = "agent join timeout (30s)"
		return rt.metrics
	case <-ctx.Done():
		rt.metrics.Error = "cancelled"
		return rt.metrics
	}

	// Step 4: Wait for audio track (with timeout)
	audioTimeout := 30 * time.Second
	select {
	case <-audioTrackCh:
		// Audio received
	case <-time.After(audioTimeout):
		// Agent joined but no audio — still partial success
		if rt.metrics.Error == "" {
			rt.metrics.Error = "no audio track received (30s)"
		}
	case <-ctx.Done():
		rt.metrics.Error = "cancelled"
		return rt.metrics
	}

	// Step 5: Keep connection alive for test duration
	remainingDuration := rt.config.TestDuration - time.Since(dispatchStart)
	if remainingDuration > 0 {
		select {
		case <-time.After(remainingDuration):
		case <-ctx.Done():
		}
	}

	return rt.metrics
}

// ─── Load Test Orchestrator ─────────────────────────────────────────────────

func runLoadTest(config Config) *TestResult {
	result := &TestResult{
		Config:    config,
		StartTime: time.Now(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\n⚠️  Interrupted — cleaning up...")
		cancel()
	}()

	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("  LiveKit Agent Load Test")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Printf("  Rooms:          %d\n", config.NumRooms)
	fmt.Printf("  Ramp-up:        %s\n", config.RampUpDuration)
	fmt.Printf("  Test duration:  %s per room\n", config.TestDuration)
	fmt.Printf("  Agent:          %s\n", config.AgentName)
	fmt.Printf("  LiveKit URL:    %s\n", config.LivekitURL)
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	// Run rooms with ramp-up
	var wg sync.WaitGroup
	metricsCh := make(chan RoomMetrics, config.NumRooms)

	rampInterval := config.RampUpDuration / time.Duration(max(config.NumRooms, 1))

	for i := 0; i < config.NumRooms; i++ {
		if ctx.Err() != nil {
			break
		}

		wg.Add(1)
		rt := NewRoomTest(config, i)
		result.RoomsCreated++

		fmt.Printf("🚀 [%d/%d] Starting room: %s\n", i+1, config.NumRooms, rt.roomName)

		go func() {
			defer wg.Done()
			metrics := rt.Run(ctx)
			metricsCh <- metrics
		}()

		// Ramp-up delay between rooms
		if i < config.NumRooms-1 && rampInterval > 0 {
			select {
			case <-time.After(rampInterval):
			case <-ctx.Done():
			}
		}
	}

	// Collect results
	go func() {
		wg.Wait()
		close(metricsCh)
	}()

	for m := range metricsCh {
		result.Rooms = append(result.Rooms, m)
		if m.Error != "" {
			result.RoomsFailed++
			result.Errors = append(result.Errors, fmt.Sprintf("[%s] %s", m.RoomName, m.Error))
		} else {
			result.RoomsSucceeded++
		}
		if !m.AgentJoinTime.IsZero() {
			result.AgentsJoined++
			result.DispatchLatencies = append(result.DispatchLatencies, m.DispatchLatency)
		}
		if !m.FirstAudioTime.IsZero() {
			result.AgentsWithAudio++
			result.AudioLatencies = append(result.AudioLatencies, m.AudioLatency)
		}
	}

	result.EndTime = time.Now()
	result.TotalDuration = result.EndTime.Sub(result.StartTime)

	return result
}

// ─── Report ─────────────────────────────────────────────────────────────────

func printReport(result *TestResult) {
	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("  LOAD TEST RESULTS")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	// Summary
	fmt.Println("── Summary ────────────────────────────────────────────────────")
	fmt.Printf("  Total duration:    %s\n", result.TotalDuration.Round(time.Millisecond))
	fmt.Printf("  Rooms created:     %d\n", result.RoomsCreated)
	fmt.Printf("  Rooms succeeded:   %d (%.0f%%)\n", result.RoomsSucceeded, pct(result.RoomsSucceeded, result.RoomsCreated))
	fmt.Printf("  Rooms failed:      %d (%.0f%%)\n", result.RoomsFailed, pct(result.RoomsFailed, result.RoomsCreated))
	fmt.Printf("  Agents joined:     %d (%.0f%%)\n", result.AgentsJoined, pct(result.AgentsJoined, result.RoomsCreated))
	fmt.Printf("  Agents with audio: %d (%.0f%%)\n", result.AgentsWithAudio, pct(result.AgentsWithAudio, result.RoomsCreated))
	fmt.Println()

	// Dispatch Latency
	if len(result.DispatchLatencies) > 0 {
		fmt.Println("── Dispatch Latency (agent join time) ─────────────────────────")
		printLatencyStats(result.DispatchLatencies)
		fmt.Println()
	}

	// Audio Latency
	if len(result.AudioLatencies) > 0 {
		fmt.Println("── Audio Latency (join -> first audio) ────────────────────────")
		printLatencyStats(result.AudioLatencies)
		fmt.Println()
	}

	// Per-room details
	fmt.Println("── Per-Room Details ───────────────────────────────────────────")
	fmt.Printf("  %-30s %-12s %-12s %-8s %s\n", "ROOM", "DISPATCH", "AUDIO", "DATA", "STATUS")
	fmt.Printf("  %-30s %-12s %-12s %-8s %s\n", "────", "────────", "─────", "────", "──────")
	for _, m := range result.Rooms {
		dispatch := "-"
		audio := "-"
		status := "OK"
		if m.DispatchLatency > 0 {
			dispatch = fmt.Sprintf("%.1fs", m.DispatchLatency.Seconds())
		}
		if m.AudioLatency > 0 {
			audio = fmt.Sprintf("%.1fs", m.AudioLatency.Seconds())
		}
		if m.Error != "" {
			status = "FAIL: " + m.Error
		}
		fmt.Printf("  %-30s %-12s %-12s %-8d %s\n", m.RoomName, dispatch, audio, m.DataReceived, status)
	}
	fmt.Println()

	// Errors
	if len(result.Errors) > 0 {
		fmt.Println("── Errors ─────────────────────────────────────────────────────")
		for _, e := range result.Errors {
			fmt.Printf("  %s\n", e)
		}
		fmt.Println()
	}

	fmt.Println("═══════════════════════════════════════════════════════════════")
}

func printLatencyStats(latencies []time.Duration) {
	if len(latencies) == 0 {
		return
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	var sum time.Duration
	for _, l := range latencies {
		sum += l
	}
	avg := sum / time.Duration(len(latencies))

	fmt.Printf("  Min:     %s\n", latencies[0].Round(time.Millisecond))
	fmt.Printf("  Max:     %s\n", latencies[len(latencies)-1].Round(time.Millisecond))
	fmt.Printf("  Avg:     %s\n", avg.Round(time.Millisecond))
	fmt.Printf("  P50:     %s\n", percentile(latencies, 50).Round(time.Millisecond))
	fmt.Printf("  P90:     %s\n", percentile(latencies, 90).Round(time.Millisecond))
	fmt.Printf("  P99:     %s\n", percentile(latencies, 99).Round(time.Millisecond))
	fmt.Printf("  Samples: %d\n", len(latencies))
}

func saveJSON(result *TestResult, path string) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func pct(num, denom int) float64 {
	if denom == 0 {
		return 0
	}
	return float64(num) / float64(denom) * 100
}

func randomSuffix() string {
	return fmt.Sprintf("%04x", time.Now().UnixNano()&0xFFFF)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ─── Main ───────────────────────────────────────────────────────────────────

func main() {
	loadDotEnv("../../.env")

	numRooms := flag.Int("rooms", 5, "Number of concurrent rooms")
	rampUp := flag.Duration("ramp-up", 10*time.Second, "Ramp-up duration (spread room creation)")
	duration := flag.Duration("duration", 30*time.Second, "Test duration per room")
	agentName := flag.String("agent", envOrDefault("AGENT_NAME", "cavos-voice-agent"), "Agent name")
	roomPrefix := flag.String("prefix", "loadtest", "Room name prefix")
	outputJSON := flag.String("output", "", "Save results to JSON file")
	verbose := flag.Bool("v", false, "Verbose output")
	flag.Parse()

	config := Config{
		NumRooms:       *numRooms,
		RampUpDuration: *rampUp,
		TestDuration:   *duration,
		AgentName:      *agentName,
		RoomPrefix:     *roomPrefix,
		LivekitURL:     envOrDefault("LIVEKIT_URL", ""),
		APIKey:         envOrDefault("LIVEKIT_API_KEY", ""),
		APISecret:      envOrDefault("LIVEKIT_API_SECRET", ""),
		Verbose:        *verbose,
	}

	if config.LivekitURL == "" || config.APIKey == "" || config.APISecret == "" {
		fmt.Println("ERROR: LIVEKIT_URL, LIVEKIT_API_KEY, LIVEKIT_API_SECRET must be set in .env")
		os.Exit(1)
	}

	config.HTTPUrl = strings.Replace(config.LivekitURL, "wss://", "https://", 1)

	result := runLoadTest(config)
	printReport(result)

	if *outputJSON != "" {
		if err := saveJSON(result, *outputJSON); err != nil {
			fmt.Printf("Failed to save JSON: %v\n", err)
		} else {
			fmt.Printf("Results saved to %s\n", *outputJSON)
		}
	}
}

func envOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}
