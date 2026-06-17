package app

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/adapter/anam"
	"github.com/cavos-io/rtp-agent/adapter/anthropic"
	"github.com/cavos-io/rtp-agent/adapter/assemblyai"
	"github.com/cavos-io/rtp-agent/adapter/asyncai"
	"github.com/cavos-io/rtp-agent/adapter/avatario"
	"github.com/cavos-io/rtp-agent/adapter/avatartalk"
	adapteraws "github.com/cavos-io/rtp-agent/adapter/aws"
	"github.com/cavos-io/rtp-agent/adapter/azure"
	"github.com/cavos-io/rtp-agent/adapter/baseten"
	"github.com/cavos-io/rtp-agent/adapter/bey"
	"github.com/cavos-io/rtp-agent/adapter/bithuman"
	"github.com/cavos-io/rtp-agent/adapter/browser"
	"github.com/cavos-io/rtp-agent/adapter/cambai"
	"github.com/cavos-io/rtp-agent/adapter/cartesia"
	"github.com/cavos-io/rtp-agent/adapter/cavos"
	"github.com/cavos-io/rtp-agent/adapter/cerebras"
	"github.com/cavos-io/rtp-agent/adapter/clova"
	"github.com/cavos-io/rtp-agent/adapter/deepgram"
	"github.com/cavos-io/rtp-agent/adapter/did"
	"github.com/cavos-io/rtp-agent/adapter/elevenlabs"
	"github.com/cavos-io/rtp-agent/adapter/fal"
	"github.com/cavos-io/rtp-agent/adapter/fireworksai"
	"github.com/cavos-io/rtp-agent/adapter/fishaudio"
	"github.com/cavos-io/rtp-agent/adapter/gladia"
	"github.com/cavos-io/rtp-agent/adapter/gnani"
	adaptergoogle "github.com/cavos-io/rtp-agent/adapter/google"
	"github.com/cavos-io/rtp-agent/adapter/gradium"
	"github.com/cavos-io/rtp-agent/adapter/groq"
	"github.com/cavos-io/rtp-agent/adapter/hamming"
	"github.com/cavos-io/rtp-agent/adapter/hedra"
	"github.com/cavos-io/rtp-agent/adapter/hume"
	"github.com/cavos-io/rtp-agent/adapter/inworld"
	"github.com/cavos-io/rtp-agent/adapter/keyframe"
	"github.com/cavos-io/rtp-agent/adapter/krisp"
	"github.com/cavos-io/rtp-agent/adapter/langchain"
	"github.com/cavos-io/rtp-agent/adapter/lemonslice"
	"github.com/cavos-io/rtp-agent/adapter/liveavatar"
	adapterlivekit "github.com/cavos-io/rtp-agent/adapter/livekit"
	"github.com/cavos-io/rtp-agent/adapter/lmnt"
	"github.com/cavos-io/rtp-agent/adapter/minimal"
	"github.com/cavos-io/rtp-agent/adapter/minimax"
	"github.com/cavos-io/rtp-agent/adapter/mistralai"
	"github.com/cavos-io/rtp-agent/adapter/murf"
	"github.com/cavos-io/rtp-agent/adapter/neuphonic"
	"github.com/cavos-io/rtp-agent/adapter/nltk"
	"github.com/cavos-io/rtp-agent/adapter/nvidia"
	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/adapter/perplexity"
	"github.com/cavos-io/rtp-agent/adapter/phonic"
	"github.com/cavos-io/rtp-agent/adapter/pipecat"
	"github.com/cavos-io/rtp-agent/adapter/resemble"
	"github.com/cavos-io/rtp-agent/adapter/respeecher"
	"github.com/cavos-io/rtp-agent/adapter/rime"
	"github.com/cavos-io/rtp-agent/adapter/rtzr"
	"github.com/cavos-io/rtp-agent/adapter/runway"
	"github.com/cavos-io/rtp-agent/adapter/sarvam"
	"github.com/cavos-io/rtp-agent/adapter/silero"
	"github.com/cavos-io/rtp-agent/adapter/simli"
	"github.com/cavos-io/rtp-agent/adapter/simplismart"
	"github.com/cavos-io/rtp-agent/adapter/smallestai"
	"github.com/cavos-io/rtp-agent/adapter/soniox"
	"github.com/cavos-io/rtp-agent/adapter/speechify"
	"github.com/cavos-io/rtp-agent/adapter/speechmatics"
	"github.com/cavos-io/rtp-agent/adapter/spitch"
	"github.com/cavos-io/rtp-agent/adapter/tavus"
	"github.com/cavos-io/rtp-agent/adapter/telnyx"
	"github.com/cavos-io/rtp-agent/adapter/ten"
	"github.com/cavos-io/rtp-agent/adapter/trugen"
	"github.com/cavos-io/rtp-agent/adapter/ultravox"
	"github.com/cavos-io/rtp-agent/adapter/upliftai"
	"github.com/cavos-io/rtp-agent/adapter/xai"
	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/beta/workflows"
	"github.com/cavos-io/rtp-agent/core/evals"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/interface/worker"
	workeragora "github.com/cavos-io/rtp-agent/interface/worker/agora"
	logutil "github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/plugin"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/gorilla/websocket"
	"github.com/livekit/protocol/livekit"
	livekitlogger "github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/vmihailenco/msgpack/v5"
)

type fakeAppAgoraChannelClient struct {
	joinOptions worker.AgoraOptions
	handler     workeragora.EventHandler
	audio       workeragora.AudioHandler
	joinedCh    chan struct{}
	publishedCh chan workeragora.PCMFrame
	joinEvent   *workeragora.Event
	leaveEvent  *workeragora.Event
	joinErr     error
	leaveErr    error
	published   []workeragora.PCMFrame
	joined      bool
	left        bool
}

type fakeAppSessionAssistant struct {
	audioCh  chan *model.AudioFrame
	startCtx context.Context
	publish  func(ctx context.Context, frame *model.AudioFrame) error
}

func (f *fakeAppSessionAssistant) Start(ctx context.Context, s *agent.AgentSession) error {
	f.startCtx = ctx
	return nil
}

func (f *fakeAppSessionAssistant) OnAudioFrame(ctx context.Context, frame *model.AudioFrame) {
	copied := *frame
	copied.Data = append([]byte(nil), frame.Data...)
	if f.audioCh != nil {
		select {
		case f.audioCh <- &copied:
		default:
		}
	}
}

func (f *fakeAppSessionAssistant) SetPublishAudio(publish func(ctx context.Context, frame *model.AudioFrame) error) {
	f.publish = publish
}

func (f *fakeAppAgoraChannelClient) Join(ctx context.Context, opts worker.AgoraOptions, handler workeragora.EventHandler, audio workeragora.AudioHandler) error {
	f.joinOptions = opts
	f.handler = handler
	f.audio = audio
	if f.joinEvent != nil && f.handler != nil {
		f.handler(*f.joinEvent)
	}
	if f.joinErr != nil {
		return f.joinErr
	}
	f.joined = true
	if f.joinedCh != nil {
		select {
		case f.joinedCh <- struct{}{}:
		default:
		}
	}
	return nil
}

func (f *fakeAppAgoraChannelClient) Leave(ctx context.Context) error {
	if f.leaveErr != nil {
		return f.leaveErr
	}
	if f.leaveEvent != nil && f.handler != nil {
		f.handler(*f.leaveEvent)
	}
	f.left = true
	return nil
}

func (f *fakeAppAgoraChannelClient) PublishPCM(ctx context.Context, frame workeragora.PCMFrame) error {
	copied := frame
	copied.Data = append([]byte(nil), frame.Data...)
	f.published = append(f.published, copied)
	if f.publishedCh != nil {
		select {
		case f.publishedCh <- copied:
		default:
		}
	}
	return nil
}

func (f *fakeAppAgoraChannelClient) emitAudio(frame *model.AudioFrame) {
	f.audio(frame)
}

func (f *fakeAppAgoraChannelClient) emit(event workeragora.Event) {
	f.handler(event)
}

func TestAppRegistersReferencePluginMetadataBatch(t *testing.T) {
	expected := map[string]struct {
		title   string
		version string
	}{
		anam.PluginPackage:       {title: anam.PluginTitle, version: anam.PluginVersion},
		anthropic.PluginPackage:  {title: anthropic.PluginTitle, version: anthropic.PluginVersion},
		assemblyai.PluginPackage: {title: assemblyai.PluginTitle, version: assemblyai.PluginVersion},
		asyncai.PluginPackage:    {title: asyncai.PluginTitle, version: asyncai.PluginVersion},
		avatario.PluginPackage:   {title: avatario.PluginTitle, version: avatario.PluginVersion},
		avatartalk.PluginPackage: {title: avatartalk.PluginTitle, version: avatartalk.PluginVersion},
		adapteraws.PluginPackage: {title: adapteraws.PluginTitle, version: adapteraws.PluginVersion},
		azure.PluginPackage:      {title: azure.PluginTitle, version: azure.PluginVersion},
		baseten.PluginPackage:    {title: baseten.PluginTitle, version: baseten.PluginVersion},
		bey.PluginPackage:        {title: bey.PluginTitle, version: bey.PluginVersion},
		bithuman.PluginPackage:   {title: bithuman.PluginTitle, version: bithuman.PluginVersion},
		browser.PluginPackage:    {title: browser.PluginTitle, version: browser.PluginVersion},
		cambai.PluginPackage:     {title: cambai.PluginTitle, version: cambai.PluginVersion},
		cartesia.PluginPackage:   {title: cartesia.PluginTitle, version: cartesia.PluginVersion},
		cavos.PluginPackage:      {title: cavos.PluginTitle, version: cavos.PluginVersion},
		cerebras.PluginPackage:   {title: cerebras.PluginTitle, version: cerebras.PluginVersion},
		clova.PluginPackage:      {title: clova.PluginTitle, version: clova.PluginVersion},
		deepgram.PluginPackage:   {title: deepgram.PluginTitle, version: deepgram.PluginVersion},
		did.PluginPackage:        {title: did.PluginTitle, version: did.PluginVersion},
		elevenlabs.PluginPackage: {title: elevenlabs.PluginTitle, version: elevenlabs.PluginVersion},
		fal.PluginPackage:        {title: fal.PluginTitle, version: fal.PluginVersion},
		fireworksai.PluginPackage: {
			title:   fireworksai.PluginTitle,
			version: fireworksai.PluginVersion,
		},
		fishaudio.PluginPackage: {title: fishaudio.PluginTitle, version: fishaudio.PluginVersion},
		gladia.PluginPackage:    {title: gladia.PluginTitle, version: gladia.PluginVersion},
		gnani.PluginPackage:     {title: gnani.PluginTitle, version: gnani.PluginVersion},
		adaptergoogle.PluginPackage: {
			title:   adaptergoogle.PluginTitle,
			version: adaptergoogle.PluginVersion,
		},
		gradium.PluginPackage:    {title: gradium.PluginTitle, version: gradium.PluginVersion},
		groq.PluginPackage:       {title: groq.PluginTitle, version: groq.PluginVersion},
		hamming.PluginPackage:    {title: hamming.PluginTitle, version: hamming.PluginVersion},
		hedra.PluginPackage:      {title: hedra.PluginTitle, version: hedra.PluginVersion},
		hume.PluginPackage:       {title: hume.PluginTitle, version: hume.PluginVersion},
		inworld.PluginPackage:    {title: inworld.PluginTitle, version: inworld.PluginVersion},
		keyframe.PluginPackage:   {title: keyframe.PluginTitle, version: keyframe.PluginVersion},
		krisp.PluginPackage:      {title: krisp.PluginTitle, version: krisp.PluginVersion},
		langchain.PluginPackage:  {title: langchain.PluginTitle, version: langchain.PluginVersion},
		lemonslice.PluginPackage: {title: lemonslice.PluginTitle, version: lemonslice.PluginVersion},
		liveavatar.PluginPackage: {title: liveavatar.PluginTitle, version: liveavatar.PluginVersion},
		lmnt.PluginPackage:       {title: lmnt.PluginTitle, version: lmnt.PluginVersion},
		minimal.PluginPackage:    {title: minimal.PluginTitle, version: minimal.PluginVersion},
		minimax.PluginPackage:    {title: minimax.PluginTitle, version: minimax.PluginVersion},
		mistralai.PluginPackage:  {title: mistralai.PluginTitle, version: mistralai.PluginVersion},
		murf.PluginPackage:       {title: murf.PluginTitle, version: murf.PluginVersion},
		neuphonic.PluginPackage:  {title: neuphonic.PluginTitle, version: neuphonic.PluginVersion},
		nltk.PluginPackage:       {title: nltk.PluginTitle, version: nltk.PluginVersion},
		nvidia.PluginPackage:     {title: nvidia.PluginTitle, version: nvidia.PluginVersion},
		openai.PluginPackage:     {title: openai.PluginTitle, version: openai.PluginVersion},
		perplexity.PluginPackage: {title: perplexity.PluginTitle, version: perplexity.PluginVersion},
		phonic.PluginPackage:     {title: phonic.PluginTitle, version: phonic.PluginVersion},
		resemble.PluginPackage:   {title: resemble.PluginTitle, version: resemble.PluginVersion},
		respeecher.PluginPackage: {title: respeecher.PluginTitle, version: respeecher.PluginVersion},
		rime.PluginPackage:       {title: rime.PluginTitle, version: rime.PluginVersion},
		rtzr.PluginPackage:       {title: rtzr.PluginTitle, version: rtzr.PluginVersion},
		runway.PluginPackage:     {title: runway.PluginTitle, version: runway.PluginVersion},
		sarvam.PluginPackage:     {title: sarvam.PluginTitle, version: sarvam.PluginVersion},
		simli.PluginPackage:      {title: simli.PluginTitle, version: simli.PluginVersion},
		simplismart.PluginPackage: {
			title:   simplismart.PluginTitle,
			version: simplismart.PluginVersion,
		},
		smallestai.PluginPackage:     {title: smallestai.PluginTitle, version: smallestai.PluginVersion},
		soniox.PluginPackage:         {title: soniox.PluginTitle, version: soniox.PluginVersion},
		speechify.PluginPackage:      {title: speechify.PluginTitle, version: speechify.PluginVersion},
		speechmatics.PluginPackage:   {title: speechmatics.PluginTitle, version: speechmatics.PluginVersion},
		spitch.PluginPackage:         {title: spitch.PluginTitle, version: spitch.PluginVersion},
		tavus.PluginPackage:          {title: tavus.PluginTitle, version: tavus.PluginVersion},
		telnyx.PluginPackage:         {title: telnyx.PluginTitle, version: telnyx.PluginVersion},
		ten.PluginPackage:            {title: ten.PluginTitle, version: ten.PluginVersion},
		trugen.PluginPackage:         {title: trugen.PluginTitle, version: trugen.PluginVersion},
		adapterlivekit.PluginPackage: {title: adapterlivekit.PluginTitle, version: adapterlivekit.PluginVersion},
		ultravox.PluginPackage:       {title: ultravox.PluginTitle, version: ultravox.PluginVersion},
		upliftai.PluginPackage:       {title: upliftai.PluginTitle, version: upliftai.PluginVersion},
		xai.PluginPackage:            {title: xai.PluginTitle, version: xai.PluginVersion},
	}
	if expected[adapterlivekit.PluginPackage].title != "rtp-agent.plugins.livekit" {
		t.Fatalf("livekit plugin title = %q, want rtp-agent.plugins.livekit", expected[adapterlivekit.PluginPackage].title)
	}
	if expected[adapterlivekit.PluginPackage].version != adapterlivekit.PluginVersion {
		t.Fatalf("livekit plugin version = %q, want %q", expected[adapterlivekit.PluginPackage].version, adapterlivekit.PluginVersion)
	}

	for _, registered := range plugin.RegisteredPlugins() {
		want, ok := expected[registered.Package()]
		if !ok {
			continue
		}
		if registered.Title() != want.title {
			t.Fatalf("%s title = %q, want %q", registered.Package(), registered.Title(), want.title)
		}
		if registered.Version() != want.version {
			t.Fatalf("%s version = %q, want %q", registered.Package(), registered.Version(), want.version)
		}
		delete(expected, registered.Package())
	}
	if len(expected) > 0 {
		missing := make([]string, 0, len(expected))
		for packageName := range expected {
			missing = append(missing, packageName)
		}
		t.Fatalf("plugin metadata was not registered for packages: %s", strings.Join(missing, ", "))
	}
}

func TestAppRegistersBrowserPluginDownloader(t *testing.T) {
	for _, registered := range plugin.RegisteredPlugins() {
		if registered.Package() != browser.PluginPackage {
			continue
		}
		if registered.Title() != browser.PluginTitle {
			t.Fatalf("plugin title = %q, want %q", registered.Title(), browser.PluginTitle)
		}
		if registered.Version() != browser.PluginVersion {
			t.Fatalf("plugin version = %q, want %q", registered.Version(), browser.PluginVersion)
		}
		if err := registered.DownloadFiles(); err != nil {
			t.Fatalf("DownloadFiles() error = %v, want nil for Go PageActions adapter", err)
		}
		return
	}
	t.Fatal("Browser plugin downloader was not registered")
}

func TestAppRegistersNltkPluginDownloader(t *testing.T) {
	for _, registered := range plugin.RegisteredPlugins() {
		if registered.Package() != nltk.PluginPackage {
			continue
		}
		if registered.Title() != nltk.PluginTitle {
			t.Fatalf("plugin title = %q, want %q", registered.Title(), nltk.PluginTitle)
		}
		if registered.Version() != nltk.PluginVersion {
			t.Fatalf("plugin version = %q, want %q", registered.Version(), nltk.PluginVersion)
		}
		if err := registered.DownloadFiles(); err != nil {
			t.Fatalf("DownloadFiles() error = %v, want nil for Go-native tokenizer", err)
		}
		return
	}
	t.Fatal("NLTK plugin downloader was not registered")
}

func TestAppRegistersClovaPluginDownloader(t *testing.T) {
	for _, registered := range plugin.RegisteredPlugins() {
		if registered.Package() != clova.PluginPackage {
			continue
		}
		if registered.Title() != clova.PluginTitle {
			t.Fatalf("plugin title = %q, want %q", registered.Title(), clova.PluginTitle)
		}
		if registered.Version() != clova.PluginVersion {
			t.Fatalf("plugin version = %q, want %q", registered.Version(), clova.PluginVersion)
		}
		if err := registered.DownloadFiles(); err != nil {
			t.Fatalf("DownloadFiles() error = %v, want nil reference no-op", err)
		}
		return
	}
	t.Fatal("Clova plugin downloader was not registered")
}

func TestAppRegistersSLNGPluginMetadata(t *testing.T) {
	for _, registered := range plugin.RegisteredPlugins() {
		if registered.Package() != "rtp-agent.plugins.slng" {
			continue
		}
		if registered.Title() != "rtp-agent.plugins.slng" {
			t.Fatalf("plugin title = %q, want rtp-agent.plugins.slng", registered.Title())
		}
		if registered.Version() != "1.5.15" {
			t.Fatalf("plugin version = %q, want reference version", registered.Version())
		}
		return
	}
	t.Fatal("SLNG plugin metadata was not registered")
}

func TestAppRegistersSileroPluginDownloader(t *testing.T) {
	for _, registered := range plugin.RegisteredPlugins() {
		if registered.Package() != silero.PluginPackage {
			continue
		}
		if registered.Title() != silero.PluginTitle {
			t.Fatalf("plugin title = %q, want %q", registered.Title(), silero.PluginTitle)
		}
		if registered.Version() != silero.PluginVersion {
			t.Fatalf("plugin version = %q, want %q", registered.Version(), silero.PluginVersion)
		}
		return
	}
	t.Fatal("Silero plugin downloader was not registered")
}

func TestAppRegistersPipecatPluginDownloader(t *testing.T) {
	for _, registered := range plugin.RegisteredPlugins() {
		if registered.Package() != pipecat.PluginPackage {
			continue
		}
		if registered.Title() != pipecat.PluginTitle {
			t.Fatalf("plugin title = %q, want %q", registered.Title(), pipecat.PluginTitle)
		}
		if registered.Version() != pipecat.PluginVersion {
			t.Fatalf("plugin version = %q, want %q", registered.Version(), pipecat.PluginVersion)
		}
		return
	}
	t.Fatal("Pipecat plugin downloader was not registered")
}

func TestAppRegistersTenPluginDownloader(t *testing.T) {
	for _, registered := range plugin.RegisteredPlugins() {
		if registered.Package() != ten.PluginPackage {
			continue
		}
		if registered.Title() != ten.PluginTitle {
			t.Fatalf("plugin title = %q, want %q", registered.Title(), ten.PluginTitle)
		}
		if registered.Version() != ten.PluginVersion {
			t.Fatalf("plugin version = %q, want %q", registered.Version(), ten.PluginVersion)
		}
		return
	}
	t.Fatal("TEN plugin downloader was not registered")
}

func TestNewAppInstallsConfiguredLogger(t *testing.T) {
	previous := logutil.Logger
	t.Cleanup(func() { logutil.Logger = previous })

	recorder := &appRecordingLogger{}
	app, err := NewApp(AppConfig{Logger: recorder})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app == nil {
		t.Fatal("NewApp() returned nil app")
	}
	if logutil.Logger != recorder {
		t.Fatal("NewApp() did not install configured logger")
	}
}

func TestNewAppUsesConfiguredMetricsRegistry(t *testing.T) {
	registry := telemetry.NewMetricRegistry()
	app, err := NewApp(AppConfig{
		WorkerOptions:   worker.WorkerOptions{AgentName: "metrics-agent"},
		MetricsRegistry: registry,
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	want := registry.GetUsageCollector(telemetry.MetricLabels{AgentName: "metrics-agent"})
	if app.Session.MetricsCollector != want {
		t.Fatal("Session MetricsCollector was not allocated from configured registry")
	}
}

func TestNewAppUsesLiveKitAgentNameFromEnv(t *testing.T) {
	t.Setenv("LIVEKIT_AGENT_NAME", "env-app-agent")
	registry := telemetry.NewMetricRegistry()

	app, err := NewApp(AppConfig{MetricsRegistry: registry})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}

	if app.Server.Options.AgentName != "env-app-agent" {
		t.Fatalf("server AgentName = %q, want LIVEKIT_AGENT_NAME", app.Server.Options.AgentName)
	}
	if !app.Server.Options.AgentNameIsEnv {
		t.Fatal("server AgentNameIsEnv = false, want true")
	}
	want := registry.GetUsageCollector(telemetry.MetricLabels{AgentName: "env-app-agent"})
	if app.Session.MetricsCollector != want {
		t.Fatal("Session MetricsCollector was not allocated with LIVEKIT_AGENT_NAME")
	}
}

func TestDefaultConfigFromEnvConfiguresTelemetryLogs(t *testing.T) {
	t.Setenv("RTP_AGENT_OTLP_LOGS_ENDPOINT", "otel.example:4318")
	t.Setenv("RTP_AGENT_OTLP_LOGS_HEADERS", "Authorization=Bearer token,X-Scope=agent")

	cfg := DefaultConfigFromEnv()

	if cfg.TelemetryLogsEndpoint != "otel.example:4318" {
		t.Fatalf("TelemetryLogsEndpoint = %q, want otel.example:4318", cfg.TelemetryLogsEndpoint)
	}
	if got := cfg.TelemetryLogsHeaders["Authorization"]; got != "Bearer token" {
		t.Fatalf("TelemetryLogsHeaders[Authorization] = %q, want Bearer token", got)
	}
	if got := cfg.TelemetryLogsHeaders["X-Scope"]; got != "agent" {
		t.Fatalf("TelemetryLogsHeaders[X-Scope] = %q, want agent", got)
	}
}

func TestDefaultConfigFromEnvDefaultsWorkerTransportToLiveKit(t *testing.T) {
	t.Setenv("RTP_AGENT_TRANSPORT", "")

	cfg := DefaultConfigFromEnv()

	if cfg.WorkerOptions.Transport != worker.WorkerTransportLiveKit {
		t.Fatalf("Transport = %q, want %q", cfg.WorkerOptions.Transport, worker.WorkerTransportLiveKit)
	}
}

func TestDefaultConfigFromEnvConfiguresAgoraWorkerTransport(t *testing.T) {
	t.Setenv("RTP_AGENT_TRANSPORT", " agora ")
	t.Setenv("AGORA_APP_ID", " agora-app ")
	t.Setenv("AGORA_APP_CERTIFICATE", " agora-cert ")
	t.Setenv("AGORA_CHANNEL", " support-room ")
	t.Setenv("AGORA_UID", " agent-42 ")
	t.Setenv("AGORA_TOKEN", " agora-token ")

	cfg := DefaultConfigFromEnv()

	if cfg.WorkerOptions.Transport != worker.WorkerTransportAgora {
		t.Fatalf("Transport = %q, want %q", cfg.WorkerOptions.Transport, worker.WorkerTransportAgora)
	}
	for name, value := range map[string]string{
		"AppID":          cfg.WorkerOptions.Agora.AppID,
		"AppCertificate": cfg.WorkerOptions.Agora.AppCertificate,
		"Channel":        cfg.WorkerOptions.Agora.Channel,
		"UID":            cfg.WorkerOptions.Agora.UID,
		"Token":          cfg.WorkerOptions.Agora.Token,
	} {
		if strings.TrimSpace(value) != value {
			t.Fatalf("Agora.%s = %q, want trimmed value", name, value)
		}
	}
	if cfg.WorkerOptions.Agora.AppID != "agora-app" {
		t.Fatalf("Agora.AppID = %q, want agora-app", cfg.WorkerOptions.Agora.AppID)
	}
	if cfg.WorkerOptions.Agora.AppCertificate != "agora-cert" {
		t.Fatalf("Agora.AppCertificate = %q, want agora-cert", cfg.WorkerOptions.Agora.AppCertificate)
	}
	if cfg.WorkerOptions.Agora.Channel != "support-room" {
		t.Fatalf("Agora.Channel = %q, want support-room", cfg.WorkerOptions.Agora.Channel)
	}
	if cfg.WorkerOptions.Agora.UID != "agent-42" {
		t.Fatalf("Agora.UID = %q, want agent-42", cfg.WorkerOptions.Agora.UID)
	}
	if cfg.WorkerOptions.Agora.Token != "agora-token" {
		t.Fatalf("Agora.Token = %q, want agora-token", cfg.WorkerOptions.Agora.Token)
	}
}

func TestAppRunUsesAgoraTransportWhenConfigured(t *testing.T) {
	client := &fakeAppAgoraChannelClient{joinedCh: make(chan struct{}, 1)}
	oldNewAgoraChannelClient := appNewAgoraChannelClient
	appNewAgoraChannelClient = func() (workeragora.ChannelClient, error) {
		return client, nil
	}
	t.Cleanup(func() {
		appNewAgoraChannelClient = oldNewAgoraChannelClient
	})

	rtpApp, err := NewApp(AppConfig{
		WorkerOptions: worker.WorkerOptions{
			Transport: worker.WorkerTransportAgora,
			Agora: worker.AgoraOptions{
				AppID:   "app",
				Channel: "support",
				UID:     "agent",
				Token:   "token",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- rtpApp.Server.Run(ctx)
	}()

	select {
	case <-client.joinedCh:
	case <-time.After(time.Second):
		t.Fatal("App.Run() did not join Agora channel")
	}
	if client.joinOptions.Channel != "support" {
		t.Fatalf("joined channel = %q, want support", client.joinOptions.Channel)
	}

	cancel()
	select {
	case err := <-doneCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Server.Run() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Server.Run() did not return after cancellation")
	}
	if !client.left {
		t.Fatal("Agora client left = false, want true after App.Run cancellation")
	}
}

func TestRunAgoraLogsConnectedTransportEvent(t *testing.T) {
	previousLogger := logutil.Logger
	recorder := &appRecordingLogger{entriesCh: make(chan appLogEntry, 8)}
	logutil.Logger = recorder
	t.Cleanup(func() {
		logutil.Logger = previousLogger
	})

	client := &fakeAppAgoraChannelClient{joinedCh: make(chan struct{}, 1)}
	oldNewAgoraChannelClient := appNewAgoraChannelClient
	appNewAgoraChannelClient = func() (workeragora.ChannelClient, error) {
		return client, nil
	}
	t.Cleanup(func() {
		appNewAgoraChannelClient = oldNewAgoraChannelClient
	})

	rtpApp := &App{
		Session: agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{}),
		Server: worker.NewAgentServer(worker.WorkerOptions{
			Agora: worker.AgoraOptions{
				AppID:   "app",
				Channel: "support",
				UID:     "agent",
				Token:   "token",
			},
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- rtpApp.runAgora(ctx)
	}()

	select {
	case <-client.joinedCh:
	case <-time.After(time.Second):
		t.Fatal("runAgora() did not join Agora channel")
	}
	client.emit(workeragora.Event{Kind: workeragora.EventConnected, Channel: "support", Reason: 42})

	select {
	case entry := <-recorder.entriesCh:
		if entry.msg != "agora transport connected" {
			t.Fatalf("log msg = %q, want agora transport connected", entry.msg)
		}
		if got := entry.value("channel"); got != "support" {
			t.Fatalf("log channel = %#v, want support", got)
		}
		if got := entry.value("reason"); got != 42 {
			t.Fatalf("log reason = %#v, want 42", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Agora connected log")
	}

	cancel()
	select {
	case err := <-doneCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runAgora() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runAgora() did not return after cancellation")
	}
}

func TestRunAgoraWaitsForDisconnectEventOnShutdown(t *testing.T) {
	previousLogger := logutil.Logger
	recorder := &appRecordingLogger{
		blockMsg:     "agora transport disconnected",
		blockStarted: make(chan struct{}, 1),
		unblock:      make(chan struct{}),
	}
	logutil.Logger = recorder
	t.Cleanup(func() {
		logutil.Logger = previousLogger
	})

	client := &fakeAppAgoraChannelClient{
		joinedCh: make(chan struct{}, 1),
		leaveEvent: &workeragora.Event{
			Kind:    workeragora.EventDisconnected,
			Channel: "support",
			Reason:  99,
		},
	}
	oldNewAgoraChannelClient := appNewAgoraChannelClient
	appNewAgoraChannelClient = func() (workeragora.ChannelClient, error) {
		return client, nil
	}
	t.Cleanup(func() {
		appNewAgoraChannelClient = oldNewAgoraChannelClient
	})

	rtpApp := &App{
		Session: agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{}),
		Server: worker.NewAgentServer(worker.WorkerOptions{
			Agora: worker.AgoraOptions{
				AppID:   "app",
				Channel: "support",
				UID:     "agent",
				Token:   "token",
			},
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- rtpApp.runAgora(ctx)
	}()

	select {
	case <-client.joinedCh:
	case <-time.After(time.Second):
		t.Fatal("runAgora() did not join Agora channel")
	}
	cancel()

	select {
	case err := <-doneCh:
		t.Fatalf("runAgora() returned before disconnect event observer drained: %v", err)
	case <-recorder.blockStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for disconnect event log")
	}
	select {
	case err := <-doneCh:
		t.Fatalf("runAgora() returned while disconnect event log was blocked: %v", err)
	default:
	}
	close(recorder.unblock)

	select {
	case err := <-doneCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runAgora() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runAgora() did not return after disconnect event observer drained")
	}
}

func TestRunAgoraLogsJoinErrorTransportEvent(t *testing.T) {
	previousLogger := logutil.Logger
	recorder := &appRecordingLogger{entriesCh: make(chan appLogEntry, 8)}
	logutil.Logger = recorder
	t.Cleanup(func() {
		logutil.Logger = previousLogger
	})

	joinErr := errors.New("join failed")
	eventErr := errors.New("sdk denied")
	client := &fakeAppAgoraChannelClient{
		joinErr: joinErr,
		joinEvent: &workeragora.Event{
			Kind:    workeragora.EventError,
			Channel: "support",
			Reason:  110,
			Err:     eventErr,
		},
	}
	oldNewAgoraChannelClient := appNewAgoraChannelClient
	appNewAgoraChannelClient = func() (workeragora.ChannelClient, error) {
		return client, nil
	}
	t.Cleanup(func() {
		appNewAgoraChannelClient = oldNewAgoraChannelClient
	})

	rtpApp := &App{
		Session: agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{}),
		Server: worker.NewAgentServer(worker.WorkerOptions{
			Agora: worker.AgoraOptions{
				AppID:   "app",
				Channel: "support",
				UID:     "agent",
				Token:   "token",
			},
		}),
	}

	err := rtpApp.runAgora(context.Background())
	if !errors.Is(err, joinErr) {
		t.Fatalf("runAgora() error = %v, want join failed", err)
	}
	select {
	case entry := <-recorder.entriesCh:
		if entry.msg != "agora transport event error" {
			t.Fatalf("log msg = %q, want agora transport event error", entry.msg)
		}
		if got := entry.value("channel"); got != "support" {
			t.Fatalf("log channel = %#v, want support", got)
		}
		if got := entry.value("reason"); got != 110 {
			t.Fatalf("log reason = %#v, want 110", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Agora join error log")
	}
}

func TestRunAgoraClosesTransportWhenJoinFails(t *testing.T) {
	joinErr := errors.New("join failed")
	client := &fakeAppAgoraChannelClient{joinErr: joinErr}
	oldNewAgoraChannelClient := appNewAgoraChannelClient
	appNewAgoraChannelClient = func() (workeragora.ChannelClient, error) {
		return client, nil
	}
	t.Cleanup(func() {
		appNewAgoraChannelClient = oldNewAgoraChannelClient
	})

	rtpApp := &App{
		Session: agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{}),
		Server: worker.NewAgentServer(worker.WorkerOptions{
			Agora: worker.AgoraOptions{
				AppID:   "app",
				Channel: "support",
				UID:     "agent",
				Token:   "token",
			},
		}),
	}

	err := rtpApp.runAgora(context.Background())
	if !errors.Is(err, joinErr) {
		t.Fatalf("runAgora() error = %v, want join failed", err)
	}
	if !client.left {
		t.Fatal("Agora client left = false, want true after join failure")
	}
}

func TestRunAgoraPublishesAssistantAudioToChannel(t *testing.T) {
	client := &fakeAppAgoraChannelClient{
		joinedCh:    make(chan struct{}, 1),
		publishedCh: make(chan workeragora.PCMFrame, 1),
	}
	oldNewAgoraChannelClient := appNewAgoraChannelClient
	appNewAgoraChannelClient = func() (workeragora.ChannelClient, error) {
		return client, nil
	}
	t.Cleanup(func() {
		appNewAgoraChannelClient = oldNewAgoraChannelClient
	})

	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	rtpApp := &App{
		Session: session,
		Server: worker.NewAgentServer(worker.WorkerOptions{
			Agora: worker.AgoraOptions{
				AppID:   "app",
				Channel: "support",
				UID:     "agent",
				Token:   "token",
			},
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- rtpApp.runAgora(ctx)
	}()

	select {
	case <-client.joinedCh:
	case <-time.After(time.Second):
		t.Fatal("runAgora() did not join Agora channel")
	}

	pipeline, ok := session.EnsureAssistant().(*agent.PipelineAgent)
	if !ok {
		t.Fatalf("session assistant = %T, want *agent.PipelineAgent", session.Assistant)
	}
	var publishAudio func(context.Context, *model.AudioFrame) error
	timeout := time.After(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for publishAudio == nil {
		publishAudio = pipeline.PublishAudio
		if publishAudio != nil {
			break
		}
		select {
		case <-timeout:
			t.Fatal("session assistant PublishAudio was not connected to Agora transport")
		case <-ticker.C:
		}
	}
	if err := publishAudio(context.Background(), &model.AudioFrame{
		Data:              make([]byte, 320),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 160,
	}); err != nil {
		t.Fatalf("PublishAudio() error = %v", err)
	}

	select {
	case frame := <-client.publishedCh:
		if len(frame.Data) != 320 {
			t.Fatalf("published data length = %d, want 320", len(frame.Data))
		}
		if frame.SampleRate != 16000 {
			t.Fatalf("published sample rate = %d, want 16000", frame.SampleRate)
		}
		if frame.Channels != 1 {
			t.Fatalf("published channels = %d, want 1", frame.Channels)
		}
		if frame.StartPTSMS != 0 {
			t.Fatalf("published StartPTSMS = %d, want 0", frame.StartPTSMS)
		}
	case <-time.After(time.Second):
		t.Fatal("assistant audio was not published to Agora")
	}

	cancel()
	select {
	case err := <-doneCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runAgora() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runAgora() did not return after cancellation")
	}
}

func TestRunAgoraForwardsChannelAudioToSession(t *testing.T) {
	client := &fakeAppAgoraChannelClient{joinedCh: make(chan struct{}, 1)}
	oldNewAgoraChannelClient := appNewAgoraChannelClient
	appNewAgoraChannelClient = func() (workeragora.ChannelClient, error) {
		return client, nil
	}
	t.Cleanup(func() {
		appNewAgoraChannelClient = oldNewAgoraChannelClient
	})

	assistant := &fakeAppSessionAssistant{audioCh: make(chan *model.AudioFrame, 1)}
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	session.Assistant = assistant
	rtpApp := &App{
		Session: session,
		Server: worker.NewAgentServer(worker.WorkerOptions{
			Agora: worker.AgoraOptions{
				AppID:   "app",
				Channel: "support",
				UID:     "agent",
			},
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- rtpApp.runAgora(ctx)
	}()

	select {
	case <-client.joinedCh:
	case <-time.After(time.Second):
		t.Fatal("runAgora() did not join Agora channel")
	}
	if client.audio == nil {
		t.Fatal("Agora client audio handler = nil, want session forwarding handler")
	}
	client.emitAudio(&model.AudioFrame{
		Data:              []byte{1, 2, 3, 4},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	})

	select {
	case frame := <-assistant.audioCh:
		if frame.SampleRate != 16000 {
			t.Fatalf("session frame sample rate = %d, want 16000", frame.SampleRate)
		}
		if frame.SamplesPerChannel != 2 {
			t.Fatalf("session frame samples per channel = %d, want 2", frame.SamplesPerChannel)
		}
	case <-time.After(time.Second):
		t.Fatal("session did not receive Agora audio frame")
	}

	cancel()
	select {
	case err := <-doneCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runAgora() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runAgora() did not return after cancellation")
	}
}

func TestRunAgoraStartsSessionWithWorkerContext(t *testing.T) {
	client := &fakeAppAgoraChannelClient{joinedCh: make(chan struct{}, 1)}
	oldNewAgoraChannelClient := appNewAgoraChannelClient
	appNewAgoraChannelClient = func() (workeragora.ChannelClient, error) {
		return client, nil
	}
	t.Cleanup(func() {
		appNewAgoraChannelClient = oldNewAgoraChannelClient
	})

	assistant := &fakeAppSessionAssistant{}
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	session.Assistant = assistant
	session.LLM = &fakeAppLLM{}
	rtpApp := &App{
		Session: session,
		Server: worker.NewAgentServer(worker.WorkerOptions{
			Agora: worker.AgoraOptions{
				AppID:   "app",
				Channel: "support",
				UID:     "agent",
				Token:   "token",
			},
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- rtpApp.runAgora(ctx)
	}()

	select {
	case <-client.joinedCh:
	case <-time.After(time.Second):
		t.Fatal("runAgora() did not join Agora channel")
	}
	if assistant.startCtx == nil {
		t.Fatal("assistant Start context = nil, want worker context")
	}
	cancel()
	select {
	case <-assistant.startCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("assistant Start context was not canceled when worker context canceled")
	}

	select {
	case err := <-doneCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runAgora() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runAgora() did not return after cancellation")
	}
}

func TestSplitEnvMapParsesTypedModelOptions(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_MODEL_OPTIONS", "auto_mode=true,chunk_length_schedule=[80,120,180],speed=1.1,voice=alpha")

	options := splitEnvMap("RTP_AGENT_TTS_MODEL_OPTIONS")

	if options["auto_mode"] != true {
		t.Fatalf("auto_mode = %#v, want true", options["auto_mode"])
	}
	if options["speed"] != float64(1.1) {
		t.Fatalf("speed = %#v, want 1.1", options["speed"])
	}
	if options["voice"] != "alpha" {
		t.Fatalf("voice = %#v, want alpha", options["voice"])
	}
	schedule, _ := options["chunk_length_schedule"].([]any)
	if len(schedule) != 3 || schedule[0] != float64(80) || schedule[1] != float64(120) || schedule[2] != float64(180) {
		t.Fatalf("chunk_length_schedule = %#v, want [80 120 180]", options["chunk_length_schedule"])
	}
}

func TestNewAppInitializesAndClosesTelemetryLogs(t *testing.T) {
	var initializedEndpoint string
	var initializedHeaders map[string]string
	var shutdownCalled bool
	oldInit := appInitLoggerProvider
	oldShutdown := appShutdownLoggerProvider
	appInitLoggerProvider = func(ctx context.Context, endpoint string, headers map[string]string) error {
		initializedEndpoint = endpoint
		initializedHeaders = headers
		return nil
	}
	appShutdownLoggerProvider = func(ctx context.Context) error {
		shutdownCalled = true
		return nil
	}
	t.Cleanup(func() {
		appInitLoggerProvider = oldInit
		appShutdownLoggerProvider = oldShutdown
	})

	app, err := NewApp(AppConfig{
		TelemetryLogsEndpoint: "otel.example:4318",
		TelemetryLogsHeaders:  map[string]string{"Authorization": "Bearer token"},
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if initializedEndpoint != "otel.example:4318" {
		t.Fatalf("initialized endpoint = %q, want otel.example:4318", initializedEndpoint)
	}
	if initializedHeaders["Authorization"] != "Bearer token" {
		t.Fatalf("initialized headers = %#v, want Authorization header", initializedHeaders)
	}
	if err := app.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !shutdownCalled {
		t.Fatal("Close() did not shut down telemetry log provider")
	}
}

func TestRunSessionUsesJobMetricLabels(t *testing.T) {
	registry := telemetry.NewMetricRegistry()
	app, err := NewApp(AppConfig{
		WorkerOptions:   worker.WorkerOptions{AgentName: "metrics-agent"},
		MetricsRegistry: registry,
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}

	jobCtx := worker.NewJobContext(&livekit.Job{
		Id:   "job_metrics",
		Room: &livekit.Room{Name: "metrics-room"},
	}, "", "", "")
	if err := app.runSession(jobCtx); err != nil {
		t.Fatalf("runSession() error = %v", err)
	}

	want := registry.GetUsageCollector(telemetry.MetricLabels{
		AgentName:           "metrics-agent",
		RoomName:            "metrics-room",
		ParticipantIdentity: "agent-job_metrics",
	})
	if app.Session.MetricsCollector != want {
		t.Fatal("Session MetricsCollector was not allocated from job metric labels")
	}
}

func TestDefaultConfigFromEnvSelectsOpenAIProviders(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_REALTIME_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_LLM_MODEL", "gpt-test")
	t.Setenv("RTP_AGENT_STT_MODEL", "gpt-transcribe-test")
	t.Setenv("RTP_AGENT_TTS_MODEL", "gpt-4o-mini-tts")
	t.Setenv("RTP_AGENT_TTS_VOICE", "alloy")
	t.Setenv("RTP_AGENT_REALTIME_MODEL", "gpt-realtime-test")

	cfg := DefaultConfigFromEnv()

	app, err := NewApp(cfg)
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := llm.Model(app.Session.LLM); got != "gpt-test" {
		t.Fatalf("LLM model = %q, want gpt-test", got)
	}
	if app.Session.STT == nil {
		t.Fatal("STT is nil")
	}
	if app.Session.TTS == nil {
		t.Fatal("TTS is nil")
	}
	if got := tts.Provider(app.Session.TTS); got != "api.openai.com" {
		t.Fatalf("TTS provider = %q, want api.openai.com", got)
	}
	if app.RealtimeModel == nil {
		t.Fatal("RealtimeModel is nil")
	}
	if got := llm.RealtimeModelName(app.RealtimeModel); got != "gpt-realtime-test" {
		t.Fatalf("Realtime model = %q, want gpt-realtime-test", got)
	}
	if _, ok := app.Session.Assistant.(*agent.MultimodalAgent); !ok {
		t.Fatalf("Session assistant = %T, want *agent.MultimodalAgent", app.Session.Assistant)
	}
}

func TestDefaultConfigFromEnvPreservesOpenAITTSExplicitZeroSpeed(t *testing.T) {
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"speech.audio.done\"}\n\n")
	}))
	t.Cleanup(server.Close)

	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", server.URL)
	t.Setenv("RTP_AGENT_TTS_SPEED", "0")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	stream, err := app.Session.TTS.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next error = %v, want EOF", err)
	}
	if !strings.Contains(string(body), `"speed":0`) {
		t.Fatalf("request body %s missing explicit zero speed", body)
	}
}

func TestDefaultConfigFromEnvSelectsPerplexityLLM(t *testing.T) {
	t.Setenv("PERPLEXITY_API_KEY", "test-perplexity-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "perplexity")
	t.Setenv("RTP_AGENT_LLM_MODEL", "sonar")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Model(app.Session.LLM); got != "sonar" {
		t.Fatalf("LLM model = %q, want sonar", got)
	}
}

func TestDefaultConfigFromEnvSelectsNebiusOpenAILLM(t *testing.T) {
	t.Setenv("NEBIUS_API_KEY", "test-nebius-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "nebius")
	t.Setenv("RTP_AGENT_LLM_MODEL", "custom-nebius-model")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Model(app.Session.LLM); got != "custom-nebius-model" {
		t.Fatalf("LLM model = %q, want custom-nebius-model", got)
	}
	if got := llm.Provider(app.Session.LLM); got != "api.studio.nebius.com" {
		t.Fatalf("LLM provider = %q, want api.studio.nebius.com", got)
	}
}

func TestDefaultConfigFromEnvSelectsLettaOpenAILLM(t *testing.T) {
	t.Setenv("LETTA_API_KEY", "test-letta-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "letta")
	t.Setenv("RTP_AGENT_LLM_MODEL", "agent-test")
	t.Setenv("RTP_AGENT_LLM_BASE_URL", "https://letta.example.test/v1/chat/completions")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Model(app.Session.LLM); got != "agent-test" {
		t.Fatalf("LLM model = %q, want agent-test", got)
	}
	if got := llm.Provider(app.Session.LLM); got != "letta.example.test" {
		t.Fatalf("LLM provider = %q, want letta.example.test", got)
	}
}

func TestDefaultConfigFromEnvSelectsDeepSeekOpenAILLM(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-deepseek-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "deepseek")
	t.Setenv("RTP_AGENT_LLM_MODEL", "deepseek-reasoner")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Model(app.Session.LLM); got != "deepseek-reasoner" {
		t.Fatalf("LLM model = %q, want deepseek-reasoner", got)
	}
	if got := llm.Provider(app.Session.LLM); got != "api.deepseek.com" {
		t.Fatalf("LLM provider = %q, want api.deepseek.com", got)
	}
}

func TestDefaultConfigFromEnvSelectsCometAPIOpenAILLM(t *testing.T) {
	t.Setenv("COMETAPI_API_KEY", "test-cometapi-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "cometapi")
	t.Setenv("RTP_AGENT_LLM_MODEL", "custom-comet-model")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Model(app.Session.LLM); got != "custom-comet-model" {
		t.Fatalf("LLM model = %q, want custom-comet-model", got)
	}
	if got := llm.Provider(app.Session.LLM); got != "api.cometapi.com" {
		t.Fatalf("LLM provider = %q, want api.cometapi.com", got)
	}
}

func TestDefaultConfigFromEnvSelectsOVHCloudOpenAILLM(t *testing.T) {
	t.Setenv("OVHCLOUD_API_KEY", "test-ovhcloud-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "ovhcloud")
	t.Setenv("RTP_AGENT_LLM_MODEL", "custom-ovhcloud-model")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Model(app.Session.LLM); got != "custom-ovhcloud-model" {
		t.Fatalf("LLM model = %q, want custom-ovhcloud-model", got)
	}
	if got := llm.Provider(app.Session.LLM); got != "oai.endpoints.kepler.ai.cloud.ovh.net" {
		t.Fatalf("LLM provider = %q, want oai.endpoints.kepler.ai.cloud.ovh.net", got)
	}
}

func TestDefaultConfigFromEnvSelectsOctoAIOpenAILLM(t *testing.T) {
	t.Setenv("OCTOAI_TOKEN", "test-octoai-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "octoai")
	t.Setenv("RTP_AGENT_LLM_MODEL", "custom-octoai-model")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Model(app.Session.LLM); got != "custom-octoai-model" {
		t.Fatalf("LLM model = %q, want custom-octoai-model", got)
	}
	if got := llm.Provider(app.Session.LLM); got != "text.octoai.run" {
		t.Fatalf("LLM provider = %q, want text.octoai.run", got)
	}
}

func TestDefaultConfigFromEnvSelectsSambaNovaOpenAILLM(t *testing.T) {
	t.Setenv("SAMBANOVA_API_KEY", "test-sambanova-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "sambanova")
	t.Setenv("RTP_AGENT_LLM_MODEL", "custom-sambanova-model")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Model(app.Session.LLM); got != "custom-sambanova-model" {
		t.Fatalf("LLM model = %q, want custom-sambanova-model", got)
	}
	if got := llm.Provider(app.Session.LLM); got != "api.sambanova.ai" {
		t.Fatalf("LLM provider = %q, want api.sambanova.ai", got)
	}
}

func TestDefaultConfigFromEnvSelectsOllamaOpenAILLM(t *testing.T) {
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "ollama")
	t.Setenv("RTP_AGENT_LLM_MODEL", "llama3.2")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Model(app.Session.LLM); got != "llama3.2" {
		t.Fatalf("LLM model = %q, want llama3.2", got)
	}
	if got := llm.Provider(app.Session.LLM); got != "localhost:11434" {
		t.Fatalf("LLM provider = %q, want localhost:11434", got)
	}
}

func TestDefaultConfigFromEnvSelectsOpenRouterOpenAILLM(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "test-openrouter-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "openrouter")
	t.Setenv("RTP_AGENT_LLM_MODEL", "openai/gpt-4o-mini")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Model(app.Session.LLM); got != "openai/gpt-4o-mini" {
		t.Fatalf("LLM model = %q, want openai/gpt-4o-mini", got)
	}
	if got := llm.Provider(app.Session.LLM); got != "openrouter.ai" {
		t.Fatalf("LLM provider = %q, want openrouter.ai", got)
	}
}

func TestDefaultConfigFromEnvSelectsTogetherOpenAILLM(t *testing.T) {
	t.Setenv("TOGETHER_API_KEY", "test-together-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "together")
	t.Setenv("RTP_AGENT_LLM_MODEL", "custom-together-model")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Model(app.Session.LLM); got != "custom-together-model" {
		t.Fatalf("LLM model = %q, want custom-together-model", got)
	}
	if got := llm.Provider(app.Session.LLM); got != "api.together.xyz" {
		t.Fatalf("LLM provider = %q, want api.together.xyz", got)
	}
}

func TestDefaultConfigFromEnvSelectsNvidiaLLM(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "test-nvidia-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "nvidia")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Label(app.Session.LLM); got != "nvidia.NvidiaLLM" {
		t.Fatalf("LLM label = %q, want nvidia.NvidiaLLM", got)
	}
}

func TestDefaultConfigFromEnvSelectsLangChainLLM(t *testing.T) {
	t.Setenv("LANGCHAIN_API_KEY", "test-langchain-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "langchain")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Label(app.Session.LLM); got != "langchain.LangchainLLM" {
		t.Fatalf("LLM label = %q, want langchain.LangchainLLM", got)
	}
}

func TestDefaultConfigFromEnvSelectsMinimalLLM(t *testing.T) {
	t.Setenv("MINIMAL_API_KEY", "test-minimal-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "minimal")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Label(app.Session.LLM); got != "minimal.MinimalLLM" {
		t.Fatalf("LLM label = %q, want minimal.MinimalLLM", got)
	}
}

func TestDefaultConfigFromEnvSelectsSimliLLM(t *testing.T) {
	t.Setenv("SIMLI_API_KEY", "test-simli-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "simli")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Label(app.Session.LLM); got != "simli.SimliLLM" {
		t.Fatalf("LLM label = %q, want simli.SimliLLM", got)
	}
}

func TestDefaultConfigFromEnvSelectsHedraLLM(t *testing.T) {
	t.Setenv("HEDRA_API_KEY", "test-hedra-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "hedra")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Label(app.Session.LLM); got != "hedra.HedraLLM" {
		t.Fatalf("LLM label = %q, want hedra.HedraLLM", got)
	}
}

func TestDefaultConfigFromEnvSelectsLemonSliceLLM(t *testing.T) {
	t.Setenv("LEMONSLICE_API_KEY", "test-lemonslice-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "lemonslice")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Label(app.Session.LLM); got != "lemonslice.LemonSliceLLM" {
		t.Fatalf("LLM label = %q, want lemonslice.LemonSliceLLM", got)
	}
}

func TestDefaultConfigFromEnvSelectsTrugenLLM(t *testing.T) {
	t.Setenv("TRUGEN_API_KEY", "test-trugen-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "trugen")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Label(app.Session.LLM); got != "trugen.TrugenLLM" {
		t.Fatalf("LLM label = %q, want trugen.TrugenLLM", got)
	}
}

func TestDefaultConfigFromEnvSelectsUpliftAIProviders(t *testing.T) {
	t.Setenv("UPLIFTAI_API_KEY", "test-upliftai-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "upliftai")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "upliftai")
	t.Setenv("RTP_AGENT_TTS_VOICE", "bright")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := llm.Label(app.Session.LLM); got != "upliftai.UpliftAILLM" {
		t.Fatalf("LLM label = %q, want upliftai.UpliftAILLM", got)
	}
	if got := app.Session.TTS.Label(); got != "upliftai.TTS" {
		t.Fatalf("TTS label = %q, want upliftai.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 22050 {
		t.Fatalf("TTS sample rate = %d, want reference sample rate 22050", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want reference streaming without aligned transcript", caps)
	}
	if _, err := app.Session.TTS.Stream(context.Background()); err == nil || !strings.Contains(err.Error(), "streaming tts not natively supported") {
		t.Fatalf("TTS Stream() error = %v, want explicit unsupported streaming error", err)
	}
}

func TestDefaultConfigFromEnvSelectsNvidiaTTS(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "test-nvidia-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "nvidia")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "nvidia.TTS" {
		t.Fatalf("TTS label = %q, want nvidia.TTS", got)
	}
	if got := tts.Model(app.Session.TTS); got != "Magpie-Multilingual.EN-US.Leo" {
		t.Fatalf("TTS model = %q, want reference default voice", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 16000 {
		t.Fatalf("TTS sample rate = %d, want reference sample rate 16000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want reference streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsNvidiaTTSWithReferenceOptions(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "test-nvidia-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "nvidia")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "localhost:50051")
	t.Setenv("RTP_AGENT_TTS_MODEL", "local-function")
	t.Setenv("RTP_AGENT_TTS_VOICE", "Magpie-Multilingual.ID-ID.Ayu")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "id-ID")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	nvidiaProvider, ok := app.Session.TTS.(*nvidia.NvidiaTTS)
	if !ok {
		t.Fatalf("TTS provider type = %T, want *nvidia.NvidiaTTS", app.Session.TTS)
	}
	state := reflect.ValueOf(nvidiaProvider).Elem()
	if got, want := state.FieldByName("server").String(), "localhost:50051"; got != want {
		t.Fatalf("server = %q, want %q", got, want)
	}
	if got, want := state.FieldByName("functionID").String(), "local-function"; got != want {
		t.Fatalf("functionID = %q, want %q", got, want)
	}
	if got, want := state.FieldByName("voice").String(), "Magpie-Multilingual.ID-ID.Ayu"; got != want {
		t.Fatalf("voice = %q, want %q", got, want)
	}
	if got, want := state.FieldByName("languageCode").String(), "id-ID"; got != want {
		t.Fatalf("languageCode = %q, want %q", got, want)
	}
}

func TestDefaultConfigFromEnvSelectsNvidiaTTSLocalRivaWithoutAPIKey(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "nvidia")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "localhost:50051")
	t.Setenv("RTP_AGENT_TTS_MODEL_OPTIONS", "use_ssl=false")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	nvidiaProvider, ok := app.Session.TTS.(*nvidia.NvidiaTTS)
	if !ok {
		t.Fatalf("TTS provider type = %T, want *nvidia.NvidiaTTS", app.Session.TTS)
	}
	useSSL := reflect.ValueOf(nvidiaProvider).Elem().FieldByName("useSSL").Bool()
	if useSSL {
		t.Fatal("useSSL = true, want false for local Riva")
	}
}

func TestDefaultConfigFromEnvSelectsUltravoxTTS(t *testing.T) {
	t.Setenv("ULTRAVOX_API_KEY", "test-ultravox-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "ultravox")
	t.Setenv("RTP_AGENT_TTS_VOICE", "alloy")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "ultravox.TTS" {
		t.Fatalf("TTS label = %q, want ultravox.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
	if caps := app.Session.TTS.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want non-streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsSileroVAD(t *testing.T) {
	t.Setenv("RTP_AGENT_VAD_PROVIDER", "silero")
	t.Setenv("RTP_AGENT_VAD_SAMPLE_RATE", "8000")
	t.Setenv("RTP_AGENT_VAD_MIN_SPEECH_DURATION", "0.08")
	t.Setenv("RTP_AGENT_VAD_MIN_SILENCE_DURATION", "0.2")
	t.Setenv("RTP_AGENT_VAD_PREFIX_PADDING_DURATION", "0.1")
	t.Setenv("RTP_AGENT_VAD_MAX_BUFFERED_SPEECH", "2.5")
	t.Setenv("RTP_AGENT_VAD_ACTIVATION_THRESHOLD", "0.7")
	t.Setenv("RTP_AGENT_VAD_DEACTIVATION_THRESHOLD", "0.4")
	t.Setenv("RTP_AGENT_VAD_UPDATE_INTERVAL", "0.064")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.VAD == nil {
		t.Fatal("Session VAD is nil")
	}
	if got := app.Session.VAD.Label(); got != "silero.VAD" {
		t.Fatalf("VAD label = %q, want silero.VAD", got)
	}
	if caps := app.Session.VAD.Capabilities(); caps.UpdateInterval != 0.064 {
		t.Fatalf("VAD capabilities = %+v, want update interval 0.064", caps)
	}
}

func TestDefaultConfigFromEnvSelectsTenVAD(t *testing.T) {
	t.Setenv("RTP_AGENT_VAD_PROVIDER", "ten")
	t.Setenv("RTP_AGENT_VAD_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_VAD_MIN_SPEECH_DURATION", "0.032")
	t.Setenv("RTP_AGENT_VAD_MIN_SILENCE_DURATION", "0.096")
	t.Setenv("RTP_AGENT_VAD_PREFIX_PADDING_DURATION", "0.048")
	t.Setenv("RTP_AGENT_VAD_MAX_BUFFERED_SPEECH", "2.5")
	t.Setenv("RTP_AGENT_VAD_ACTIVATION_THRESHOLD", "0.7")
	t.Setenv("RTP_AGENT_VAD_DEACTIVATION_THRESHOLD", "0.4")
	t.Setenv("RTP_AGENT_VAD_UPDATE_INTERVAL", "0.016")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.VAD == nil {
		t.Fatal("Session VAD is nil")
	}
	if got := app.Session.VAD.Label(); got != "ten.VAD" {
		t.Fatalf("VAD label = %q, want ten.VAD", got)
	}
	if caps := app.Session.VAD.Capabilities(); caps.UpdateInterval != 0.016 {
		t.Fatalf("VAD capabilities = %+v, want update interval 0.016", caps)
	}
}

func TestDefaultConfigFromEnvSelectsAssemblyAISTT(t *testing.T) {
	t.Setenv("ASSEMBLYAI_API_KEY", "test-assemblyai-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "assemblyai")
	t.Setenv("RTP_AGENT_STT_MODEL", "u3-pro")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "wss://streaming.eu.assemblyai.com/")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "8000")
	t.Setenv("RTP_AGENT_STT_SPEAKER_LABELS", "true")
	t.Setenv("RTP_AGENT_STT_MAX_SPEAKERS", "2")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.STT == nil {
		t.Fatal("Session STT is nil")
	}
	if got := app.Session.STT.Label(); got != "assemblyai.STT" {
		t.Fatalf("STT label = %q, want assemblyai.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Diarization {
		t.Fatalf("STT Capabilities().Diarization = false, want true")
	}
}

func TestDefaultConfigFromEnvSelectsOVHCloudOpenAISTT(t *testing.T) {
	t.Setenv("OVHCLOUD_API_KEY", "test-ovhcloud-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "ovhcloud")
	t.Setenv("RTP_AGENT_STT_MODEL", "custom-ovhcloud-stt")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.STT == nil {
		t.Fatal("Session STT is nil")
	}
	if got := stt.Model(app.Session.STT); got != "custom-ovhcloud-stt" {
		t.Fatalf("STT model = %q, want custom-ovhcloud-stt", got)
	}
	if got := stt.Provider(app.Session.STT); got != "oai.endpoints.kepler.ai.cloud.ovh.net" {
		t.Fatalf("STT provider = %q, want oai.endpoints.kepler.ai.cloud.ovh.net", got)
	}
}

func TestDefaultConfigFromEnvWrapsSTTWithMultiSpeakerAdapter(t *testing.T) {
	t.Setenv("ASSEMBLYAI_API_KEY", "test-assemblyai-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "assemblyai")
	t.Setenv("RTP_AGENT_STT_SPEAKER_LABELS", "true")
	t.Setenv("RTP_AGENT_STT_MULTI_SPEAKER", "true")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.STT == nil {
		t.Fatal("Session STT is nil")
	}
	wrapped, ok := app.Session.STT.(*stt.MultiSpeakerAdapter)
	if !ok {
		t.Fatalf("Session STT = %T, want *stt.MultiSpeakerAdapter", app.Session.STT)
	}
	if caps := wrapped.Capabilities(); !caps.Streaming || !caps.Diarization {
		t.Fatalf("wrapped STT capabilities = %+v, want streaming diarization", caps)
	}
}

func TestDefaultConfigFromEnvSelectsAsyncAITTS(t *testing.T) {
	t.Setenv("ASYNCAI_API_KEY", "test-asyncai-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "asyncai")
	t.Setenv("RTP_AGENT_TTS_MODEL", "async_test_model")
	t.Setenv("RTP_AGENT_TTS_VOICE", "voice-test")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://async.example/")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "pcm_mulaw")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "8000")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "asyncai.TTS" {
		t.Fatalf("TTS label = %q, want asyncai.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 8000 {
		t.Fatalf("TTS sample rate = %d, want 8000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming {
		t.Fatalf("TTS Capabilities().Streaming = false, want true")
	}
}

func TestDefaultConfigFromEnvSelectsCambaiTTS(t *testing.T) {
	t.Setenv("CAMB_API_KEY", "test-cambai-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "cambai")
	t.Setenv("RTP_AGENT_TTS_MODEL", "mars-pro")
	t.Setenv("RTP_AGENT_TTS_VOICE", "123")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en-us")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "pcm_s16le")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://cambai.example/apis")
	t.Setenv("RTP_AGENT_TTS_INSTRUCTIONS", "speak clearly")
	t.Setenv("RTP_AGENT_TTS_ENHANCE_NAMED_ENTITIES", "true")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "cambai.TTS" {
		t.Fatalf("TTS label = %q, want cambai.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 48000 {
		t.Fatalf("TTS sample rate = %d, want 48000", got)
	}
}

func TestDefaultConfigFromEnvSelectsElevenLabsSpeechProviders(t *testing.T) {
	t.Setenv("ELEVENLABS_API_KEY", "test-elevenlabs-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "elevenlabs")
	t.Setenv("RTP_AGENT_STT_MODEL", "scribe_v2_realtime")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://elevenlabs.example/v1")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_KEYTERMS_PROMPT", "alpha,beta")
	t.Setenv("RTP_AGENT_STT_VAD_THRESHOLD", "0.6")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "elevenlabs")
	t.Setenv("RTP_AGENT_TTS_MODEL", "eleven_turbo_v2_5")
	t.Setenv("RTP_AGENT_TTS_VOICE", "voice-test")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "pcm_24000")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://elevenlabs.example/v1")
	t.Setenv("RTP_AGENT_TTS_ENABLE_SSML_PARSING", "true")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "elevenlabs.STT" {
		t.Fatalf("STT label = %q, want elevenlabs.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || caps.AlignedTranscript != "" {
		t.Fatalf("STT capabilities = %+v, want streaming without timestamps", caps)
	}
	if got := app.Session.TTS.Label(); got != "elevenlabs.TTS" {
		t.Fatalf("TTS label = %q, want elevenlabs.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || !caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming aligned transcript", caps)
	}
}

func TestTTSRuntimeDefaultsUsePCMCompatibleProviderFormats(t *testing.T) {
	sampleRate := 8000
	tests := []struct {
		name                  string
		provider              string
		cfg                   AppConfig
		fieldName             string
		want                  string
		wantSampleRate        int
		wantOutputFormatQuery string
	}{
		{
			name:      "openai",
			provider:  providerOpenAI,
			cfg:       AppConfig{OpenAIAPIKey: "test-openai-key"},
			fieldName: "responseFormat",
			want:      "pcm",
		},
		{
			name:     "elevenlabs",
			provider: providerElevenLabs,
			cfg: AppConfig{
				ElevenLabsAPIKey: "test-elevenlabs-key",
				TTSBaseURL:       "https://eleven.example/v1",
				TTSSampleRate:    &sampleRate,
			},
			fieldName:             "encoding",
			want:                  "pcm_8000",
			wantSampleRate:        sampleRate,
			wantOutputFormatQuery: "pcm_8000",
		},
		{
			name:      "mistralai",
			provider:  providerMistralAI,
			cfg:       AppConfig{MistralAPIKey: "test-mistral-key"},
			fieldName: "responseFormat",
			want:      "pcm",
		},
		{
			name:      "fishaudio",
			provider:  providerFishAudio,
			cfg:       AppConfig{FishAudioAPIKey: "test-fish-key"},
			fieldName: "outputFormat",
			want:      "pcm",
		},
		{
			name:      "hume",
			provider:  providerHume,
			cfg:       AppConfig{HumeAPIKey: "test-hume-key"},
			fieldName: "audioFormat",
			want:      "pcm",
		},
		{
			name:      "lmnt",
			provider:  providerLMNT,
			cfg:       AppConfig{LMNTAPIKey: "test-lmnt-key"},
			fieldName: "format",
			want:      "raw",
		},
		{
			name:      "speechify",
			provider:  providerSpeechify,
			cfg:       AppConfig{SpeechifyAPIKey: "test-speechify-key"},
			fieldName: "encoding",
			want:      "wav_48000",
		},
		{
			name:      "spitch",
			provider:  providerSpitch,
			cfg:       AppConfig{SpitchAPIKey: "test-spitch-key"},
			fieldName: "outputFormat",
			want:      "wav",
		},
		{
			name:      "minimax",
			provider:  providerMinimax,
			cfg:       AppConfig{MinimaxAPIKey: "test-minimax-key"},
			fieldName: "audioFormat",
			want:      "pcm",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := fallbackTTSFromProvider(tt.cfg, tt.provider)
			if err != nil {
				t.Fatalf("fallbackTTSFromProvider() error = %v", err)
			}
			if got := reflect.ValueOf(provider).Elem().FieldByName(tt.fieldName).String(); got != tt.want {
				t.Fatalf("%s = %q, want %q", tt.fieldName, got, tt.want)
			}

			configureCfg := tt.cfg
			configureCfg.TTSProvider = tt.provider
			configuredAgent := &agent.Agent{}
			if _, err := configureProviders(configureCfg, configuredAgent); err != nil {
				t.Fatalf("configureProviders() error = %v", err)
			}
			if configuredAgent.TTS == nil {
				t.Fatal("configureProviders() left TTS nil")
			}
			if got := reflect.ValueOf(configuredAgent.TTS).Elem().FieldByName(tt.fieldName).String(); got != tt.want {
				t.Fatalf("configured %s = %q, want %q", tt.fieldName, got, tt.want)
			}

			if tt.wantSampleRate != 0 {
				if got := provider.SampleRate(); got != tt.wantSampleRate {
					t.Fatalf("sample rate = %d, want %d", got, tt.wantSampleRate)
				}
				if got := configuredAgent.TTS.SampleRate(); got != tt.wantSampleRate {
					t.Fatalf("configured sample rate = %d, want %d", got, tt.wantSampleRate)
				}
			}
			if tt.wantOutputFormatQuery != "" {
				var gotOutputFormat string
				originalClient := http.DefaultClient
				http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(r *http.Request) (*http.Response, error) {
					gotOutputFormat = r.URL.Query().Get("output_format")
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
						Body:       io.NopCloser(strings.NewReader("\x00\x00\x01\x00")),
						Request:    r,
					}, nil
				})}
				t.Cleanup(func() { http.DefaultClient = originalClient })

				stream, err := provider.Synthesize(context.Background(), "hello")
				if err != nil {
					t.Fatalf("Synthesize() error = %v", err)
				}
				defer stream.Close()
				if _, err := stream.Next(); err != nil {
					t.Fatalf("stream.Next() error = %v", err)
				}
				if gotOutputFormat != tt.wantOutputFormatQuery {
					t.Fatalf("output_format = %q, want %q", gotOutputFormat, tt.wantOutputFormatQuery)
				}
			}
		})
	}
}

func TestDefaultConfigFromEnvSelectsCartesiaSpeechProviders(t *testing.T) {
	t.Setenv("CARTESIA_API_KEY", "test-cartesia-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "cartesia")
	t.Setenv("RTP_AGENT_STT_MODEL", "ink-2")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://cartesia.example")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_ENCODING", "pcm_s16le")
	t.Setenv("RTP_AGENT_STT_AUDIO_CHUNK_DURATION_MS", "120")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "cartesia")
	t.Setenv("RTP_AGENT_TTS_MODEL", "sonic-3")
	t.Setenv("RTP_AGENT_TTS_VOICE", "voice-test")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "pcm_s16le")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "44100")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://cartesia.example")
	t.Setenv("RTP_AGENT_TTS_API_VERSION", "2025-04-16")
	t.Setenv("RTP_AGENT_TTS_WORD_TIMESTAMPS", "false")
	t.Setenv("RTP_AGENT_TTS_VOICE_EMBEDDING", "0.1,0.2")
	t.Setenv("RTP_AGENT_TTS_SPEED", "1.1")
	t.Setenv("RTP_AGENT_TTS_EMOTION", "positivity")
	t.Setenv("RTP_AGENT_TTS_VOLUME", "0.8")
	t.Setenv("RTP_AGENT_TTS_PRONUNCIATION_DICT_ID", "dict-test")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "cartesia.STT" {
		t.Fatalf("STT label = %q, want cartesia.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults {
		t.Fatalf("STT capabilities = %+v, want streaming interim results", caps)
	}
	if got := app.Session.TTS.Label(); got != "cartesia.TTS" {
		t.Fatalf("TTS label = %q, want cartesia.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 44100 {
		t.Fatalf("TTS sample rate = %d, want 44100", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsClovaSpeechProviders(t *testing.T) {
	t.Setenv("CLOVA_STT_SECRET", "test-clova-stt-secret")
	t.Setenv("CLOVA_STT_INVOKE_URL", "https://clova.example/stt")
	t.Setenv("CLOVA_CLIENT_ID", "test-clova-client-id")
	t.Setenv("CLOVA_CLIENT_SECRET", "test-clova-client-secret")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "clova")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "ko")
	t.Setenv("RTP_AGENT_STT_VAD_THRESHOLD", "0.6")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "clova")
	t.Setenv("RTP_AGENT_TTS_VOICE", "nara")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "clova.STT" {
		t.Fatalf("STT label = %q, want clova.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); caps.Streaming || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want offline recognize only", caps)
	}
	if got := app.Session.TTS.Label(); got != "clova.TTS" {
		t.Fatalf("TTS label = %q, want clova.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
}

func TestDefaultConfigFromEnvSelectsDeepgramSpeechProviders(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "test-deepgram-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "deepgram")
	t.Setenv("RTP_AGENT_STT_MODEL", "nova-3")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://deepgram.example/v1/listen")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_NUMBER_OF_CHANNELS", "2")
	t.Setenv("RTP_AGENT_STT_INTERIM_RESULTS", "true")
	t.Setenv("RTP_AGENT_STT_PUNCTUATE", "true")
	t.Setenv("RTP_AGENT_STT_SMART_FORMAT", "true")
	t.Setenv("RTP_AGENT_STT_NO_DELAY", "true")
	t.Setenv("RTP_AGENT_STT_ENDPOINTING_MS", "75")
	t.Setenv("RTP_AGENT_STT_DIARIZATION", "true")
	t.Setenv("RTP_AGENT_STT_FILLER_WORDS", "true")
	t.Setenv("RTP_AGENT_STT_VAD_EVENTS", "true")
	t.Setenv("RTP_AGENT_STT_PROFANITY_FILTER", "true")
	t.Setenv("RTP_AGENT_STT_NUMERALS", "true")
	t.Setenv("RTP_AGENT_STT_MIP_OPT_OUT", "true")
	t.Setenv("RTP_AGENT_STT_KEYWORDS", "agent:1.5,voice")
	t.Setenv("RTP_AGENT_STT_KEYTERMS_PROMPT", "alpha,beta")
	t.Setenv("RTP_AGENT_STT_REDACT", "pci,ssn")
	t.Setenv("RTP_AGENT_STT_TAGS", "test,app")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "deepgram")
	t.Setenv("RTP_AGENT_TTS_MODEL", "aura-2-andromeda-en")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://deepgram.example/v1/speak")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "linear16")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "32000")
	t.Setenv("RTP_AGENT_TTS_MIP_OPT_OUT", "true")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "deepgram.STT" {
		t.Fatalf("STT label = %q, want deepgram.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.Diarization || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming diarization offline recognize", caps)
	}
	if got := app.Session.TTS.Label(); got != "deepgram.TTS" {
		t.Fatalf("TTS label = %q, want deepgram.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 32000 {
		t.Fatalf("TTS sample rate = %d, want 32000", got)
	}
}

func TestDefaultConfigFromEnvSelectsFishAudioTTS(t *testing.T) {
	t.Setenv("FISHAUDIO_API_KEY", "test-fishaudio-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "fishaudio")
	t.Setenv("RTP_AGENT_TTS_MODEL", "s2-pro")
	t.Setenv("RTP_AGENT_TTS_VOICE", "voice-test")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://fishaudio.example")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "opus")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "48000")
	t.Setenv("RTP_AGENT_TTS_LATENCY_MODE", "balanced")
	t.Setenv("RTP_AGENT_TTS_CHUNK_LENGTH", "120")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "fishaudio.TTS" {
		t.Fatalf("TTS label = %q, want fishaudio.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 48000 {
		t.Fatalf("TTS sample rate = %d, want 48000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming {
		t.Fatalf("TTS capabilities = %+v, want streaming", caps)
	}
}

func TestDefaultConfigFromEnvSelectsFalProviders(t *testing.T) {
	t.Setenv("FAL_KEY", "test-fal-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "fal")
	t.Setenv("RTP_AGENT_LLM_MODEL", "fal-ai/llm-test")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "fal")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_TASK", "translate")
	t.Setenv("RTP_AGENT_STT_CHUNK_LEVEL", "word")
	t.Setenv("RTP_AGENT_STT_VERSION", "3")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := llm.Provider(app.Session.LLM); got != "fal" {
		t.Fatalf("LLM provider = %q, want fal", got)
	}
	if got := llm.Model(app.Session.LLM); got != "fal-ai/llm-test" {
		t.Fatalf("LLM model = %q, want fal-ai/llm-test", got)
	}
	if got := app.Session.STT.Label(); got != "fal.STT" {
		t.Fatalf("STT label = %q, want fal.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); caps.Streaming || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want offline recognize only", caps)
	}
}

func TestDefaultConfigFromEnvSelectsFireworksProviders(t *testing.T) {
	t.Setenv("FIREWORKS_API_KEY", "test-fireworks-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "fireworks")
	t.Setenv("RTP_AGENT_LLM_MODEL", "accounts/fireworks/models/firefunction-test")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "fireworks")
	t.Setenv("RTP_AGENT_STT_MODEL", "whisper-test")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "wss://fireworks.example/v1")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_PROMPT", "domain prompt")
	t.Setenv("RTP_AGENT_STT_TEMPERATURE", "0.2")
	t.Setenv("RTP_AGENT_STT_SKIP_VAD", "true")
	t.Setenv("RTP_AGENT_STT_TEXT_TIMEOUT_SECONDS", "2.5")
	t.Setenv("RTP_AGENT_STT_TIMESTAMP_GRANULARITIES", "word,segment")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := llm.Provider(app.Session.LLM); got != "fireworks" {
		t.Fatalf("LLM provider = %q, want fireworks", got)
	}
	if got := llm.Model(app.Session.LLM); got != "accounts/fireworks/models/firefunction-test" {
		t.Fatalf("LLM model = %q, want accounts/fireworks/models/firefunction-test", got)
	}
	if got := app.Session.STT.Label(); got != "fireworks.STT" {
		t.Fatalf("STT label = %q, want fireworks.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming interim-only", caps)
	}
}

func TestDefaultConfigFromEnvSelectsGladiaSTT(t *testing.T) {
	t.Setenv("GLADIA_API_KEY", "test-gladia-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "gladia")
	t.Setenv("RTP_AGENT_STT_MODEL", "solaria-1")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://gladia.example/v2/live")
	t.Setenv("RTP_AGENT_STT_LANGUAGE_OPTIONS", "en,fr")
	t.Setenv("RTP_AGENT_STT_CODE_SWITCHING", "true")
	t.Setenv("RTP_AGENT_STT_INTERIM_RESULTS", "false")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_BIT_DEPTH", "16")
	t.Setenv("RTP_AGENT_STT_NUMBER_OF_CHANNELS", "1")
	t.Setenv("RTP_AGENT_STT_ENCODING", "wav/pcm")
	t.Setenv("RTP_AGENT_STT_ENDPOINTING_SECONDS", "0.1")
	t.Setenv("RTP_AGENT_STT_MAX_DURATION_WITHOUT_ENDPOINTING_SECONDS", "4")
	t.Setenv("RTP_AGENT_STT_REGION", "eu-west")
	t.Setenv("RTP_AGENT_STT_CUSTOM_VOCABULARY", "LiveKit,Agents")
	t.Setenv("RTP_AGENT_STT_CUSTOM_SPELLING", "livekit=live kit|live-kit")
	t.Setenv("RTP_AGENT_STT_TRANSLATION_TARGET_LANGUAGES", "es,de")
	t.Setenv("RTP_AGENT_STT_TRANSLATION_MODEL", "base")
	t.Setenv("RTP_AGENT_STT_TRANSLATION_MATCH_ORIGINAL_UTTERANCES", "true")
	t.Setenv("RTP_AGENT_STT_TRANSLATION_LIPSYNC", "true")
	t.Setenv("RTP_AGENT_STT_TRANSLATION_CONTEXT_ADAPTATION", "true")
	t.Setenv("RTP_AGENT_STT_TRANSLATION_CONTEXT", "support call")
	t.Setenv("RTP_AGENT_STT_TRANSLATION_INFORMAL", "true")
	t.Setenv("RTP_AGENT_STT_PRE_PROCESSING_AUDIO_ENHANCER", "true")
	t.Setenv("RTP_AGENT_STT_PRE_PROCESSING_SPEECH_THRESHOLD", "0.7")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.STT == nil {
		t.Fatal("Session STT is nil")
	}
	if got := app.Session.STT.Label(); got != "gladia.STT" {
		t.Fatalf("STT label = %q, want gladia.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || caps.InterimResults || caps.AlignedTranscript != "word" {
		t.Fatalf("STT capabilities = %+v, want streaming word-aligned without interim results", caps)
	}
}

func TestDefaultConfigFromEnvSelectsGnaniSpeechProviders(t *testing.T) {
	t.Setenv("GNANI_API_KEY", "test-gnani-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "gnani")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://gnani.example")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en-IN")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_ORGANIZATION_ID", "org-test")
	t.Setenv("RTP_AGENT_STT_USER_ID", "user-test")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "gnani")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://gnani.example")
	t.Setenv("RTP_AGENT_TTS_VOICE", "Karan")
	t.Setenv("RTP_AGENT_TTS_MODEL", "vachana-voice-v3")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "22050")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "linear_pcm")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "wav")
	t.Setenv("RTP_AGENT_TTS_NUMBER_OF_CHANNELS", "1")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_WIDTH", "2")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "hi")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "gnani.STT" {
		t.Fatalf("STT label = %q, want gnani.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming with offline recognize", caps)
	}
	if got := app.Session.TTS.Label(); got != "gnani.TTS" {
		t.Fatalf("TTS label = %q, want gnani.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 22050 {
		t.Fatalf("TTS sample rate = %d, want 22050", got)
	}
}

func TestDefaultConfigFromEnvSelectsGradiumProviders(t *testing.T) {
	t.Setenv("GRADIUM_API_KEY", "test-gradium-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "gradium")
	t.Setenv("RTP_AGENT_LLM_MODEL", "gradium-llm-test")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "gradium")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "wss://gradium.example/asr")
	t.Setenv("RTP_AGENT_STT_MODEL", "asr-test")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_TEMPERATURE", "0.3")
	t.Setenv("RTP_AGENT_STT_BUFFER_SIZE_SECONDS", "0.12")
	t.Setenv("RTP_AGENT_STT_VAD_BUCKET", "3")
	t.Setenv("RTP_AGENT_STT_VAD_FLUSH", "false")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "gradium")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "wss://gradium.example/tts")
	t.Setenv("RTP_AGENT_TTS_MODEL", "tts-test")
	t.Setenv("RTP_AGENT_TTS_VOICE", "voice-test")
	t.Setenv("RTP_AGENT_TTS_VOICE_ID", "voice-id-test")
	t.Setenv("RTP_AGENT_TTS_PRONUNCIATION_DICT_ID", "pronunciation-test")
	t.Setenv("RTP_AGENT_TTS_JSON_CONFIG", "style=clear,pace=1.2")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.STT.Label(); got != "gradium.STT" {
		t.Fatalf("STT label = %q, want gradium.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming interim-only", caps)
	}
	if got := app.Session.TTS.Label(); got != "gradium.TTS" {
		t.Fatalf("TTS label = %q, want gradium.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 48000 {
		t.Fatalf("TTS sample rate = %d, want 48000", got)
	}
}

func TestDefaultConfigFromEnvSelectsInworldProviders(t *testing.T) {
	t.Setenv("INWORLD_API_KEY", "test-inworld-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "inworld")
	t.Setenv("RTP_AGENT_LLM_MODEL", "inworld-llm-test")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "inworld")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://inworld.example/")
	t.Setenv("RTP_AGENT_STT_MODEL", "inworld-stt-test")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en-US")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_NUMBER_OF_CHANNELS", "1")
	t.Setenv("RTP_AGENT_STT_VOICE_PROFILE", "false")
	t.Setenv("RTP_AGENT_STT_VOICE_PROFILE_TOP_N", "2")
	t.Setenv("RTP_AGENT_STT_VAD_THRESHOLD", "0.4")
	t.Setenv("RTP_AGENT_STT_MIN_END_OF_TURN_SILENCE_WHEN_CONFIDENT", "180")
	t.Setenv("RTP_AGENT_STT_END_OF_TURN_CONFIDENCE_THRESHOLD", "0.45")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "inworld")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://inworld.example/")
	t.Setenv("RTP_AGENT_TTS_WEBSOCKET_URL", "wss://inworld.example/")
	t.Setenv("RTP_AGENT_TTS_MODEL", "inworld-tts-test")
	t.Setenv("RTP_AGENT_TTS_VOICE", "Ashley")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "PCM")
	t.Setenv("RTP_AGENT_TTS_BIT_RATE", "64000")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "22050")
	t.Setenv("RTP_AGENT_TTS_SPEAKING_RATE", "1.1")
	t.Setenv("RTP_AGENT_TTS_TEMPERATURE", "0.8")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en-US")
	t.Setenv("RTP_AGENT_TTS_TIMESTAMP_TYPE", "WORD")
	t.Setenv("RTP_AGENT_TTS_TEXT_NORMALIZATION", "true")
	t.Setenv("RTP_AGENT_TTS_DELIVERY_MODE", "STREAM")
	t.Setenv("RTP_AGENT_TTS_TIMESTAMP_TRANSPORT_STRATEGY", "ASYNC")
	t.Setenv("RTP_AGENT_TTS_BUFFER_CHAR_THRESHOLD", "90")
	t.Setenv("RTP_AGENT_TTS_MAX_BUFFER_DELAY_MS", "1200")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.STT.Label(); got != "inworld.STT" {
		t.Fatalf("STT label = %q, want inworld.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming interim-only", caps)
	}
	if got := app.Session.TTS.Label(); got != "inworld.TTS" {
		t.Fatalf("TTS label = %q, want inworld.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 22050 {
		t.Fatalf("TTS sample rate = %d, want 22050", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || !caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsHumeProviders(t *testing.T) {
	t.Setenv("HUME_API_KEY", "test-hume-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "hume")
	t.Setenv("RTP_AGENT_LLM_MODEL", "hume-evi-test")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "hume")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://hume.example")
	t.Setenv("RTP_AGENT_TTS_MODEL", "2")
	t.Setenv("RTP_AGENT_TTS_VOICE", "Ava")
	t.Setenv("RTP_AGENT_TTS_VOICE_ID", "voice-id-test")
	t.Setenv("RTP_AGENT_TTS_VOICE_PROVIDER", "HUME_AI")
	t.Setenv("RTP_AGENT_TTS_INSTRUCTIONS", "warm and calm")
	t.Setenv("RTP_AGENT_TTS_SPEED", "1.1")
	t.Setenv("RTP_AGENT_TTS_TRAILING_SILENCE", "0.25")
	t.Setenv("RTP_AGENT_TTS_INSTANT_MODE", "false")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "wav")
	t.Setenv("RTP_AGENT_TTS_CONTEXT_UTTERANCES", "hello there,how are you")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.TTS.Label(); got != "hume.TTS" {
		t.Fatalf("TTS label = %q, want hume.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 48000 {
		t.Fatalf("TTS sample rate = %d, want 48000", got)
	}
	if caps := app.Session.TTS.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want non-streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsMinimaxProviders(t *testing.T) {
	t.Setenv("MINIMAX_API_KEY", "test-minimax-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "minimax")
	t.Setenv("RTP_AGENT_LLM_MODEL", "abab-test")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "minimax")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://minimax.example")
	t.Setenv("RTP_AGENT_TTS_MODEL", "speech-test")
	t.Setenv("RTP_AGENT_TTS_VOICE", "voice-test")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "32000")
	t.Setenv("RTP_AGENT_TTS_BIT_RATE", "96000")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "pcm")
	t.Setenv("RTP_AGENT_TTS_EMOTION", "happy")
	t.Setenv("RTP_AGENT_TTS_SPEED", "1.2")
	t.Setenv("RTP_AGENT_TTS_VOLUME", "0.9")
	t.Setenv("RTP_AGENT_TTS_PITCH", "2")
	t.Setenv("RTP_AGENT_TTS_TEXT_NORMALIZATION", "true")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.TTS.Label(); got != "minimax.TTS" {
		t.Fatalf("TTS label = %q, want minimax.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 32000 {
		t.Fatalf("TTS sample rate = %d, want 32000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsMistralAIProviders(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "test-mistral-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "mistralai")
	t.Setenv("RTP_AGENT_LLM_MODEL", "ministral-test")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "mistralai")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://mistral.example/v1")
	t.Setenv("RTP_AGENT_STT_MODEL", "voxtral-mini-test")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_KEYTERMS_PROMPT", "LiveKit,Agents")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "mistralai")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://mistral.example/v1")
	t.Setenv("RTP_AGENT_TTS_MODEL", "voxtral-tts-test")
	t.Setenv("RTP_AGENT_TTS_VOICE", "en_paul_neutral")
	t.Setenv("RTP_AGENT_TTS_REF_AUDIO", "https://example.com/reference.wav")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "pcm")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.STT.Label(); got != "mistralai.STT" {
		t.Fatalf("STT label = %q, want mistralai.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); caps.Streaming || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want offline recognize only", caps)
	}
	if got := app.Session.TTS.Label(); got != "mistralai.TTS" {
		t.Fatalf("TTS label = %q, want mistralai.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
	if caps := app.Session.TTS.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want non-streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsMurfTTS(t *testing.T) {
	t.Setenv("MURF_API_KEY", "test-murf-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "murf")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://murf.example")
	t.Setenv("RTP_AGENT_TTS_MODEL", "FALCON")
	t.Setenv("RTP_AGENT_TTS_VOICE", "en-US-matthew")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en-US")
	t.Setenv("RTP_AGENT_TTS_INSTRUCTIONS", "Conversation")
	t.Setenv("RTP_AGENT_TTS_SPEED", "4")
	t.Setenv("RTP_AGENT_TTS_PITCH", "2")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "44100")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "murf.TTS" {
		t.Fatalf("TTS label = %q, want murf.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 44100 {
		t.Fatalf("TTS sample rate = %d, want 44100", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsLMNTTTS(t *testing.T) {
	t.Setenv("LMNT_API_KEY", "test-lmnt-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "lmnt")
	t.Setenv("RTP_AGENT_TTS_MODEL", "blizzard")
	t.Setenv("RTP_AGENT_TTS_VOICE", "leah")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "wav")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "48000")
	t.Setenv("RTP_AGENT_TTS_TEMPERATURE", "0.7")
	t.Setenv("RTP_AGENT_TTS_TOP_P", "0.9")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "lmnt.TTS" {
		t.Fatalf("TTS label = %q, want lmnt.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 48000 {
		t.Fatalf("TTS sample rate = %d, want 48000", got)
	}
	if caps := app.Session.TTS.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want non-streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsNeuphonicTTS(t *testing.T) {
	t.Setenv("NEUPHONIC_API_KEY", "test-neuphonic-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "neuphonic")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://neuphonic.example")
	t.Setenv("RTP_AGENT_TTS_VOICE", "voice-id")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "pcm_linear")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "44100")
	t.Setenv("RTP_AGENT_TTS_SPEED", "1.1")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "neuphonic.TTS" {
		t.Fatalf("TTS label = %q, want neuphonic.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 44100 {
		t.Fatalf("TTS sample rate = %d, want 44100", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsResembleTTS(t *testing.T) {
	t.Setenv("RESEMBLE_API_KEY", "test-resemble-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "resemble")
	t.Setenv("RTP_AGENT_TTS_MODEL", "chatterbox-turbo")
	t.Setenv("RTP_AGENT_TTS_VOICE", "voice-uuid")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "24000")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "resemble.TTS" {
		t.Fatalf("TTS label = %q, want resemble.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsRespeecherTTS(t *testing.T) {
	t.Setenv("RESPEECHER_API_KEY", "test-respeecher-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "respeecher")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://respeecher.example/v1")
	t.Setenv("RTP_AGENT_TTS_MODEL", "/public/tts/ua-rt")
	t.Setenv("RTP_AGENT_TTS_VOICE", "olesia-conversation")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "48000")
	t.Setenv("RTP_AGENT_TTS_JSON_CONFIG", "temperature=0.4")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "respeecher.TTS" {
		t.Fatalf("TTS label = %q, want respeecher.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 48000 {
		t.Fatalf("TTS sample rate = %d, want 48000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsRimeTTS(t *testing.T) {
	t.Setenv("RIME_API_KEY", "test-rime-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "rime")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://rime.example/v1/rime-tts")
	t.Setenv("RTP_AGENT_TTS_WEBSOCKET_URL", "wss://rime.example")
	t.Setenv("RTP_AGENT_TTS_MODEL", "mist")
	t.Setenv("RTP_AGENT_TTS_VOICE", "cove")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "eng")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "44100")
	t.Setenv("RTP_AGENT_TTS_SPEED", "1.1")
	t.Setenv("RTP_AGENT_TTS_DELIVERY_MODE", "bySentence")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "rime.TTS" {
		t.Fatalf("TTS label = %q, want rime.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 44100 {
		t.Fatalf("TTS sample rate = %d, want 44100", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || !caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming with aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsSarvamProviders(t *testing.T) {
	t.Setenv("SARVAM_API_KEY", "test-sarvam-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "sarvam")
	t.Setenv("RTP_AGENT_LLM_MODEL", "sarvam-30b")
	t.Setenv("RTP_AGENT_LLM_BASE_URL", "https://sarvam.example/v1")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "sarvam")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://sarvam.example/speech-to-text")
	t.Setenv("RTP_AGENT_STT_STREAMING_URL", "wss://sarvam.example/speech-to-text/ws")
	t.Setenv("RTP_AGENT_STT_MODEL", "saarika:v2.5")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "hi-IN")
	t.Setenv("RTP_AGENT_STT_TASK", "transcribe")
	t.Setenv("RTP_AGENT_STT_PROMPT", "domain words")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_VAD_EVENTS", "true")
	t.Setenv("RTP_AGENT_STT_VAD_FLUSH", "true")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "sarvam")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://sarvam.example/text-to-speech")
	t.Setenv("RTP_AGENT_TTS_WEBSOCKET_URL", "wss://sarvam.example/text-to-speech/ws")
	t.Setenv("RTP_AGENT_TTS_MODEL", "bulbul:v2")
	t.Setenv("RTP_AGENT_TTS_VOICE", "anushka")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "hi-IN")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "22050")
	t.Setenv("RTP_AGENT_TTS_TEMPERATURE", "0.4")
	t.Setenv("RTP_AGENT_TTS_SPEED", "1.1")
	t.Setenv("RTP_AGENT_TTS_PITCH", "2")
	t.Setenv("RTP_AGENT_TTS_BIT_RATE", "128000")
	t.Setenv("RTP_AGENT_TTS_BUFFER_SIZE", "20")
	t.Setenv("RTP_AGENT_TTS_CHUNK_LENGTH", "120")
	t.Setenv("RTP_AGENT_TTS_ENHANCE_NAMED_ENTITIES", "true")
	t.Setenv("RTP_AGENT_TTS_INSTANT_MODE", "false")
	t.Setenv("RTP_AGENT_TTS_PRONUNCIATION_DICT_ID", "dict-1")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "wav")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.STT.Label(); got != "sarvam.STT" {
		t.Fatalf("STT label = %q, want sarvam.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming interim offline", caps)
	}
	if got := app.Session.TTS.Label(); got != "sarvam.TTS" {
		t.Fatalf("TTS label = %q, want sarvam.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 22050 {
		t.Fatalf("TTS sample rate = %d, want 22050", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsRtzrSTT(t *testing.T) {
	t.Setenv("RTZR_CLIENT_ID", "client-id")
	t.Setenv("RTZR_CLIENT_SECRET", "client-secret")
	t.Setenv("RTZR_ACCESS_TOKEN", "access-token")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "rtzr")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://rtzr.example")
	t.Setenv("RTP_AGENT_STT_STREAMING_URL", "wss://rtzr.example")
	t.Setenv("RTP_AGENT_STT_MODEL", "sommers_ko")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "ko")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_DOMAIN", "CALL")
	t.Setenv("RTP_AGENT_STT_ENDPOINTING_SECONDS", "0.7")
	t.Setenv("RTP_AGENT_STT_VAD_THRESHOLD", "0.6")
	t.Setenv("RTP_AGENT_STT_END_OF_TURN_CONFIDENCE_THRESHOLD", "0.8")
	t.Setenv("RTP_AGENT_STT_PUNCTUATE", "true")
	t.Setenv("RTP_AGENT_STT_KEYTERMS_PROMPT", "livekit,agents")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.STT == nil {
		t.Fatal("Session STT is nil")
	}
	if got := app.Session.STT.Label(); got != "rtzr.STT" {
		t.Fatalf("STT label = %q, want rtzr.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming interim without offline recognize", caps)
	}
}

func TestDefaultConfigFromEnvSelectsSimplismartProviders(t *testing.T) {
	t.Setenv("SIMPLISMART_API_KEY", "test-simplismart-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "simplismart")
	t.Setenv("RTP_AGENT_LLM_MODEL", "simplismart-chat")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "simplismart")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://simplismart.example/predict")
	t.Setenv("RTP_AGENT_STT_MODEL", "openai/whisper-large-v3-turbo")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_TASK", "transcribe")
	t.Setenv("RTP_AGENT_STT_INTERIM_RESULTS", "true")
	t.Setenv("RTP_AGENT_STT_INCLUDE_TIMESTAMPS", "false")
	t.Setenv("RTP_AGENT_STT_KEYTERMS_PROMPT", "livekit,agents")
	t.Setenv("RTP_AGENT_STT_MAX_SPEAKERS", "2")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "simplismart")
	t.Setenv("RTP_AGENT_TTS_VOICE", "default_voice")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.STT.Label(); got != "simplismart.STT" {
		t.Fatalf("STT label = %q, want simplismart.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || caps.InterimResults || !caps.Diarization || caps.AlignedTranscript != "word" || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming diarization word-aligned offline", caps)
	}
	if got := app.Session.TTS.Label(); got != "simplismart.TTS" {
		t.Fatalf("TTS label = %q, want simplismart.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
	if caps := app.Session.TTS.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want non-streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsSmallestAISpeechProviders(t *testing.T) {
	t.Setenv("SMALLESTAI_API_KEY", "test-smallestai-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "smallestai")
	t.Setenv("RTP_AGENT_LLM_MODEL", "smallestai-chat")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "smallestai")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://smallest.example/waves/v1")
	t.Setenv("RTP_AGENT_STT_MODEL", "pulse")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_ENCODING", "linear16")
	t.Setenv("RTP_AGENT_STT_WORD_TIMESTAMPS", "true")
	t.Setenv("RTP_AGENT_STT_DIARIZATION", "true")
	t.Setenv("RTP_AGENT_STT_ENDPOINTING_MS", "500")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "smallestai")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://smallest.example/waves/v1")
	t.Setenv("RTP_AGENT_TTS_WEBSOCKET_URL", "wss://smallest.example/waves/v1/tts/live")
	t.Setenv("RTP_AGENT_TTS_MODEL", "lightning_v3.1_pro")
	t.Setenv("RTP_AGENT_TTS_VOICE", "meher")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "24000")
	t.Setenv("RTP_AGENT_TTS_SPEED", "1.1")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "pcm")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.STT.Label(); got != "smallestai.STT" {
		t.Fatalf("STT label = %q, want smallestai.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults || !caps.Diarization || caps.AlignedTranscript != "word" || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming diarization word-aligned offline", caps)
	}
	if got := app.Session.TTS.Label(); got != "smallestai.TTS" {
		t.Fatalf("TTS label = %q, want smallestai.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsSLNGSpeechProviders(t *testing.T) {
	t.Setenv("SLNG_API_KEY", "test-slng-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "slng")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "wss://slng.example/stt")
	t.Setenv("RTP_AGENT_STT_MODEL", "deepgram/nova:3")
	t.Setenv("RTP_AGENT_STT_REGION", "us")
	t.Setenv("RTP_AGENT_STT_ENCODING", "pcm_s16le")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_INTERIM_RESULTS", "false")
	t.Setenv("RTP_AGENT_STT_DIARIZATION", "true")
	t.Setenv("RTP_AGENT_STT_MIN_SPEAKERS", "1")
	t.Setenv("RTP_AGENT_STT_MAX_SPEAKERS", "2")
	t.Setenv("RTP_AGENT_STT_MODEL_OPTIONS", "punctuate=true,tier=enhanced")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "slng")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "wss://slng.example/tts")
	t.Setenv("RTP_AGENT_TTS_MODEL", "deepgram/aura:2")
	t.Setenv("RTP_AGENT_TTS_REGION", "eu")
	t.Setenv("RTP_AGENT_TTS_VOICE", "athena")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "32000")
	t.Setenv("RTP_AGENT_TTS_SPEED", "1.2")
	t.Setenv("RTP_AGENT_TTS_MODEL_OPTIONS", "encoding=linear16")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "slng.STT" {
		t.Fatalf("STT label = %q, want slng.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.Diarization {
		t.Fatalf("STT capabilities = %+v, want streaming diarization", caps)
	}
	if got := app.Session.TTS.Label(); got != "slng.TTS" {
		t.Fatalf("TTS label = %q, want slng.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 32000 {
		t.Fatalf("TTS sample rate = %d, want 32000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsSonioxSpeechProviders(t *testing.T) {
	t.Setenv("SONIOX_API_KEY", "test-soniox-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "soniox")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "wss://soniox.example/stt")
	t.Setenv("RTP_AGENT_STT_MODEL", "stt-rt-v4")
	t.Setenv("RTP_AGENT_STT_LANGUAGE_OPTIONS", "en,es")
	t.Setenv("RTP_AGENT_STT_LANGUAGE_DETECTION", "false")
	t.Setenv("RTP_AGENT_STT_NUMBER_OF_CHANNELS", "2")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "8000")
	t.Setenv("RTP_AGENT_STT_DIARIZATION", "true")
	t.Setenv("RTP_AGENT_STT_ENDPOINTING_MS", "750")
	t.Setenv("RTP_AGENT_STT_SESSION_ID", "client-1")
	t.Setenv("RTP_AGENT_STT_MODEL_OPTIONS", "language_hints_strict=true,context_text=domain terms,context_terms=LiveKit|Cavos,context_general=product:rtp-agent,context_translation_terms=agent:agente")
	t.Setenv("RTP_AGENT_STT_TRANSLATION_SOURCE_LANGUAGES", "en")
	t.Setenv("RTP_AGENT_STT_TRANSLATION_TARGET_LANGUAGES", "es")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "soniox")
	t.Setenv("RTP_AGENT_TTS_WEBSOCKET_URL", "wss://soniox.example/tts")
	t.Setenv("RTP_AGENT_TTS_MODEL", "tts-rt-v1-preview")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "es")
	t.Setenv("RTP_AGENT_TTS_VOICE", "Adrian")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "mp3")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "48000")
	t.Setenv("RTP_AGENT_TTS_BIT_RATE", "128000")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "soniox.STT" {
		t.Fatalf("STT label = %q, want soniox.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults || !caps.Diarization || caps.AlignedTranscript != "chunk" || caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming interim diarization chunk-aligned without offline recognize", caps)
	}
	if got := app.Session.TTS.Label(); got != "soniox.TTS" {
		t.Fatalf("TTS label = %q, want soniox.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 48000 {
		t.Fatalf("TTS sample rate = %d, want 48000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsSonioxSTTWithReferenceLanguageHintFallback(t *testing.T) {
	t.Setenv("SONIOX_API_KEY", "test-soniox-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "soniox")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "id")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	sonioxProvider, ok := app.Session.STT.(*soniox.SonioxSTT)
	if !ok {
		t.Fatalf("STT provider type = %T, want *soniox.SonioxSTT", app.Session.STT)
	}
	languageHints := reflect.ValueOf(sonioxProvider).Elem().FieldByName("languageHints")
	got := make([]string, 0, languageHints.Len())
	for i := 0; i < languageHints.Len(); i++ {
		got = append(got, languageHints.Index(i).String())
	}
	if want := []string{"id"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("languageHints = %#v, want %#v", got, want)
	}
}

func TestDefaultConfigFromEnvSelectsSpeechifyTTS(t *testing.T) {
	t.Setenv("SPEECHIFY_API_KEY", "test-speechify-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "speechify")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://speechify.example/v1")
	t.Setenv("RTP_AGENT_TTS_VOICE", "cliff")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "mp3_48000")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en-US")
	t.Setenv("RTP_AGENT_TTS_MODEL", "simba-english")
	t.Setenv("RTP_AGENT_TTS_LOUDNESS_NORMALIZATION", "true")
	t.Setenv("RTP_AGENT_TTS_TEXT_NORMALIZATION", "false")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "speechify.TTS" {
		t.Fatalf("TTS label = %q, want speechify.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 48000 {
		t.Fatalf("TTS sample rate = %d, want 48000", got)
	}
	if caps := app.Session.TTS.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want non-streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsSpeechmaticsSpeechProviders(t *testing.T) {
	t.Setenv("SPEECHMATICS_API_KEY", "test-speechmatics-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "speechmatics")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "wss://speechmatics.example/v2")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "de")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "48000")
	t.Setenv("RTP_AGENT_STT_ENCODING", "pcm_f32le")
	t.Setenv("RTP_AGENT_STT_DOMAIN", "finance")
	t.Setenv("RTP_AGENT_STT_OUTPUT_LOCALE", "de-DE")
	t.Setenv("RTP_AGENT_STT_INTERIM_RESULTS", "false")
	t.Setenv("RTP_AGENT_STT_DIARIZATION", "false")
	t.Setenv("RTP_AGENT_STT_KEYTERMS_PROMPT", "LiveKit:live kit,Cavos")
	t.Setenv("RTP_AGENT_STT_OPERATING_POINT", "enhanced")
	t.Setenv("RTP_AGENT_STT_TEXT_TIMEOUT_SECONDS", "1.2")
	t.Setenv("RTP_AGENT_STT_VAD_SILENCE_THRESHOLD_SECONDS", "0.6")
	t.Setenv("RTP_AGENT_STT_MAX_DURATION_WITHOUT_ENDPOINTING_SECONDS", "1.8")
	t.Setenv("RTP_AGENT_STT_MODEL_OPTIONS", "focus_speakers=agent,ignore_speakers=customer,focus_mode=ignore,known_speakers=agent:spk-1,permitted_marks=.|?,speaker_sensitivity=0.7")
	t.Setenv("RTP_AGENT_STT_MAX_SPEAKERS", "4")
	t.Setenv("RTP_AGENT_STT_PREFER_CURRENT_SPEAKER", "true")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "speechmatics")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://tts.speechmatics.example")
	t.Setenv("RTP_AGENT_TTS_VOICE", "theo")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "24000")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "speechmatics.STT" {
		t.Fatalf("STT label = %q, want speechmatics.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults || !caps.Diarization || caps.AlignedTranscript != "chunk" || caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming interim diarization chunk-aligned without offline recognize", caps)
	}
	if got := app.Session.TTS.Label(); got != "speechmatics.TTS" {
		t.Fatalf("TTS label = %q, want speechmatics.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
	if caps := app.Session.TTS.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want non-streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsSpitchSpeechProviders(t *testing.T) {
	t.Setenv("SPITCH_API_KEY", "test-spitch-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "spitch")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "spitch")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://spitch.example")
	t.Setenv("RTP_AGENT_TTS_VOICE", "amina")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "fr")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "wav")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "16000")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "spitch.STT" {
		t.Fatalf("STT label = %q, want spitch.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); caps.Streaming || caps.InterimResults || caps.Diarization || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want offline recognize only", caps)
	}
	if got := app.Session.TTS.Label(); got != "spitch.TTS" {
		t.Fatalf("TTS label = %q, want spitch.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 16000 {
		t.Fatalf("TTS sample rate = %d, want 16000", got)
	}
	if caps := app.Session.TTS.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want non-streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsTelnyxProviders(t *testing.T) {
	t.Setenv("TELNYX_API_KEY", "test-telnyx-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "telnyx")
	t.Setenv("RTP_AGENT_LLM_MODEL", "telnyx-chat")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "telnyx")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "wss://telnyx.example/transcription")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "es")
	t.Setenv("RTP_AGENT_STT_MODEL", "deepgram")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "8000")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "telnyx")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "wss://telnyx.example/speech")
	t.Setenv("RTP_AGENT_TTS_VOICE", "Telnyx.NaturalHD.astra")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.STT.Label(); got != "telnyx.STT" {
		t.Fatalf("STT label = %q, want telnyx.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.Diarization || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming interim offline recognize without diarization", caps)
	}
	if got := app.Session.TTS.Label(); got != "telnyx.TTS" {
		t.Fatalf("TTS label = %q, want telnyx.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 16000 {
		t.Fatalf("TTS sample rate = %d, want 16000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvSelectsXAIProviders(t *testing.T) {
	t.Setenv("XAI_API_KEY", "test-xai-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "xai")
	t.Setenv("RTP_AGENT_LLM_MODEL", "grok-test")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "xai")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://xai.example/v1/stt")
	t.Setenv("RTP_AGENT_STT_STREAMING_URL", "wss://xai.example/v1/stt")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "8000")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "es")
	t.Setenv("RTP_AGENT_STT_INTERIM_RESULTS", "false")
	t.Setenv("RTP_AGENT_STT_DIARIZATION", "true")
	t.Setenv("RTP_AGENT_STT_ENDPOINTING_MS", "250")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "xai")
	t.Setenv("RTP_AGENT_TTS_WEBSOCKET_URL", "wss://xai.example/v1/tts")
	t.Setenv("RTP_AGENT_TTS_VOICE", "ara")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "es")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := app.Session.STT.Label(); got != "xai.STT" {
		t.Fatalf("STT label = %q, want xai.STT", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || caps.InterimResults || !caps.Diarization || caps.AlignedTranscript != "word" || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want streaming diarization word-aligned offline without interim", caps)
	}
	if got := app.Session.TTS.Label(); got != "xai.TTS" {
		t.Fatalf("TTS label = %q, want xai.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want streaming without aligned transcript", caps)
	}
}

func TestDefaultConfigFromEnvAddsXAIProviderTools(t *testing.T) {
	t.Setenv("XAI_API_KEY", "test-xai-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "xai")
	t.Setenv("RTP_AGENT_XAI_TOOLS", "web_search,x_search,file_search")
	t.Setenv("RTP_AGENT_XAI_ALLOWED_X_HANDLES", "cavos_io,livekit")
	t.Setenv("RTP_AGENT_XAI_FILE_SEARCH_VECTOR_STORE_IDS", "vs_1,vs_2")
	t.Setenv("RTP_AGENT_XAI_FILE_SEARCH_MAX_RESULTS", "3")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Agent == nil {
		t.Fatal("Agent is nil")
	}
	if len(app.Agent.Tools) != 3 {
		t.Fatalf("len(Agent.Tools) = %d, want 3", len(app.Agent.Tools))
	}
	if got := app.Agent.Tools[0].Name(); got != "xai_web_search" {
		t.Fatalf("tool[0].Name() = %q, want xai_web_search", got)
	}
	if got := app.Agent.Tools[1].Name(); got != "xai_x_search" {
		t.Fatalf("tool[1].Name() = %q, want xai_x_search", got)
	}
	if got := app.Agent.Tools[2].Name(); got != "xai_file_search" {
		t.Fatalf("tool[2].Name() = %q, want xai_file_search", got)
	}
}

func TestDefaultConfigFromEnvAddsMCPStdioTools(t *testing.T) {
	servers := []MCPStdioServerConfig{{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPStdioHelperProcess"},
		Env:     map[string]string{"GO_WANT_MCP_HELPER": "1"},
	}}
	encoded, err := json.Marshal(servers)
	if err != nil {
		t.Fatalf("marshal MCP config: %v", err)
	}
	t.Setenv("RTP_AGENT_MCP_STDIO_SERVERS", string(encoded))

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	defer app.closeMCPServers()
	if len(app.Agent.Tools) != 1 {
		t.Fatalf("len(Agent.Tools) = %d, want 1 MCP tool", len(app.Agent.Tools))
	}
	if got := app.Agent.Tools[0].Name(); got != "lookup" {
		t.Fatalf("tool name = %q, want lookup", got)
	}
}

func TestDefaultConfigFromEnvAddsMCPHTTPTools(t *testing.T) {
	httpClient := newAppMCPHTTPTestClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("Authorization header = %q, want bearer token", got)
		}
		var req appMCPJSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode MCP request: %v", err)
		}
		switch req.Method {
		case "initialize":
			writeAppMCPHTTPResponse(t, w, req.ID, map[string]any{"protocolVersion": "2024-11-05"})
		case "initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeAppMCPHTTPResponse(t, w, req.ID, map[string]any{
				"tools": []map[string]any{
					{"name": "lookup", "description": "lookup tool", "inputSchema": map[string]any{"type": "object"}},
				},
			})
		default:
			t.Fatalf("unexpected MCP method %q", req.Method)
		}
	}))
	originalNewMCPServerHTTP := appNewMCPServerHTTP
	appNewMCPServerHTTP = func(url string) *llm.MCPServerHTTP {
		server := llm.NewMCPServerHTTP(url)
		server.SetHTTPClient(httpClient)
		return server
	}
	t.Cleanup(func() {
		appNewMCPServerHTTP = originalNewMCPServerHTTP
	})

	servers := []map[string]any{{
		"url":           "https://mcp.test/rpc",
		"transportType": "streamable_http",
		"allowedTools":  []string{"lookup"},
		"headers":       map[string]string{"Authorization": "Bearer token"},
	}}
	encoded, err := json.Marshal(servers)
	if err != nil {
		t.Fatalf("marshal MCP HTTP config: %v", err)
	}
	t.Setenv("RTP_AGENT_MCP_HTTP_SERVERS", string(encoded))

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	defer app.closeMCPServers()
	if len(app.Agent.Tools) != 1 {
		t.Fatalf("len(Agent.Tools) = %d, want 1 MCP HTTP tool", len(app.Agent.Tools))
	}
	if got := len(app.Session.MCPServers()); got != 1 {
		t.Fatalf("len(Session.MCPServers()) = %d, want 1 MCP HTTP server", got)
	}
	if got := app.Agent.Tools[0].Name(); got != "lookup" {
		t.Fatalf("tool name = %q, want lookup", got)
	}
}

func newAppMCPHTTPTestClient(handler http.Handler) *http.Client {
	return &http.Client{
		Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			resp := recorder.Result()
			if resp.Body == nil {
				resp.Body = io.NopCloser(strings.NewReader(""))
			}
			return resp, nil
		}),
	}
}

type appMCPHTTPRoundTripper func(*http.Request) (*http.Response, error)

func (f appMCPHTTPRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestMCPStdioHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil || request.ID == 0 {
			continue
		}
		switch request.Method {
		case "initialize":
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{},
					"serverInfo":      map[string]any{"name": "fake", "version": "1"},
				},
			})
		case "tools/list":
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"result": map[string]any{
					"tools": []map[string]any{{
						"name":        "lookup",
						"description": "Look up information",
						"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
					}},
				},
			})
		}
	}
	os.Exit(0)
}

func TestDefaultConfigFromEnvAddsEndCallTool(t *testing.T) {
	t.Setenv("RTP_AGENT_TOOLS", "end_call")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Agent == nil {
		t.Fatal("Agent is nil")
	}
	if len(app.Agent.Tools) != 1 {
		t.Fatalf("len(Agent.Tools) = %d, want 1", len(app.Agent.Tools))
	}
	if got := app.Agent.Tools[0].Name(); got != "end_call" {
		t.Fatalf("tool[0].Name() = %q, want end_call", got)
	}
}

type appMCPJSONRPCRequest struct {
	ID     int64  `json:"id"`
	Method string `json:"method"`
}

func writeAppMCPHTTPResponse(t *testing.T, w http.ResponseWriter, id int64, result any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}); err != nil {
		t.Fatalf("encode MCP response: %v", err)
	}
}

func TestDefaultConfigFromEnvSelectsDtmfWorkflowAgent(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "dtmf")
	t.Setenv("RTP_AGENT_WORKFLOW_DTMF_NUM_DIGITS", "4")
	t.Setenv("RTP_AGENT_WORKFLOW_DTMF_ASK_CONFIRMATION", "true")
	t.Setenv("RTP_AGENT_WORKFLOW_DTMF_INPUT_TIMEOUT_SECONDS", "2.5")
	t.Setenv("RTP_AGENT_WORKFLOW_DTMF_STOP_EVENT", "*")
	t.Setenv("RTP_AGENT_WORKFLOW_DTMF_EXTRA_INSTRUCTIONS", "Tell the user this is their appointment PIN.")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	task, ok := app.Session.Agent.(*workflows.GetDtmfTask)
	if !ok {
		t.Fatalf("Session.Agent = %T, want *workflows.GetDtmfTask", app.Session.Agent)
	}
	if task.NumDigits != 4 {
		t.Fatalf("NumDigits = %d, want 4", task.NumDigits)
	}
	if !task.AskForConfirmation {
		t.Fatal("AskForConfirmation = false, want true")
	}
	if task.DtmfInputTimeout != 2500*time.Millisecond {
		t.Fatalf("DtmfInputTimeout = %s, want 2.5s", task.DtmfInputTimeout)
	}
	if string(task.DtmfStopEvent) != "*" {
		t.Fatalf("DtmfStopEvent = %q, want *", task.DtmfStopEvent)
	}
	if !strings.Contains(task.Instructions, "Tell the user this is their appointment PIN.") {
		t.Fatalf("Instructions = %q, want DTMF extra instructions", task.Instructions)
	}
	if app.Agent != task.GetAgent() {
		t.Fatal("App.Agent does not point at selected workflow agent")
	}
	if len(app.Agent.Tools) != 1 || app.Agent.Tools[0].Name() != "confirm_inputs" {
		t.Fatalf("workflow tools = %#v, want confirm_inputs", app.Agent.Tools)
	}
}

func TestDefaultConfigFromEnvRejectsInvalidDtmfNumDigits(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "dtmf")
	t.Setenv("RTP_AGENT_WORKFLOW_DTMF_NUM_DIGITS", "0")

	_, err := NewApp(DefaultConfigFromEnv())
	if err == nil {
		t.Fatal("NewApp() error = nil, want invalid DTMF num digits error")
	}
	if !strings.Contains(err.Error(), "num_digits must be greater than 0") {
		t.Fatalf("NewApp() error = %v, want invalid num_digits error", err)
	}
}

func TestDefaultConfigFromEnvSelectsAddressWorkflowAgent(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "address")
	t.Setenv("RTP_AGENT_WORKFLOW_REQUIRE_CONFIRMATION", "true")
	t.Setenv("RTP_AGENT_WORKFLOW_ADDRESS_PERSONA", "You only collect shipping addresses for hardware orders.")
	t.Setenv("RTP_AGENT_WORKFLOW_ADDRESS_EXTRA_INSTRUCTIONS", "Ask whether this is the shipping address.")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	task, ok := app.Session.Agent.(*workflows.GetAddressTask)
	if !ok {
		t.Fatalf("Session.Agent = %T, want *workflows.GetAddressTask", app.Session.Agent)
	}
	if !task.RequireConfirmation {
		t.Fatal("RequireConfirmation = false, want true")
	}
	if !strings.Contains(task.Instructions, "Ask whether this is the shipping address.") {
		t.Fatalf("Instructions = %q, want address extra instructions", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "You only collect shipping addresses for hardware orders.") {
		t.Fatalf("Instructions = %q, want custom address persona", task.Instructions)
	}
	if strings.Contains(task.Instructions, "responsible solely for capturing an address") {
		t.Fatalf("Instructions = %q, want default address persona replaced", task.Instructions)
	}
	if app.Agent != task.GetAgent() {
		t.Fatal("App.Agent does not point at selected address workflow agent")
	}
	if len(app.Agent.Tools) != 2 {
		t.Fatalf("workflow tools = %d, want update/decline tools", len(app.Agent.Tools))
	}
}

func TestDefaultConfigFromEnvSelectsEmailWorkflowAgent(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "email")
	t.Setenv("RTP_AGENT_WORKFLOW_REQUIRE_CONFIRMATION", "true")
	t.Setenv("RTP_AGENT_WORKFLOW_EMAIL_PERSONA", "You only collect work email addresses for account recovery.")
	t.Setenv("RTP_AGENT_WORKFLOW_EMAIL_EXTRA_INSTRUCTIONS", "Ask for the work email address on file.")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	task, ok := app.Session.Agent.(*workflows.GetEmailTask)
	if !ok {
		t.Fatalf("Session.Agent = %T, want *workflows.GetEmailTask", app.Session.Agent)
	}
	if !task.RequireConfirmation {
		t.Fatal("RequireConfirmation = false, want true")
	}
	if !strings.Contains(task.Instructions, "Ask for the work email address on file.") {
		t.Fatalf("Instructions = %q, want email extra instructions", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "You only collect work email addresses for account recovery.") {
		t.Fatalf("Instructions = %q, want custom email persona", task.Instructions)
	}
	if strings.Contains(task.Instructions, "responsible solely for capturing an email address") {
		t.Fatalf("Instructions = %q, want default email persona replaced", task.Instructions)
	}
	if app.Agent != task.GetAgent() {
		t.Fatal("App.Agent does not point at selected workflow agent")
	}
	if len(app.Agent.Tools) != 2 {
		t.Fatalf("workflow tools = %d, want email update/decline tools", len(app.Agent.Tools))
	}
	if app.Agent.Tools[0].Name() != "update_email_address" || app.Agent.Tools[1].Name() != "decline_email_capture" {
		t.Fatalf("workflow tools = %#v, want email update/decline tools", app.Agent.Tools)
	}
}

func TestDefaultConfigFromEnvSelectsPhoneNumberWorkflowAgent(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "phone_number")
	t.Setenv("RTP_AGENT_WORKFLOW_REQUIRE_CONFIRMATION", "true")
	t.Setenv("RTP_AGENT_WORKFLOW_PHONE_NUMBER_EXTRA_INSTRUCTIONS", "Ask whether this is a mobile number.")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	task, ok := app.Session.Agent.(*workflows.GetPhoneNumberTask)
	if !ok {
		t.Fatalf("Session.Agent = %T, want *workflows.GetPhoneNumberTask", app.Session.Agent)
	}
	if !task.RequireConfirmation {
		t.Fatal("RequireConfirmation = false, want true")
	}
	if !strings.Contains(task.Instructions, "Ask whether this is a mobile number.") {
		t.Fatalf("Instructions = %q, want phone-number extra instructions", task.Instructions)
	}
	if app.Agent != task.GetAgent() {
		t.Fatal("App.Agent does not point at selected phone-number workflow agent")
	}
	if len(app.Agent.Tools) != 2 {
		t.Fatalf("workflow tools = %d, want update/decline tools", len(app.Agent.Tools))
	}
}

func TestDefaultConfigFromEnvSelectsDOBWorkflowAgent(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "dob")
	t.Setenv("RTP_AGENT_WORKFLOW_REQUIRE_CONFIRMATION", "true")
	t.Setenv("RTP_AGENT_WORKFLOW_DOB_INCLUDE_TIME", "true")
	t.Setenv("RTP_AGENT_WORKFLOW_DOB_EXTRA_INSTRUCTIONS", "Ask for the birthdate exactly as shown on the insurance card.")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	task, ok := app.Session.Agent.(*workflows.GetDOBTask)
	if !ok {
		t.Fatalf("Session.Agent = %T, want *workflows.GetDOBTask", app.Session.Agent)
	}
	if !task.RequireConfirmation {
		t.Fatal("RequireConfirmation = false, want true")
	}
	if !task.IncludeTime {
		t.Fatal("IncludeTime = false, want true")
	}
	if !strings.Contains(task.Instructions, "Ask for the birthdate exactly as shown on the insurance card.") {
		t.Fatalf("Instructions = %q, want DOB extra instructions", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "Also ask for and capture the time of birth if the user knows it.") {
		t.Fatalf("Instructions = %q, want DOB time instructions", task.Instructions)
	}
	if app.Agent != task.GetAgent() {
		t.Fatal("App.Agent does not point at selected DOB workflow agent")
	}
	if len(app.Agent.Tools) != 3 {
		t.Fatalf("workflow tools = %d, want update/decline/time tools", len(app.Agent.Tools))
	}
	wantTools := map[string]bool{"update_dob": true, "decline_dob_capture": true, "update_time": true}
	for _, tool := range app.Agent.Tools {
		delete(wantTools, tool.Name())
	}
	if len(wantTools) != 0 {
		t.Fatalf("workflow tools = %#v, missing %v", app.Agent.Tools, wantTools)
	}
}

func TestDefaultConfigFromEnvSelectsNameWorkflowAgent(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "name")
	t.Setenv("RTP_AGENT_WORKFLOW_REQUIRE_CONFIRMATION", "true")
	t.Setenv("RTP_AGENT_WORKFLOW_NAME_MIDDLE_NAME", "true")
	t.Setenv("RTP_AGENT_WORKFLOW_NAME_VERIFY_SPELLING", "true")
	t.Setenv("RTP_AGENT_WORKFLOW_NAME_FORMAT", "{last_name}, {first_name} {middle_name}")
	t.Setenv("RTP_AGENT_WORKFLOW_NAME_EXTRA_INSTRUCTIONS", "Ask for the legal name on the account.")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	task, ok := app.Session.Agent.(*workflows.GetNameTask)
	if !ok {
		t.Fatalf("Session.Agent = %T, want *workflows.GetNameTask", app.Session.Agent)
	}
	if !task.RequireConfirmation {
		t.Fatal("RequireConfirmation = false, want true")
	}
	if !task.CollectFirstName || !task.CollectMiddleName || !task.CollectLastName {
		t.Fatalf("name parts = first:%t middle:%t last:%t, want all enabled", task.CollectFirstName, task.CollectMiddleName, task.CollectLastName)
	}
	if !task.VerifySpelling {
		t.Fatal("VerifySpelling = false, want true")
	}
	if !strings.Contains(task.Instructions, "You need to naturally collect the name parts in this order: {last_name}, {first_name} {middle_name}.") {
		t.Fatalf("Instructions = %q, want configured name collection order", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "After receiving the name, always verify the spelling") {
		t.Fatalf("Instructions = %q, want spelling verification instructions", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "Ask for the legal name on the account.") {
		t.Fatalf("Instructions = %q, want name extra instructions", task.Instructions)
	}
	if app.Agent != task.GetAgent() {
		t.Fatal("App.Agent does not point at selected name workflow agent")
	}
	if len(app.Agent.Tools) != 2 {
		t.Fatalf("workflow tools = %d, want update/decline tools", len(app.Agent.Tools))
	}
}

func TestDefaultConfigFromEnvRejectsNameWorkflowWithoutSelectedParts(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "name")
	t.Setenv("RTP_AGENT_WORKFLOW_NAME_FIRST_NAME", "false")
	t.Setenv("RTP_AGENT_WORKFLOW_NAME_MIDDLE_NAME", "false")
	t.Setenv("RTP_AGENT_WORKFLOW_NAME_LAST_NAME", "false")

	_, err := NewApp(DefaultConfigFromEnv())
	if err == nil {
		t.Fatal("NewApp() error = nil, want no selected name parts error")
	}
	if got, want := err.Error(), "At least one of first_name, middle_name, or last_name must be True"; got != want {
		t.Fatalf("NewApp() error = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), "must be true") {
		t.Fatalf("NewApp() error = %q, want reference True casing", err.Error())
	}
}

func TestDefaultConfigFromEnvSelectsCardNumberWorkflowAgent(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "card_number")
	t.Setenv("RTP_AGENT_WORKFLOW_REQUIRE_CONFIRMATION", "true")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	task, ok := app.Session.Agent.(*workflows.GetCardNumberTask)
	if !ok {
		t.Fatalf("Session.Agent = %T, want *workflows.GetCardNumberTask", app.Session.Agent)
	}
	if !task.RequireConfirmation {
		t.Fatal("RequireConfirmation = false, want true")
	}
	if app.Agent != task.GetAgent() {
		t.Fatal("App.Agent does not point at selected card-number workflow agent")
	}
	if len(app.Agent.Tools) != 3 {
		t.Fatalf("workflow tools = %d, want record/decline/restart tools", len(app.Agent.Tools))
	}
}

func TestDefaultConfigFromEnvSelectsSecurityCodeWorkflowAgent(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "security_code")
	t.Setenv("RTP_AGENT_WORKFLOW_REQUIRE_CONFIRMATION", "true")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	task, ok := app.Session.Agent.(*workflows.GetSecurityCodeTask)
	if !ok {
		t.Fatalf("Session.Agent = %T, want *workflows.GetSecurityCodeTask", app.Session.Agent)
	}
	if !task.RequireConfirmation {
		t.Fatal("RequireConfirmation = false, want true")
	}
	if app.Agent != task.GetAgent() {
		t.Fatal("App.Agent does not point at selected security-code workflow agent")
	}
	if len(app.Agent.Tools) != 3 {
		t.Fatalf("workflow tools = %d, want update/decline/restart tools", len(app.Agent.Tools))
	}
}

func TestDefaultConfigFromEnvSelectsExpirationDateWorkflowAgent(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "expiration_date")
	t.Setenv("RTP_AGENT_WORKFLOW_REQUIRE_CONFIRMATION", "true")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	task, ok := app.Session.Agent.(*workflows.GetExpirationDateTask)
	if !ok {
		t.Fatalf("Session.Agent = %T, want *workflows.GetExpirationDateTask", app.Session.Agent)
	}
	if !task.RequireConfirmation {
		t.Fatal("RequireConfirmation = false, want true")
	}
	if app.Agent != task.GetAgent() {
		t.Fatal("App.Agent does not point at selected expiration-date workflow agent")
	}
	if len(app.Agent.Tools) != 3 {
		t.Fatalf("workflow tools = %d, want update/decline/restart tools", len(app.Agent.Tools))
	}
}

func TestDefaultConfigFromEnvSelectsCreditCardWorkflowAgent(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "credit_card")
	t.Setenv("RTP_AGENT_WORKFLOW_REQUIRE_CONFIRMATION", "true")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	task, ok := app.Session.Agent.(*workflows.GetCreditCardTask)
	if !ok {
		t.Fatalf("Session.Agent = %T, want *workflows.GetCreditCardTask", app.Session.Agent)
	}
	if !task.RequireConfirmation {
		t.Fatal("RequireConfirmation = false, want true")
	}
	if app.Agent != task.GetAgent() {
		t.Fatal("App.Agent does not point at selected credit-card workflow agent")
	}
}

func TestDefaultConfigFromEnvSelectsWarmTransferWorkflowAgent(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "warm_transfer")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_CALL_TO", "+15550100")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_TRUNK_ID", "trunk_123")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_NUMBER", "+15550999")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_HEADERS", "X-Trace=trace-a,X-Queue=billing")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_DTMF", "ww1234#")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_RINGING_TIMEOUT_SECONDS", "3.5")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_HOLD_AUDIO", "custom-hold.ogg")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_PERSONA", "You brief a licensed support specialist before joining the caller.")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_EXTRA_INSTRUCTIONS", "\nKeep the handoff concise.")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	task, ok := app.Session.Agent.(*workflows.WarmTransferTask)
	if !ok {
		t.Fatalf("Session.Agent = %T, want *workflows.WarmTransferTask", app.Session.Agent)
	}
	if task.TargetPhoneNumber != "+15550100" {
		t.Fatalf("TargetPhoneNumber = %q, want +15550100", task.TargetPhoneNumber)
	}
	if task.SipTrunkID != "trunk_123" {
		t.Fatalf("SipTrunkID = %q, want trunk_123", task.SipTrunkID)
	}
	if task.SipNumber != "+15550999" {
		t.Fatalf("SipNumber = %q, want +15550999", task.SipNumber)
	}
	if task.SipHeaders["X-Trace"] != "trace-a" || task.SipHeaders["X-Queue"] != "billing" {
		t.Fatalf("SipHeaders = %#v, want configured SIP headers", task.SipHeaders)
	}
	if task.Dtmf != "ww1234#" {
		t.Fatalf("Dtmf = %q, want ww1234#", task.Dtmf)
	}
	if task.RingingTimeout != 3500*time.Millisecond {
		t.Fatalf("RingingTimeout = %v, want 3.5s", task.RingingTimeout)
	}
	if task.HoldAudio != "custom-hold.ogg" {
		t.Fatalf("HoldAudio = %#v, want configured custom hold audio", task.HoldAudio)
	}
	if !strings.Contains(task.Instructions, "You brief a licensed support specialist before joining the caller.") {
		t.Fatalf("Instructions = %q, want custom warm-transfer persona", task.Instructions)
	}
	if strings.Contains(task.Instructions, "You are an agent that is reaching out to a human agent for help.") {
		t.Fatalf("Instructions = %q, want default warm-transfer persona replaced", task.Instructions)
	}
	if !strings.Contains(task.Instructions, "Keep the handoff concise.") {
		t.Fatalf("Instructions = %q, want warm-transfer extra instructions", task.Instructions)
	}
	if app.Agent != task.GetAgent() {
		t.Fatal("App.Agent does not point at selected warm transfer agent")
	}
	if len(app.Agent.Tools) != 3 {
		t.Fatalf("workflow tools = %d, want connect/decline/voicemail tools", len(app.Agent.Tools))
	}
}

func TestDefaultConfigFromEnvDisablesWarmTransferHoldAudio(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "warm_transfer")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_CALL_TO", "+15550100")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_TRUNK_ID", "trunk_123")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_DISABLE_HOLD_AUDIO", "true")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	task, ok := app.Session.Agent.(*workflows.WarmTransferTask)
	if !ok {
		t.Fatalf("Session.Agent = %T, want *workflows.WarmTransferTask", app.Session.Agent)
	}
	if task.HoldAudio != nil {
		t.Fatalf("HoldAudio = %#v, want nil when hold audio is disabled", task.HoldAudio)
	}
}

func TestDefaultConfigFromEnvRejectsWarmTransferWithoutSIPTrunk(t *testing.T) {
	t.Setenv("LIVEKIT_SIP_OUTBOUND_TRUNK", "")
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "warm_transfer")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_CALL_TO", "+15550100")

	_, err := NewApp(DefaultConfigFromEnv())
	if err == nil {
		t.Fatal("NewApp() error = nil, want missing SIP trunk error")
	}
	if !strings.Contains(err.Error(), "LIVEKIT_SIP_OUTBOUND_TRUNK") {
		t.Fatalf("NewApp() error = %v, want missing outbound trunk error", err)
	}
}

func TestDefaultConfigFromEnvSelectsWarmTransferSIPConnection(t *testing.T) {
	t.Setenv("LIVEKIT_SIP_OUTBOUND_TRUNK", "")
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "warm_transfer")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_CALL_TO", "+15550100")
	t.Setenv("RTP_AGENT_WORKFLOW_WARM_TRANSFER_SIP_CONNECTION_JSON", `{"hostname":"sip.example.com","destination_country":"US","auth_username":"agent","auth_password":"secret"}`)

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	task, ok := app.Session.Agent.(*workflows.WarmTransferTask)
	if !ok {
		t.Fatalf("Session.Agent = %T, want *workflows.WarmTransferTask", app.Session.Agent)
	}
	if task.SipTrunkID != "" {
		t.Fatalf("SipTrunkID = %q, want empty when explicit SIP connection is configured", task.SipTrunkID)
	}
	if task.SipConnection == nil {
		t.Fatal("SipConnection = nil, want configured SIP outbound connection")
	}
	if task.SipConnection.GetHostname() != "sip.example.com" ||
		task.SipConnection.GetDestinationCountry() != "US" ||
		task.SipConnection.GetAuthUsername() != "agent" ||
		task.SipConnection.GetAuthPassword() != "secret" {
		t.Fatalf("SipConnection = %#v, want configured SIP outbound connection", task.SipConnection)
	}
}

func TestDefaultConfigFromEnvSelectsTaskGroupWorkflowAgent(t *testing.T) {
	t.Setenv("RTP_AGENT_WORKFLOW_TASK", "task_group")
	t.Setenv("RTP_AGENT_WORKFLOW_TASK_GROUP_TASKS", "address,email,phone_number,dob,name,dtmf,card_number,security_code,expiration_date,credit_card")
	t.Setenv("RTP_AGENT_WORKFLOW_DTMF_NUM_DIGITS", "4")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	group, ok := app.Session.Agent.(*workflows.TaskGroup)
	if !ok {
		t.Fatalf("Session.Agent = %T, want *workflows.TaskGroup", app.Session.Agent)
	}
	if app.Agent != group.GetAgent() {
		t.Fatal("App.Agent does not point at selected task group agent")
	}
	if len(group.RegisteredTasks) != 10 {
		t.Fatalf("RegisteredTasks = %d, want 10", len(group.RegisteredTasks))
	}
	wantIDs := []string{"address", "email", "phone_number", "dob", "name", "dtmf", "card_number", "security_code", "expiration_date", "credit_card"}
	for i, want := range wantIDs {
		if got := group.RegisteredTasks[i].ID; got != want {
			t.Fatalf("RegisteredTasks[%d].ID = %q, want %q", i, got, want)
		}
	}
}

func TestDefaultConfigFromEnvEnablesIVRDetection(t *testing.T) {
	t.Setenv("RTP_AGENT_IVR_DETECTION", "true")
	t.Setenv("RTP_AGENT_IVR_SILENCE_DURATION_SECONDS", "0.25")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if !app.Session.Options.IVRDetection {
		t.Fatal("IVRDetection = false, want true")
	}
	if got := app.Session.Options.IVRSilenceDuration; got != 250*time.Millisecond {
		t.Fatalf("IVRSilenceDuration = %v, want 250ms", got)
	}
}

func TestDefaultConfigFromEnvConfiguresEvaluationJudges(t *testing.T) {
	t.Setenv("RTP_AGENT_EVAL_JUDGES", "task_completion,accuracy,safety")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Evaluator == nil {
		t.Fatal("Evaluator = nil, want configured judge group")
	}
	if len(app.Evaluator.Judges) != 3 {
		t.Fatalf("Evaluator.Judges = %d, want 3", len(app.Evaluator.Judges))
	}
	wantNames := []string{"task_completion", "accuracy", "safety"}
	for i, want := range wantNames {
		if got := app.Evaluator.Judges[i].Name(); got != want {
			t.Fatalf("Evaluator.Judges[%d].Name() = %q, want %q", i, got, want)
		}
	}
}

func TestDefaultConfigFromEnvWrapsLLMFallbackProviders(t *testing.T) {
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "minimal")
	t.Setenv("RTP_AGENT_LLM_FALLBACK_PROVIDERS", "openai")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := llm.Label(app.Agent.LLM); got != "FallbackAdapter(minimal.MinimalLLM)" {
		t.Fatalf("LLM label = %q, want fallback adapter around primary minimal LLM", got)
	}
}

func TestDefaultConfigFromEnvAcceptsTogetherLLMFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "minimal")
	t.Setenv("RTP_AGENT_LLM_FALLBACK_PROVIDERS", "together")
	t.Setenv("TOGETHER_API_KEY", "test-together-key")
	t.Setenv("RTP_AGENT_LLM_MODEL", "custom-together-model")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := llm.Label(app.Agent.LLM); got != "FallbackAdapter(minimal.MinimalLLM)" {
		t.Fatalf("LLM label = %q, want fallback adapter around primary minimal LLM", got)
	}
}

func TestDefaultConfigFromEnvAcceptsOpenAICompatibleLLMFallbackProviders(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		envKey   string
		envValue string
		baseURL  string
	}{
		{name: "deepseek", provider: "deepseek", envKey: "DEEPSEEK_API_KEY", envValue: "test-deepseek-key"},
		{name: "cometapi", provider: "cometapi", envKey: "COMETAPI_API_KEY", envValue: "test-cometapi-key"},
		{name: "nebius", provider: "nebius", envKey: "NEBIUS_API_KEY", envValue: "test-nebius-key"},
		{name: "letta", provider: "letta", envKey: "LETTA_API_KEY", envValue: "test-letta-key", baseURL: "https://letta.example.test/v1/chat/completions"},
		{name: "ovhcloud", provider: "ovhcloud", envKey: "OVHCLOUD_API_KEY", envValue: "test-ovhcloud-key"},
		{name: "octoai", provider: "octoai", envKey: "OCTOAI_TOKEN", envValue: "test-octoai-key"},
		{name: "sambanova", provider: "sambanova", envKey: "SAMBANOVA_API_KEY", envValue: "test-sambanova-key"},
		{name: "ollama", provider: "ollama"},
		{name: "openrouter", provider: "openrouter", envKey: "OPENROUTER_API_KEY", envValue: "test-openrouter-key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("RTP_AGENT_LLM_PROVIDER", "minimal")
			t.Setenv("RTP_AGENT_LLM_FALLBACK_PROVIDERS", tt.provider)
			t.Setenv("RTP_AGENT_LLM_MODEL", "custom-fallback-model")
			if tt.envKey != "" {
				t.Setenv(tt.envKey, tt.envValue)
			}
			if tt.baseURL != "" {
				t.Setenv("RTP_AGENT_LLM_BASE_URL", tt.baseURL)
			}

			app, err := NewApp(DefaultConfigFromEnv())
			if err != nil {
				t.Fatalf("NewApp() error = %v", err)
			}
			if got := llm.Label(app.Agent.LLM); got != "FallbackAdapter(minimal.MinimalLLM)" {
				t.Fatalf("LLM label = %q, want fallback adapter around primary minimal LLM", got)
			}
		})
	}
}

func TestDefaultConfigFromEnvAcceptsReferenceLLMFallbackProviders(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		envKey   string
		envValue string
		model    string
	}{
		{name: "aws", provider: "aws", envKey: "AWS_REGION", envValue: "us-west-2"},
		{name: "cerebras", provider: "cerebras", envKey: "CEREBRAS_API_KEY", envValue: "test-cerebras-key"},
		{name: "fireworks", provider: "fireworks", envKey: "FIREWORKS_API_KEY", envValue: "test-fireworks-key"},
		{name: "anthropic", provider: "anthropic", envKey: "ANTHROPIC_API_KEY", envValue: "test-anthropic-key"},
		{name: "google", provider: "google", envKey: "GOOGLE_API_KEY", envValue: "test-google-key"},
		{name: "baseten", provider: "baseten", envKey: "BASETEN_API_KEY", envValue: "test-baseten-key"},
		{name: "fal", provider: "fal", envKey: "FAL_KEY", envValue: "test-fal-key"},
		{name: "gradium", provider: "gradium", envKey: "GRADIUM_API_KEY", envValue: "test-gradium-key"},
		{name: "hedra", provider: "hedra", envKey: "HEDRA_API_KEY", envValue: "test-hedra-key"},
		{name: "hume", provider: "hume", envKey: "HUME_API_KEY", envValue: "test-hume-key"},
		{name: "inworld", provider: "inworld", envKey: "INWORLD_API_KEY", envValue: "test-inworld-key"},
		{name: "langchain", provider: "langchain", envKey: "LANGCHAIN_API_KEY", envValue: "test-langchain-key"},
		{name: "lemonslice", provider: "lemonslice", envKey: "LEMONSLICE_API_KEY", envValue: "test-lemonslice-key"},
		{name: "minimax", provider: "minimax", envKey: "MINIMAX_API_KEY", envValue: "test-minimax-key"},
		{name: "mistralai", provider: "mistralai", envKey: "MISTRAL_API_KEY", envValue: "test-mistral-key"},
		{name: "nvidia", provider: "nvidia", envKey: "NVIDIA_API_KEY", envValue: "test-nvidia-key"},
		{name: "perplexity", provider: "perplexity", envKey: "PERPLEXITY_API_KEY", envValue: "test-perplexity-key"},
		{name: "sarvam", provider: "sarvam", envKey: "SARVAM_API_KEY", envValue: "test-sarvam-key", model: "sarvam-m"},
		{name: "simli", provider: "simli", envKey: "SIMLI_API_KEY", envValue: "test-simli-key"},
		{name: "simplismart", provider: "simplismart", envKey: "SIMPLISMART_API_KEY", envValue: "test-simplismart-key"},
		{name: "smallestai", provider: "smallestai", envKey: "SMALLESTAI_API_KEY", envValue: "test-smallestai-key"},
		{name: "telnyx", provider: "telnyx", envKey: "TELNYX_API_KEY", envValue: "test-telnyx-key"},
		{name: "trugen", provider: "trugen", envKey: "TRUGEN_API_KEY", envValue: "test-trugen-key"},
		{name: "upliftai", provider: "upliftai", envKey: "UPLIFTAI_API_KEY", envValue: "test-upliftai-key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("RTP_AGENT_LLM_PROVIDER", "minimal")
			t.Setenv("RTP_AGENT_LLM_FALLBACK_PROVIDERS", tt.provider)
			model := tt.model
			if model == "" {
				model = "custom-fallback-model"
			}
			t.Setenv("RTP_AGENT_LLM_MODEL", model)
			t.Setenv(tt.envKey, tt.envValue)

			app, err := NewApp(DefaultConfigFromEnv())
			if err != nil {
				t.Fatalf("NewApp() error = %v", err)
			}
			if got := llm.Label(app.Agent.LLM); got != "FallbackAdapter(minimal.MinimalLLM)" {
				t.Fatalf("LLM label = %q, want fallback adapter around primary minimal LLM", got)
			}
		})
	}
}

func TestDefaultConfigFromEnvConfiguresLLMChatOptions(t *testing.T) {
	t.Setenv("RTP_AGENT_LLM_PARALLEL_TOOL_CALLS", "true")
	t.Setenv("RTP_AGENT_LLM_JSON_CONFIG", "temperature=0.2")
	t.Setenv("RTP_AGENT_LLM_RESPONSE_FORMAT", "type=json_object")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session.Options.LLMParallelToolCalls == nil || !*app.Session.Options.LLMParallelToolCalls {
		t.Fatalf("LLMParallelToolCalls = %#v, want true", app.Session.Options.LLMParallelToolCalls)
	}
	if got := app.Session.Options.LLMExtraParams["temperature"]; got != 0.2 {
		t.Fatalf("LLMExtraParams[temperature] = %#v, want 0.2", got)
	}
	if got := app.Session.Options.LLMResponseFormat["type"]; got != "json_object" {
		t.Fatalf("LLMResponseFormat[type] = %#v, want json_object", got)
	}
}

func TestDefaultConfigFromEnvRestoresInitialChatContext(t *testing.T) {
	t.Setenv("RTP_AGENT_CHAT_CONTEXT_JSON", `{"items":[{"id":"seed-user","type":"message","role":"user","content":["hello from history"]}]}`)

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}

	for _, item := range app.Session.ChatCtx.Items {
		message, ok := item.(*llm.ChatMessage)
		if ok && message.ID == "seed-user" && message.TextContent() == "hello from history" {
			return
		}
	}
	t.Fatalf("session chat context items = %#v, want restored seed-user message", app.Session.ChatCtx.Items)
}

func TestDefaultConfigFromEnvWrapsSTTFallbackProviders(t *testing.T) {
	t.Setenv("RTP_AGENT_STT_PROVIDER", "deepgram")
	t.Setenv("RTP_AGENT_STT_FALLBACK_PROVIDERS", "slng")
	t.Setenv("SLNG_API_KEY", "test-slng-key")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.STT.Label(); got != "FallbackAdapter(deepgram.STT)" {
		t.Fatalf("STT label = %q, want fallback adapter around primary deepgram STT", got)
	}
}

func TestDefaultConfigFromEnvAcceptsLiveKitSTTFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_STT_PROVIDER", "deepgram")
	t.Setenv("RTP_AGENT_STT_FALLBACK_PROVIDERS", "livekit")
	t.Setenv("RTP_AGENT_STT_MODEL", "deepgram/nova-3")
	t.Setenv("LIVEKIT_INFERENCE_API_KEY", "test-livekit-key")
	t.Setenv("LIVEKIT_INFERENCE_API_SECRET", "test-livekit-secret")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.STT.Label(); got != "FallbackAdapter(deepgram.STT)" {
		t.Fatalf("STT label = %q, want fallback adapter around primary deepgram STT", got)
	}
}

func TestDefaultConfigFromEnvAcceptsAWSSTTFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_STT_PROVIDER", "deepgram")
	t.Setenv("RTP_AGENT_STT_FALLBACK_PROVIDERS", "aws")
	t.Setenv("AWS_REGION", "us-west-2")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.STT.Label(); got != "FallbackAdapter(deepgram.STT)" {
		t.Fatalf("STT label = %q, want fallback adapter around primary deepgram STT", got)
	}
}

func TestDefaultConfigFromEnvAcceptsAzureSTTFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_STT_PROVIDER", "deepgram")
	t.Setenv("RTP_AGENT_STT_FALLBACK_PROVIDERS", "azure")
	t.Setenv("AZURE_SPEECH_KEY", "test-azure-key")
	t.Setenv("AZURE_SPEECH_REGION", "eastus")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.STT.Label(); got != "FallbackAdapter(deepgram.STT)" {
		t.Fatalf("STT label = %q, want fallback adapter around primary deepgram STT", got)
	}
}

func TestDefaultConfigFromEnvAcceptsFalSTTFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_STT_PROVIDER", "deepgram")
	t.Setenv("RTP_AGENT_STT_FALLBACK_PROVIDERS", "fal")
	t.Setenv("RTP_AGENT_VAD_PROVIDER", "silero")
	t.Setenv("FAL_KEY", "test-fal-key")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.STT.Label(); got != "FallbackAdapter(deepgram.STT)" {
		t.Fatalf("STT label = %q, want fallback adapter around primary deepgram STT", got)
	}
}

func TestDefaultConfigFromEnvAcceptsSpitchSTTFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_STT_PROVIDER", "deepgram")
	t.Setenv("RTP_AGENT_STT_FALLBACK_PROVIDERS", "spitch")
	t.Setenv("RTP_AGENT_VAD_PROVIDER", "silero")
	t.Setenv("SPITCH_API_KEY", "test-spitch-key")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.STT.Label(); got != "FallbackAdapter(deepgram.STT)" {
		t.Fatalf("STT label = %q, want fallback adapter around primary deepgram STT", got)
	}
}

func TestDefaultConfigFromEnvAcceptsOVHCloudSTTFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_STT_PROVIDER", "deepgram")
	t.Setenv("RTP_AGENT_STT_FALLBACK_PROVIDERS", "ovhcloud")
	t.Setenv("RTP_AGENT_VAD_PROVIDER", "silero")
	t.Setenv("OVHCLOUD_API_KEY", "test-ovhcloud-key")
	t.Setenv("RTP_AGENT_STT_MODEL", "custom-ovhcloud-stt")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.STT.Label(); got != "FallbackAdapter(deepgram.STT)" {
		t.Fatalf("STT label = %q, want fallback adapter around primary deepgram STT", got)
	}
}

func TestDefaultConfigFromEnvWrapsNonStreamingSTTFallbackWithVAD(t *testing.T) {
	t.Setenv("RTP_AGENT_STT_PROVIDER", "deepgram")
	t.Setenv("RTP_AGENT_STT_FALLBACK_PROVIDERS", "elevenlabs")
	t.Setenv("RTP_AGENT_VAD_PROVIDER", "silero")
	t.Setenv("ELEVENLABS_API_KEY", "test-elevenlabs-key")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.STT.Label(); got != "FallbackAdapter(deepgram.STT)" {
		t.Fatalf("STT label = %q, want fallback adapter around primary deepgram STT", got)
	}
	if app.Session.VAD == nil {
		t.Fatal("Session VAD is nil")
	}
}

func TestElevenLabsSTTFallbackPassesReferenceKeyterms(t *testing.T) {
	tagAudioEvents := false
	provider, err := fallbackSTTFromProvider(AppConfig{
		ElevenLabsAPIKey:  "test-eleven-key",
		STTBaseURL:        "https://eleven.example/v1",
		STTModel:          "scribe_v2",
		STTLanguage:       "en",
		STTTagAudioEvents: &tagAudioEvents,
		STTKeytermsPrompt: []string{"LiveKit", "Cavos"},
	}, providerElevenLabs)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	elevenProvider, ok := provider.(*elevenlabs.ElevenLabsSTT)
	if !ok {
		t.Fatalf("provider type = %T, want *elevenlabs.ElevenLabsSTT", provider)
	}
	if got, want := provider.Label(), "elevenlabs.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "scribe_v2"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); caps.Streaming || !caps.InterimResults || !caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want offline recognize interim fallback", caps)
	}
	state := reflect.ValueOf(elevenProvider).Elem()
	if got, want := state.FieldByName("apiKey").String(), "test-eleven-key"; got != want {
		t.Fatalf("apiKey = %q, want %q", got, want)
	}
	if got, want := state.FieldByName("baseURL").String(), "https://eleven.example/v1"; got != want {
		t.Fatalf("baseURL = %q, want %q", got, want)
	}
	if got, want := state.FieldByName("languageCode").String(), "en"; got != want {
		t.Fatalf("languageCode = %q, want %q", got, want)
	}
	if got := state.FieldByName("tagAudioEvents").Bool(); got {
		t.Fatalf("tagAudioEvents = %v, want false", got)
	}
	keytermsField := state.FieldByName("keyterms")
	gotKeyterms := make([]string, 0, keytermsField.Len())
	for i := 0; i < keytermsField.Len(); i++ {
		gotKeyterms = append(gotKeyterms, keytermsField.Index(i).String())
	}
	if want := []string{"LiveKit", "Cavos"}; !reflect.DeepEqual(gotKeyterms, want) {
		t.Fatalf("keyterms = %#v, want %#v", gotKeyterms, want)
	}
}

func TestElevenLabsSTTFallbackPassesReferenceServerVAD(t *testing.T) {
	vadThreshold := 0.42
	vadSilence := 0.8
	minSpeech := 120
	minSilence := 900
	includeTimestamps := true
	provider, err := fallbackSTTFromProvider(AppConfig{
		ElevenLabsAPIKey:              "test-eleven-key",
		STTModel:                      "scribe_v2_realtime",
		STTLanguage:                   "en",
		STTVADThreshold:               &vadThreshold,
		STTVADSilenceThresholdSeconds: &vadSilence,
		STTMinTurnSilence:             &minSpeech,
		STTMaxTurnSilence:             &minSilence,
		STTIncludeTimestamps:          &includeTimestamps,
	}, providerElevenLabs)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	elevenProvider, ok := provider.(*elevenlabs.ElevenLabsSTT)
	if !ok {
		t.Fatalf("provider type = %T, want *elevenlabs.ElevenLabsSTT", provider)
	}
	if caps := provider.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.AlignedTranscript != "word" || caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want streaming word-aligned interim fallback", caps)
	}
	serverVAD := reflect.ValueOf(elevenProvider).Elem().FieldByName("serverVAD")
	if serverVAD.IsNil() {
		t.Fatal("serverVAD is nil, want reference VAD options")
	}
	vad := serverVAD.Elem()
	if got, want := vad.FieldByName("VADThreshold").Elem().Float(), vadThreshold; got != want {
		t.Fatalf("VADThreshold = %v, want %v", got, want)
	}
	if got, want := vad.FieldByName("VADSilenceThresholdSecs").Elem().Float(), vadSilence; got != want {
		t.Fatalf("VADSilenceThresholdSecs = %v, want %v", got, want)
	}
	if got, want := int(vad.FieldByName("MinSpeechDurationMS").Elem().Int()), minSpeech; got != want {
		t.Fatalf("MinSpeechDurationMS = %v, want %v", got, want)
	}
	if got, want := int(vad.FieldByName("MinSilenceDurationMS").Elem().Int()), minSilence; got != want {
		t.Fatalf("MinSilenceDurationMS = %v, want %v", got, want)
	}
}

func TestGradiumSTTFallbackPassesReferenceOptions(t *testing.T) {
	type wsRecord struct {
		apiKey    string
		apiSource string
		setup     map[string]any
	}
	records := make(chan wsRecord, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read gradium stt setup payload: %v", err)
			return
		}
		var setup map[string]any
		if err := json.Unmarshal(payload, &setup); err != nil {
			t.Errorf("decode gradium stt setup payload: %v", err)
			return
		}
		records <- wsRecord{
			apiKey:    r.Header.Get("x-api-key"),
			apiSource: r.Header.Get("x-api-source"),
			setup:     setup,
		}
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	temperature := 0.35
	vadBucket := 4
	vadFlush := false
	bufferSizeSeconds := 0.12
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	provider, err := fallbackSTTFromProvider(AppConfig{
		GradiumAPIKey:        "test-gradium-key",
		STTBaseURL:           endpoint,
		STTModel:             "asr-test",
		STTLanguage:          "en",
		STTTemperature:       &temperature,
		STTVADBucket:         &vadBucket,
		STTVADFlush:          &vadFlush,
		STTBufferSizeSeconds: &bufferSizeSeconds,
	}, providerGradium)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	if _, ok := provider.(*gradium.GradiumSTT); !ok {
		t.Fatalf("provider type = %T, want *gradium.GradiumSTT", provider)
	}
	if got, want := provider.Label(), "gradium.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "unknown"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "Gradium"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want streaming interim-only", caps)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case record := <-records:
		if got, want := record.apiKey, "test-gradium-key"; got != want {
			t.Fatalf("x-api-key = %q, want %q", got, want)
		}
		if got, want := record.apiSource, "livekit"; got != want {
			t.Fatalf("x-api-source = %q, want %q", got, want)
		}
		if got, want := record.setup["type"], "setup"; got != want {
			t.Fatalf("setup.type = %#v, want %#v", got, want)
		}
		if got, want := record.setup["model_name"], "asr-test"; got != want {
			t.Fatalf("setup.model_name = %#v, want %#v", got, want)
		}
		if got, want := record.setup["input_format"], "pcm"; got != want {
			t.Fatalf("setup.input_format = %#v, want %#v", got, want)
		}
		config, _ := record.setup["json_config"].(map[string]any)
		if got, want := config["language"], "en"; got != want {
			t.Fatalf("json_config.language = %#v, want %#v", got, want)
		}
		if got, want := config["temp"], 0.35; got != want {
			t.Fatalf("json_config.temp = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Gradium STT setup payload")
	}
}

func TestBasetenSTTFallbackPassesReferenceOptions(t *testing.T) {
	t.Setenv("BASETEN_API_KEY", "test-baseten-key")
	type wsRecord struct {
		authorization string
		metadata      map[string]any
	}
	records := make(chan wsRecord, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read baseten stt metadata: %v", err)
			return
		}
		var metadata map[string]any
		if err := json.Unmarshal(payload, &metadata); err != nil {
			t.Errorf("decode baseten stt metadata: %v", err)
			return
		}
		records <- wsRecord{
			authorization: r.Header.Get("Authorization"),
			metadata:      metadata,
		}
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	sampleRate := 8000
	bufferSizeSeconds := 0.064
	vadThreshold := 0.7
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	provider, err := fallbackSTTFromProvider(AppConfig{
		STTBaseURL:           endpoint,
		STTLanguage:          "auto",
		STTEncoding:          "pcm_mulaw",
		STTSampleRate:        &sampleRate,
		STTBufferSizeSeconds: &bufferSizeSeconds,
		STTVADThreshold:      &vadThreshold,
	}, providerBaseten)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	if _, ok := provider.(*baseten.BasetenSTT); !ok {
		t.Fatalf("provider type = %T, want *baseten.BasetenSTT", provider)
	}
	if got, want := provider.Label(), "baseten.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "unknown"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "Baseten"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.AlignedTranscript != "word" || caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want streaming word-aligned interim-only", caps)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case record := <-records:
		if got, want := record.authorization, "Api-Key test-baseten-key"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		whisper, _ := record.metadata["whisper_params"].(map[string]any)
		if got, want := whisper["audio_language"], "auto"; got != want {
			t.Fatalf("whisper.audio_language = %#v, want %#v", got, want)
		}
		if got, want := whisper["show_word_timestamps"], true; got != want {
			t.Fatalf("whisper.show_word_timestamps = %#v, want %#v", got, want)
		}
		streaming, _ := record.metadata["streaming_params"].(map[string]any)
		if got, want := streaming["encoding"], "pcm_mulaw"; got != want {
			t.Fatalf("streaming.encoding = %#v, want %#v", got, want)
		}
		if got, want := streaming["sample_rate"], float64(8000); got != want {
			t.Fatalf("streaming.sample_rate = %#v, want %#v", got, want)
		}
		if got, want := streaming["enable_partial_transcripts"], true; got != want {
			t.Fatalf("streaming.enable_partial_transcripts = %#v, want %#v", got, want)
		}
		if got, want := streaming["partial_transcript_interval_s"], float64(1); got != want {
			t.Fatalf("streaming.partial_transcript_interval_s = %#v, want %#v", got, want)
		}
		vad, _ := record.metadata["streaming_vad_config"].(map[string]any)
		if got, want := vad["threshold"], 0.7; got != want {
			t.Fatalf("vad.threshold = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Baseten STT metadata")
	}
}

func TestCartesiaSTTFallbackPassesReferenceOptions(t *testing.T) {
	t.Setenv("CARTESIA_API_KEY", "test-cartesia-key")
	type wsRecord struct {
		path       string
		query      map[string]string
		apiKey     string
		apiVersion string
	}
	records := make(chan wsRecord, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		query := map[string]string{}
		for key, values := range r.URL.Query() {
			if len(values) > 0 {
				query[key] = values[0]
			}
		}
		records <- wsRecord{
			path:       r.URL.Path,
			query:      query,
			apiKey:     r.Header.Get("X-API-Key"),
			apiVersion: r.Header.Get("Cartesia-Version"),
		}
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	sampleRate := 8000
	chunkDurationMS := 120
	provider, err := fallbackSTTFromProvider(AppConfig{
		STTBaseURL:              server.URL,
		STTModel:                "ink-2",
		STTLanguage:             "en",
		STTEncoding:             "pcm_mulaw",
		STTSampleRate:           &sampleRate,
		STTAudioChunkDurationMS: &chunkDurationMS,
	}, providerCartesia)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	if _, ok := provider.(*cartesia.CartesiaSTT); !ok {
		t.Fatalf("provider type = %T, want *cartesia.CartesiaSTT", provider)
	}
	if got, want := provider.Label(), "cartesia.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "ink-2"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "Cartesia"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.AlignedTranscript != "" || caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want streaming interim without alignment", caps)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case record := <-records:
		if got, want := record.path, "/stt/turns/websocket"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		if got, want := record.query["model"], "ink-2"; got != want {
			t.Fatalf("query model = %q, want %q", got, want)
		}
		if got, want := record.query["sample_rate"], "8000"; got != want {
			t.Fatalf("query sample_rate = %q, want %q", got, want)
		}
		if got, want := record.query["encoding"], "pcm_mulaw"; got != want {
			t.Fatalf("query encoding = %q, want %q", got, want)
		}
		if got, want := record.apiKey, "test-cartesia-key"; got != want {
			t.Fatalf("X-API-Key = %q, want %q", got, want)
		}
		if got, want := record.apiVersion, "2025-04-16"; got != want {
			t.Fatalf("Cartesia-Version = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Cartesia STT websocket request")
	}
}

func TestFireworksSTTFallbackPassesReferenceOptions(t *testing.T) {
	t.Setenv("FIREWORKS_API_KEY", "test-fireworks-key")
	type wsRecord struct {
		path          string
		query         map[string][]string
		authorization string
	}
	records := make(chan wsRecord, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		query := map[string][]string{}
		for key, values := range r.URL.Query() {
			query[key] = append([]string(nil), values...)
		}
		records <- wsRecord{
			path:          r.URL.Path,
			query:         query,
			authorization: r.Header.Get("Authorization"),
		}
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	temperature := 0.2
	skipVAD := true
	textTimeoutSeconds := 2.5
	provider, err := fallbackSTTFromProvider(AppConfig{
		STTBaseURL:                "ws" + strings.TrimPrefix(server.URL, "http"),
		STTModel:                  "whisper-v3",
		STTLanguage:               "en",
		STTPrompt:                 "domain prompt",
		STTTemperature:            &temperature,
		STTSkipVAD:                &skipVAD,
		STTVADKwargs:              map[string]any{"threshold": "0.15"},
		STTTextTimeoutSeconds:     &textTimeoutSeconds,
		STTTimestampGranularities: []string{"word", "segment"},
	}, providerFireworks)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	if _, ok := provider.(*fireworksai.FireworksSTT); !ok {
		t.Fatalf("provider type = %T, want *fireworksai.FireworksSTT", provider)
	}
	if got, want := provider.Label(), "fireworks.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "whisper-v3"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "FireworksAI"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.AlignedTranscript != "" || caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want streaming interim-only", caps)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case record := <-records:
		query := record.query
		if got, want := record.path, "/audio_streaming"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		if got, want := record.authorization, "test-fireworks-key"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		if got, want := firstQueryValue(query, "model"), "whisper-v3"; got != want {
			t.Fatalf("query model = %q, want %q", got, want)
		}
		if got, want := firstQueryValue(query, "language"), "en"; got != want {
			t.Fatalf("query language = %q, want %q", got, want)
		}
		if got, want := firstQueryValue(query, "prompt"), "domain prompt"; got != want {
			t.Fatalf("query prompt = %q, want %q", got, want)
		}
		if got, want := firstQueryValue(query, "temperature"), "0.2"; got != want {
			t.Fatalf("query temperature = %q, want %q", got, want)
		}
		if got, want := firstQueryValue(query, "skip_vad"), "true"; got != want {
			t.Fatalf("query skip_vad = %q, want %q", got, want)
		}
		if got, want := firstQueryValue(query, "vad_kwargs"), `{"threshold":"0.15"}`; got != want {
			t.Fatalf("query vad_kwargs = %q, want %q", got, want)
		}
		if got, want := firstQueryValue(query, "text_timeout_seconds"), "2.5"; got != want {
			t.Fatalf("query text_timeout_seconds = %q, want %q", got, want)
		}
		if got, want := firstQueryValue(query, "response_format"), "verbose_json"; got != want {
			t.Fatalf("query response_format = %q, want %q", got, want)
		}
		if got, want := strings.Join(query["timestamp_granularities"], ","), "word,segment"; got != want {
			t.Fatalf("query timestamp_granularities = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Fireworks STT websocket request")
	}
}

func TestSonioxSTTFallbackPassesReferenceOptions(t *testing.T) {
	t.Setenv("SONIOX_API_KEY", "test-soniox-key")
	records := make(chan map[string]any, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read soniox config: %v", err)
			return
		}
		var config map[string]any
		if err := json.Unmarshal(payload, &config); err != nil {
			t.Errorf("decode soniox config: %v", err)
			return
		}
		records <- config
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	numChannels := 2
	sampleRate := 8000
	diarization := true
	languageDetection := false
	endpointingMS := 750
	provider, err := fallbackSTTFromProvider(AppConfig{
		STTBaseURL:                    "ws" + strings.TrimPrefix(server.URL, "http"),
		STTModel:                      "stt-rt-v4",
		STTLanguageOptions:            "en,es",
		STTNumberOfChannels:           &numChannels,
		STTSampleRate:                 &sampleRate,
		STTDiarization:                &diarization,
		STTLanguageDetection:          &languageDetection,
		STTEndpointingMS:              &endpointingMS,
		STTSessionID:                  "client-1",
		STTPrompt:                     "domain terms",
		STTTranslationSourceLanguages: []string{"en"},
		STTTranslationTargetLanguages: []string{"es"},
	}, providerSoniox)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	if _, ok := provider.(*soniox.SonioxSTT); !ok {
		t.Fatalf("provider type = %T, want *soniox.SonioxSTT", provider)
	}
	if got, want := provider.Label(), "soniox.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "stt-rt-v4"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "Soniox"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || !caps.InterimResults || !caps.Diarization || caps.AlignedTranscript != "chunk" || caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want streaming interim diarization chunk-aligned", caps)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case config := <-records:
		if got, want := config["api_key"], "test-soniox-key"; got != want {
			t.Fatalf("config api_key = %#v, want %#v", got, want)
		}
		if got, want := config["model"], "stt-rt-v4"; got != want {
			t.Fatalf("config model = %#v, want %#v", got, want)
		}
		if got, want := fmt.Sprint(config["language_hints"]), "[en es]"; got != want {
			t.Fatalf("config language_hints = %#v, want %q", config["language_hints"], want)
		}
		if got, want := config["num_channels"], float64(2); got != want {
			t.Fatalf("config num_channels = %#v, want %#v", got, want)
		}
		if got, want := config["sample_rate"], float64(8000); got != want {
			t.Fatalf("config sample_rate = %#v, want %#v", got, want)
		}
		if got, want := config["enable_speaker_diarization"], true; got != want {
			t.Fatalf("config enable_speaker_diarization = %#v, want %#v", got, want)
		}
		if got, want := config["enable_language_identification"], false; got != want {
			t.Fatalf("config enable_language_identification = %#v, want %#v", got, want)
		}
		if got, want := config["client_reference_id"], "client-1"; got != want {
			t.Fatalf("config client_reference_id = %#v, want %#v", got, want)
		}
		if got, want := config["max_endpoint_delay_ms"], float64(750); got != want {
			t.Fatalf("config max_endpoint_delay_ms = %#v, want %#v", got, want)
		}
		if got, want := config["context"], "domain terms"; got != want {
			t.Fatalf("config context = %#v, want %#v", got, want)
		}
		translation, _ := config["translation"].(map[string]any)
		if got, want := translation["type"], "two_way"; got != want {
			t.Fatalf("translation type = %#v, want %#v", got, want)
		}
		if got, want := translation["language_a"], "en"; got != want {
			t.Fatalf("translation language_a = %#v, want %#v", got, want)
		}
		if got, want := translation["language_b"], "es"; got != want {
			t.Fatalf("translation language_b = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Soniox STT config")
	}
}

func TestSonioxSTTFallbackUsesReferenceLanguageHintFallback(t *testing.T) {
	provider, err := fallbackSTTFromProvider(AppConfig{
		SonioxAPIKey: "test-soniox-key",
		STTLanguage:  "id",
	}, providerSoniox)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	sonioxProvider, ok := provider.(*soniox.SonioxSTT)
	if !ok {
		t.Fatalf("provider type = %T, want *soniox.SonioxSTT", provider)
	}
	state := reflect.ValueOf(sonioxProvider).Elem()
	languageHints := state.FieldByName("languageHints")
	got := make([]string, 0, languageHints.Len())
	for i := 0; i < languageHints.Len(); i++ {
		got = append(got, languageHints.Index(i).String())
	}
	if want := []string{"id"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("languageHints = %#v, want %#v", got, want)
	}
}

func TestSpeechmaticsSTTFallbackPassesReferenceOptions(t *testing.T) {
	t.Setenv("SPEECHMATICS_API_KEY", "test-speechmatics-key")
	type wsRecord struct {
		authorization string
		message       map[string]any
	}
	records := make(chan wsRecord, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read speechmatics start message: %v", err)
			return
		}
		var message map[string]any
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Errorf("decode speechmatics start message: %v", err)
			return
		}
		records <- wsRecord{
			authorization: r.Header.Get("Authorization"),
			message:       message,
		}
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	sampleRate := 8000
	includePartials := false
	diarization := true
	maxDelay := 1.25
	silenceTrigger := 0.55
	maxUtteranceDelay := 2.5
	maxSpeakers := 3
	preferCurrentSpeaker := true
	provider, err := fallbackSTTFromProvider(AppConfig{
		STTBaseURL:                              "ws" + strings.TrimPrefix(server.URL, "http"),
		STTLanguage:                             "en",
		STTSampleRate:                           &sampleRate,
		STTEncoding:                             "pcm_mulaw",
		STTDomain:                               "finance",
		STTOutputLocale:                         "en-GB",
		STTInterimResults:                       &includePartials,
		STTDiarization:                          &diarization,
		STTKeytermsPrompt:                       []string{"LiveKit:live kit|livekit"},
		STTModelOptions:                         map[string]any{"focus_speakers": "agent|user", "ignore_speakers": "noise", "focus_mode": "retain", "known_speakers": "agent:spk-1", "speaker_sensitivity": "0.7"},
		STTOperatingPoint:                       "enhanced",
		STTTextTimeoutSeconds:                   &maxDelay,
		STTVADSilenceThresholdSeconds:           &silenceTrigger,
		STTMaxDurationWithoutEndpointingSeconds: &maxUtteranceDelay,
		STTMaxSpeakers:                          &maxSpeakers,
		STTPreferCurrentSpeaker:                 &preferCurrentSpeaker,
	}, providerSpeechmatics)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	if _, ok := provider.(*speechmatics.SpeechmaticsSTT); !ok {
		t.Fatalf("provider type = %T, want *speechmatics.SpeechmaticsSTT", provider)
	}
	if got, want := provider.Label(), "speechmatics.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "enhanced"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "Speechmatics"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || !caps.InterimResults || !caps.Diarization || caps.AlignedTranscript != "chunk" || caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want streaming interim diarization chunk-aligned", caps)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case record := <-records:
		if got, want := record.authorization, "Bearer test-speechmatics-key"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		message := record.message
		if got, want := message["message"], "StartRecognition"; got != want {
			t.Fatalf("message = %#v, want %#v", got, want)
		}
		audioFormat, _ := message["audio_format"].(map[string]any)
		if got, want := audioFormat["encoding"], "pcm_mulaw"; got != want {
			t.Fatalf("audio encoding = %#v, want %#v", got, want)
		}
		if got, want := audioFormat["sample_rate"], float64(8000); got != want {
			t.Fatalf("audio sample_rate = %#v, want %#v", got, want)
		}
		config, _ := message["transcription_config"].(map[string]any)
		if got, want := config["language"], "en"; got != want {
			t.Fatalf("language = %#v, want %#v", got, want)
		}
		if got, want := config["domain"], "finance"; got != want {
			t.Fatalf("domain = %#v, want %#v", got, want)
		}
		if got, want := config["output_locale"], "en-GB"; got != want {
			t.Fatalf("output_locale = %#v, want %#v", got, want)
		}
		if got, want := config["enable_partials"], false; got != want {
			t.Fatalf("enable_partials = %#v, want %#v", got, want)
		}
		if got, want := config["diarization"], "speaker"; got != want {
			t.Fatalf("diarization = %#v, want %#v", got, want)
		}
		if got, want := config["operating_point"], "enhanced"; got != want {
			t.Fatalf("operating_point = %#v, want %#v", got, want)
		}
		if got, want := config["max_delay"], 1.25; got != want {
			t.Fatalf("max_delay = %#v, want %#v", got, want)
		}
		if got, want := config["end_of_utterance_silence_trigger"], 0.55; got != want {
			t.Fatalf("end_of_utterance_silence_trigger = %#v, want %#v", got, want)
		}
		if got, want := config["end_of_utterance_max_delay"], 2.5; got != want {
			t.Fatalf("end_of_utterance_max_delay = %#v, want %#v", got, want)
		}
		if got, want := config["speaker_sensitivity"], 0.7; got != want {
			t.Fatalf("speaker_sensitivity = %#v, want %#v", got, want)
		}
		if got, want := config["max_speakers"], float64(3); got != want {
			t.Fatalf("max_speakers = %#v, want %#v", got, want)
		}
		if got, want := config["prefer_current_speaker"], true; got != want {
			t.Fatalf("prefer_current_speaker = %#v, want %#v", got, want)
		}
		vocab, _ := config["additional_vocab"].([]any)
		if len(vocab) != 1 {
			t.Fatalf("additional_vocab length = %d, want 1", len(vocab))
		}
		firstVocab, _ := vocab[0].(map[string]any)
		if got, want := firstVocab["content"], "LiveKit"; got != want {
			t.Fatalf("vocab content = %#v, want %#v", got, want)
		}
		if got, want := fmt.Sprint(firstVocab["sounds_like"]), "[live kit livekit]"; got != want {
			t.Fatalf("vocab sounds_like = %#v, want %q", firstVocab["sounds_like"], want)
		}
		speakerConfig, _ := config["speaker_config"].(map[string]any)
		if got, want := fmt.Sprint(speakerConfig["focus_speakers"]), "[agent user]"; got != want {
			t.Fatalf("focus_speakers = %#v, want %q", speakerConfig["focus_speakers"], want)
		}
		if got, want := fmt.Sprint(speakerConfig["ignore_speakers"]), "[noise]"; got != want {
			t.Fatalf("ignore_speakers = %#v, want %q", speakerConfig["ignore_speakers"], want)
		}
		if got, want := speakerConfig["focus_mode"], "retain"; got != want {
			t.Fatalf("focus_mode = %#v, want %#v", got, want)
		}
		knownSpeakers, _ := config["known_speakers"].([]any)
		if len(knownSpeakers) != 1 {
			t.Fatalf("known_speakers length = %d, want 1", len(knownSpeakers))
		}
		knownSpeaker, _ := knownSpeakers[0].(map[string]any)
		if got, want := knownSpeaker["label"], "agent"; got != want {
			t.Fatalf("known speaker label = %#v, want %#v", got, want)
		}
		if got, want := knownSpeaker["speaker_id"], "spk-1"; got != want {
			t.Fatalf("known speaker id = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Speechmatics STT start message")
	}
}

func TestGladiaSTTFallbackPassesReferenceOptions(t *testing.T) {
	t.Setenv("GLADIA_API_KEY", "test-gladia-key")
	type initRecord struct {
		apiKey string
		region string
		body   map[string]any
	}
	records := make(chan initRecord, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade websocket: %v", err)
				return
			}
			defer conn.Close()
			_, _, _ = conn.ReadMessage()
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode gladia init body: %v", err)
			return
		}
		records <- initRecord{
			apiKey: r.Header.Get("X-Gladia-Key"),
			region: r.URL.Query().Get("region"),
			body:   body,
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"session-1","url":"ws://%s/ws"}`, r.Host)
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	interimResults := false
	codeSwitching := true
	sampleRate := 8000
	bitDepth := 16
	channels := 2
	endpointing := 0.1
	maxDuration := 4.0
	matchOriginal := true
	lipsync := true
	contextAdaptation := true
	informal := true
	audioEnhancer := true
	speechThreshold := 0.7
	provider, err := fallbackSTTFromProvider(AppConfig{
		STTBaseURL:                              server.URL,
		STTModel:                                "solaria-1",
		STTInterimResults:                       &interimResults,
		STTLanguageOptions:                      "en,fr",
		STTCodeSwitching:                        &codeSwitching,
		STTSampleRate:                           &sampleRate,
		STTBitDepth:                             &bitDepth,
		STTNumberOfChannels:                     &channels,
		STTEncoding:                             "wav/ulaw",
		STTEndpointingSeconds:                   &endpointing,
		STTMaxDurationWithoutEndpointingSeconds: &maxDuration,
		STTRegion:                               "us-west",
		STTCustomVocabulary:                     []any{"LiveKit", map[string]any{"value": "Agents"}},
		STTCustomSpelling:                       map[string][]string{"livekit": []string{"live kit", "live-kit"}},
		STTTranslationTargetLanguages:           []string{"es", "de"},
		STTTranslationModel:                     "base",
		STTTranslationMatchOriginalUtterances:   &matchOriginal,
		STTTranslationLipsync:                   &lipsync,
		STTTranslationContextAdaptation:         &contextAdaptation,
		STTTranslationContext:                   "support call",
		STTTranslationInformal:                  &informal,
		STTPreProcessingAudioEnhancer:           &audioEnhancer,
		STTPreProcessingSpeechThreshold:         &speechThreshold,
	}, providerGladia)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	if _, ok := provider.(*gladia.GladiaSTT); !ok {
		t.Fatalf("provider type = %T, want *gladia.GladiaSTT", provider)
	}
	if got, want := provider.Label(), "gladia.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "solaria-1"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "Gladia"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.InterimResults || caps.AlignedTranscript != "word" || caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want streaming word-aligned without interim", caps)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case record := <-records:
		if got, want := record.apiKey, "test-gladia-key"; got != want {
			t.Fatalf("X-Gladia-Key = %q, want %q", got, want)
		}
		if got, want := record.region, "us-west"; got != want {
			t.Fatalf("region = %q, want %q", got, want)
		}
		body := record.body
		if got, want := body["model"], "solaria-1"; got != want {
			t.Fatalf("model = %#v, want %#v", got, want)
		}
		if got, want := body["encoding"], "wav/ulaw"; got != want {
			t.Fatalf("encoding = %#v, want %#v", got, want)
		}
		if got, want := body["sample_rate"], float64(8000); got != want {
			t.Fatalf("sample_rate = %#v, want %#v", got, want)
		}
		if got, want := body["bit_depth"], float64(16); got != want {
			t.Fatalf("bit_depth = %#v, want %#v", got, want)
		}
		if got, want := body["channels"], float64(2); got != want {
			t.Fatalf("channels = %#v, want %#v", got, want)
		}
		if got, want := body["endpointing"], 0.1; got != want {
			t.Fatalf("endpointing = %#v, want %#v", got, want)
		}
		if got, want := body["maximum_duration_without_endpointing"], 4.0; got != want {
			t.Fatalf("maximum_duration_without_endpointing = %#v, want %#v", got, want)
		}
		languageConfig, _ := body["language_config"].(map[string]any)
		if got, want := fmt.Sprint(languageConfig["languages"]), "[en fr]"; got != want {
			t.Fatalf("languages = %#v, want %q", languageConfig["languages"], want)
		}
		if got, want := languageConfig["code_switching"], true; got != want {
			t.Fatalf("code_switching = %#v, want %#v", got, want)
		}
		messagesConfig, _ := body["messages_config"].(map[string]any)
		if got, want := messagesConfig["receive_partial_transcripts"], false; got != want {
			t.Fatalf("receive_partial_transcripts = %#v, want %#v", got, want)
		}
		realtime, _ := body["realtime_processing"].(map[string]any)
		if got, want := realtime["custom_vocabulary"], true; got != want {
			t.Fatalf("custom_vocabulary = %#v, want %#v", got, want)
		}
		if got, want := realtime["custom_spelling"], true; got != want {
			t.Fatalf("custom_spelling = %#v, want %#v", got, want)
		}
		if got, want := realtime["translation"], true; got != want {
			t.Fatalf("translation = %#v, want %#v", got, want)
		}
		translationConfig, _ := realtime["translation_config"].(map[string]any)
		if got, want := fmt.Sprint(translationConfig["target_languages"]), "[es de]"; got != want {
			t.Fatalf("target_languages = %#v, want %q", translationConfig["target_languages"], want)
		}
		if got, want := translationConfig["context"], "support call"; got != want {
			t.Fatalf("translation context = %#v, want %#v", got, want)
		}
		if got, want := translationConfig["context_adaptation"], true; got != want {
			t.Fatalf("context_adaptation = %#v, want %#v", got, want)
		}
		if got, want := translationConfig["informal"], true; got != want {
			t.Fatalf("informal = %#v, want %#v", got, want)
		}
		preProcessing, _ := body["pre_processing"].(map[string]any)
		if got, want := preProcessing["audio_enhancer"], true; got != want {
			t.Fatalf("audio_enhancer = %#v, want %#v", got, want)
		}
		if got, want := preProcessing["speech_threshold"], 0.7; got != want {
			t.Fatalf("speech_threshold = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Gladia STT init request")
	}
}

func TestXaiSTTFallbackPassesReferenceOptions(t *testing.T) {
	t.Setenv("XAI_API_KEY", "test-xai-key")
	type wsRecord struct {
		authorization string
		query         map[string]string
	}
	records := make(chan wsRecord, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		query := map[string]string{}
		for key, values := range r.URL.Query() {
			if len(values) > 0 {
				query[key] = values[0]
			}
		}
		records <- wsRecord{
			authorization: r.Header.Get("Authorization"),
			query:         query,
		}
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	sampleRate := 8000
	interimResults := false
	diarization := true
	endpointing := 250
	provider, err := fallbackSTTFromProvider(AppConfig{
		STTStreamingURL:   "ws" + strings.TrimPrefix(server.URL, "http"),
		STTSampleRate:     &sampleRate,
		STTLanguage:       "es",
		STTInterimResults: &interimResults,
		STTDiarization:    &diarization,
		STTEndpointingMS:  &endpointing,
	}, providerXAI)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	if _, ok := provider.(*xai.XaiSTT); !ok {
		t.Fatalf("provider type = %T, want *xai.XaiSTT", provider)
	}
	if got, want := provider.Label(), "xai.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.InterimResults || !caps.Diarization || caps.AlignedTranscript != "word" || !caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want streaming diarization word-aligned offline without interim", caps)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case record := <-records:
		if got, want := record.authorization, "Bearer test-xai-key"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		if got, want := record.query["encoding"], "pcm"; got != want {
			t.Fatalf("query encoding = %q, want %q", got, want)
		}
		if got, want := record.query["sample_rate"], "8000"; got != want {
			t.Fatalf("query sample_rate = %q, want %q", got, want)
		}
		if got, want := record.query["language"], "es"; got != want {
			t.Fatalf("query language = %q, want %q", got, want)
		}
		if got, want := record.query["interim_results"], "false"; got != want {
			t.Fatalf("query interim_results = %q, want %q", got, want)
		}
		if got, want := record.query["diarize"], "true"; got != want {
			t.Fatalf("query diarize = %q, want %q", got, want)
		}
		if got, want := record.query["endpointing"], "250"; got != want {
			t.Fatalf("query endpointing = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for xAI STT websocket request")
	}
}

func TestSmallestAISTTFallbackPassesReferenceOptions(t *testing.T) {
	type wsRecord struct {
		authorization string
		source        string
		path          string
		query         map[string]string
	}
	records := make(chan wsRecord, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		query := map[string]string{}
		for key, values := range r.URL.Query() {
			if len(values) > 0 {
				query[key] = values[0]
			}
		}
		records <- wsRecord{
			authorization: r.Header.Get("Authorization"),
			source:        r.Header.Get("X-Source"),
			path:          r.URL.Path,
			query:         query,
		}
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	sampleRate := 8000
	wordTimestamps := false
	diarization := true
	endpointingMS := 500
	provider, err := fallbackSTTFromProvider(AppConfig{
		SmallestAIAPIKey:  "test-smallest-key",
		STTBaseURL:        server.URL,
		STTModel:          "pulse",
		STTLanguage:       "multi",
		STTSampleRate:     &sampleRate,
		STTEncoding:       "mulaw",
		STTWordTimestamps: &wordTimestamps,
		STTDiarization:    &diarization,
		STTEndpointingMS:  &endpointingMS,
	}, providerSmallestAI)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	if _, ok := provider.(*smallestai.SmallestAISTT); !ok {
		t.Fatalf("provider type = %T, want *smallestai.SmallestAISTT", provider)
	}
	if got, want := provider.Label(), "smallestai.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "pulse"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "SmallestAI"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || !caps.InterimResults || !caps.Diarization || caps.AlignedTranscript != "" || !caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want streaming interim diarization offline without alignment", caps)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case record := <-records:
		if got, want := record.authorization, "Bearer test-smallest-key"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		if got, want := record.source, "livekit"; got != want {
			t.Fatalf("X-Source = %q, want %q", got, want)
		}
		if got, want := record.path, "/pulse/get_text"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		if got, want := record.query["language"], "multi"; got != want {
			t.Fatalf("query language = %q, want %q", got, want)
		}
		if got, want := record.query["encoding"], "mulaw"; got != want {
			t.Fatalf("query encoding = %q, want %q", got, want)
		}
		if got, want := record.query["sample_rate"], "8000"; got != want {
			t.Fatalf("query sample_rate = %q, want %q", got, want)
		}
		if got, want := record.query["word_timestamps"], "false"; got != want {
			t.Fatalf("query word_timestamps = %q, want %q", got, want)
		}
		if got, want := record.query["diarize"], "true"; got != want {
			t.Fatalf("query diarize = %q, want %q", got, want)
		}
		if got, want := record.query["eou_timeout_ms"], "500"; got != want {
			t.Fatalf("query eou_timeout_ms = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SmallestAI STT websocket request")
	}
}

func TestTelnyxSTTFallbackPassesReferenceOptions(t *testing.T) {
	type wsRecord struct {
		authorization string
		query         map[string]string
		header        []byte
	}
	records := make(chan wsRecord, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		query := map[string]string{}
		for key, values := range r.URL.Query() {
			if len(values) > 0 {
				query[key] = values[0]
			}
		}
		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read telnyx wav header: %v", err)
			return
		}
		records <- wsRecord{
			authorization: r.Header.Get("Authorization"),
			query:         query,
			header:        payload,
		}
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	sampleRate := 8000
	provider, err := fallbackSTTFromProvider(AppConfig{
		TelnyxAPIKey:  "test-telnyx-key",
		STTBaseURL:    "ws" + strings.TrimPrefix(server.URL, "http"),
		STTLanguage:   "es",
		STTModel:      "google",
		STTSampleRate: &sampleRate,
	}, providerTelnyx)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	if _, ok := provider.(*telnyx.TelnyxSTT); !ok {
		t.Fatalf("provider type = %T, want *telnyx.TelnyxSTT", provider)
	}
	if got, want := provider.Label(), "telnyx.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "google"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "telnyx"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.Diarization || caps.AlignedTranscript != "" || !caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want streaming interim offline without diarization", caps)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case record := <-records:
		if got, want := record.authorization, "Bearer test-telnyx-key"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		if got, want := record.query["transcription_engine"], "google"; got != want {
			t.Fatalf("query transcription_engine = %q, want %q", got, want)
		}
		if got, want := record.query["language"], "es"; got != want {
			t.Fatalf("query language = %q, want %q", got, want)
		}
		if got, want := record.query["input_format"], "wav"; got != want {
			t.Fatalf("query input_format = %q, want %q", got, want)
		}
		if len(record.header) != 44 {
			t.Fatalf("wav header length = %d, want 44", len(record.header))
		}
		if got, want := string(record.header[0:4]), "RIFF"; got != want {
			t.Fatalf("wav header RIFF = %q, want %q", got, want)
		}
		if got, want := string(record.header[8:12]), "WAVE"; got != want {
			t.Fatalf("wav header WAVE = %q, want %q", got, want)
		}
		if got, want := binary.LittleEndian.Uint32(record.header[24:28]), uint32(8000); got != want {
			t.Fatalf("wav sample rate = %d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Telnyx STT websocket request")
	}
}

func TestInworldSTTFallbackPassesReferenceOptions(t *testing.T) {
	type wsRecord struct {
		authorization string
		path          string
		message       map[string]any
	}
	records := make(chan wsRecord, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read inworld config message: %v", err)
			return
		}
		var message map[string]any
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Errorf("decode inworld config message: %v", err)
			return
		}
		records <- wsRecord{
			authorization: r.Header.Get("Authorization"),
			path:          r.URL.Path,
			message:       message,
		}
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	sampleRate := 8000
	numChannels := 2
	voiceProfile := true
	voiceProfileTopN := 3
	vadThreshold := 0.4
	minSilence := 180
	confidenceThreshold := 0.45
	provider, err := fallbackSTTFromProvider(AppConfig{
		InworldAPIKey:                       "test-inworld-key",
		STTBaseURL:                          server.URL,
		STTModel:                            "inworld-stt-test",
		STTLanguage:                         "en-US",
		STTSampleRate:                       &sampleRate,
		STTNumberOfChannels:                 &numChannels,
		STTVoiceProfile:                     &voiceProfile,
		STTVoiceProfileTopN:                 &voiceProfileTopN,
		STTVADThreshold:                     &vadThreshold,
		STTMinEndOfTurnSilenceWhenConfident: &minSilence,
		STTEndOfTurnConfidenceThreshold:     &confidenceThreshold,
	}, providerInworld)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	if _, ok := provider.(*inworld.InworldSTT); !ok {
		t.Fatalf("provider type = %T, want *inworld.InworldSTT", provider)
	}
	if got, want := provider.Label(), "inworld.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "inworld-stt-test"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "Inworld"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want streaming interim without offline", caps)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case record := <-records:
		if got, want := record.authorization, "Basic test-inworld-key"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		if got, want := record.path, "/stt/v1/transcribe:streamBidirectional"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		config, _ := record.message["transcribeConfig"].(map[string]any)
		if got, want := config["modelId"], "inworld-stt-test"; got != want {
			t.Fatalf("modelId = %#v, want %#v", got, want)
		}
		if got, want := config["language"], "en-US"; got != want {
			t.Fatalf("language = %#v, want %#v", got, want)
		}
		if got, want := config["sampleRateHertz"], float64(8000); got != want {
			t.Fatalf("sampleRateHertz = %#v, want %#v", got, want)
		}
		if got, want := config["numberOfChannels"], float64(2); got != want {
			t.Fatalf("numberOfChannels = %#v, want %#v", got, want)
		}
		if got, want := config["endOfTurnConfidenceThreshold"], 0.45; got != want {
			t.Fatalf("endOfTurnConfidenceThreshold = %#v, want %#v", got, want)
		}
		voiceConfig, _ := config["voiceProfileConfig"].(map[string]any)
		if got, want := voiceConfig["enableVoiceProfile"], true; got != want {
			t.Fatalf("enableVoiceProfile = %#v, want %#v", got, want)
		}
		if got, want := voiceConfig["topN"], float64(3); got != want {
			t.Fatalf("voice topN = %#v, want %#v", got, want)
		}
		inworldConfig, _ := config["inworldSttV1Config"].(map[string]any)
		if got, want := inworldConfig["minEndOfTurnSilenceWhenConfident"], float64(180); got != want {
			t.Fatalf("minEndOfTurnSilenceWhenConfident = %#v, want %#v", got, want)
		}
		if got, want := inworldConfig["vadThreshold"], 0.4; got != want {
			t.Fatalf("vadThreshold = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Inworld STT websocket config")
	}
}

func TestAssemblyAISTTFallbackPassesReferenceOptions(t *testing.T) {
	t.Setenv("ASSEMBLYAI_API_KEY", "test-assemblyai-key")
	type wsRecord struct {
		authorization string
		contentType   string
		userAgent     string
		path          string
		query         map[string]string
	}
	records := make(chan wsRecord, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		query := map[string]string{}
		for key, values := range r.URL.Query() {
			if len(values) > 0 {
				query[key] = values[0]
			}
		}
		records <- wsRecord{
			authorization: r.Header.Get("Authorization"),
			contentType:   r.Header.Get("Content-Type"),
			userAgent:     r.Header.Get("User-Agent"),
			path:          r.URL.Path,
			query:         query,
		}
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	sampleRate := 8000
	minTurnSilence := 120
	maxTurnSilence := 900
	endTurnConfidence := 0.7
	formatTurns := true
	languageDetection := false
	continuousPartials := false
	interruptionDelay := 250
	vadThreshold := 0.45
	speakerLabels := true
	maxSpeakers := 3
	provider, err := fallbackSTTFromProvider(AppConfig{
		STTBaseURL:                      "ws" + strings.TrimPrefix(server.URL, "http"),
		STTSampleRate:                   &sampleRate,
		STTModel:                        "u3-rt-pro",
		STTMinTurnSilence:               &minTurnSilence,
		STTMaxTurnSilence:               &maxTurnSilence,
		STTEndOfTurnConfidenceThreshold: &endTurnConfidence,
		STTFormatTurns:                  &formatTurns,
		STTLanguageDetection:            &languageDetection,
		STTContinuousPartials:           &continuousPartials,
		STTInterruptionDelay:            &interruptionDelay,
		STTKeytermsPrompt:               []string{"LiveKit", "AssemblyAI"},
		STTPrompt:                       "agent vocabulary",
		STTVADThreshold:                 &vadThreshold,
		STTSpeakerLabels:                &speakerLabels,
		STTMaxSpeakers:                  &maxSpeakers,
		STTDomain:                       "call_center",
	}, providerAssemblyAI)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	if _, ok := provider.(*assemblyai.AssemblyAISTT); !ok {
		t.Fatalf("provider type = %T, want *assemblyai.AssemblyAISTT", provider)
	}
	if got, want := provider.Label(), "assemblyai.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "u3-rt-pro"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "AssemblyAI"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || !caps.InterimResults || !caps.Diarization || caps.AlignedTranscript != "word" || caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want streaming interim diarization word-aligned without offline", caps)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case record := <-records:
		if got, want := record.authorization, "test-assemblyai-key"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		if got, want := record.contentType, "application/json"; got != want {
			t.Fatalf("Content-Type = %q, want %q", got, want)
		}
		if got, want := record.userAgent, "AssemblyAI/1.0 (integration=Livekit)"; got != want {
			t.Fatalf("User-Agent = %q, want %q", got, want)
		}
		if got, want := record.path, "/v3/ws"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		expectedQuery := map[string]string{
			"sample_rate":                      "8000",
			"encoding":                         "pcm_s16le",
			"speech_model":                     "u3-rt-pro",
			"min_turn_silence":                 "120",
			"max_turn_silence":                 "900",
			"end_of_turn_confidence_threshold": "0.7",
			"format_turns":                     "true",
			"language_detection":               "false",
			"continuous_partials":              "false",
			"interruption_delay":               "250",
			"keyterms_prompt":                  `["LiveKit","AssemblyAI"]`,
			"prompt":                           "agent vocabulary",
			"vad_threshold":                    "0.45",
			"speaker_labels":                   "true",
			"max_speakers":                     "3",
			"domain":                           "call_center",
		}
		for key, want := range expectedQuery {
			if got := record.query[key]; got != want {
				t.Fatalf("query %s = %q, want %q", key, got, want)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for AssemblyAI STT websocket request")
	}
}

func TestGnaniSTTFallbackPassesReferenceOptions(t *testing.T) {
	type wsRecord struct {
		apiKey   string
		language string
		path     string
	}
	type restRecord struct {
		apiKey         string
		organizationID string
		userID         string
		path           string
		fields         map[string]string
		filename       string
		contentType    string
		audio          []byte
	}
	wsRecords := make(chan wsRecord, 1)
	restRecords := make(chan restRecord, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stt/v3/stream":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade websocket: %v", err)
				return
			}
			defer conn.Close()
			wsRecords <- wsRecord{
				apiKey:   r.Header.Get("x-api-key-id"),
				language: r.Header.Get("lang_code"),
				path:     r.URL.Path,
			}
		case "/stt/v3":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse gnani stt multipart request: %v", err)
				return
			}
			file, header, err := r.FormFile("audio_file")
			if err != nil {
				t.Errorf("read gnani stt audio file: %v", err)
				return
			}
			defer file.Close()
			audio, err := io.ReadAll(file)
			if err != nil {
				t.Errorf("read gnani stt audio bytes: %v", err)
				return
			}
			fields := map[string]string{}
			for key, values := range r.MultipartForm.Value {
				if len(values) > 0 {
					fields[key] = values[0]
				}
			}
			restRecords <- restRecord{
				apiKey:         r.Header.Get("X-API-Key-ID"),
				organizationID: r.Header.Get("X-Organization-ID"),
				userID:         r.Header.Get("X-API-User-ID"),
				path:           r.URL.Path,
				fields:         fields,
				filename:       header.Filename,
				contentType:    header.Header.Get("Content-Type"),
				audio:          audio,
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"transcript":"namaste","request_id":"req-1","language":"hi-IN","confidence":0.91}`))
		default:
			http.NotFound(w, r)
		}
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	sampleRate := 8000
	provider, err := fallbackSTTFromProvider(AppConfig{
		GnaniAPIKey:       "test-gnani-key",
		STTBaseURL:        server.URL,
		STTLanguage:       "hi-IN",
		STTSampleRate:     &sampleRate,
		STTOrganizationID: "org-123",
		STTUserID:         "user-456",
	}, providerGnani)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	if _, ok := provider.(*gnani.STT); !ok {
		t.Fatalf("provider type = %T, want *gnani.STT", provider)
	}
	if got, want := provider.Label(), "gnani.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "vachana-stt-v3"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "Gnani"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.InterimResults || caps.Diarization || caps.AlignedTranscript != "" || !caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want streaming offline without interim/diarization", caps)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case record := <-wsRecords:
		if got, want := record.apiKey, "test-gnani-key"; got != want {
			t.Fatalf("websocket x-api-key-id = %q, want %q", got, want)
		}
		if got, want := record.language, "hi-IN"; got != want {
			t.Fatalf("websocket lang_code = %q, want %q", got, want)
		}
		if got, want := record.path, "/stt/v3/stream"; got != want {
			t.Fatalf("websocket path = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Gnani STT websocket request")
	}

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "ta-IN")
	if err != nil {
		t.Fatalf("Recognize() error = %v", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript || len(event.Alternatives) != 1 {
		t.Fatalf("event = %+v, want one final transcript", event)
	}
	alt := event.Alternatives[0]
	if alt.Text != "namaste" || alt.Language != "ta-IN" || alt.Confidence != 1 {
		t.Fatalf("alternative = %+v, want mapped Gnani transcript", alt)
	}

	select {
	case record := <-restRecords:
		if got, want := record.apiKey, "test-gnani-key"; got != want {
			t.Fatalf("REST X-API-Key-ID = %q, want %q", got, want)
		}
		if got, want := record.organizationID, "org-123"; got != want {
			t.Fatalf("REST X-Organization-ID = %q, want %q", got, want)
		}
		if got, want := record.userID, "user-456"; got != want {
			t.Fatalf("REST X-API-User-ID = %q, want %q", got, want)
		}
		if got, want := record.path, "/stt/v3"; got != want {
			t.Fatalf("REST path = %q, want %q", got, want)
		}
		if got, want := record.fields["language_code"], "ta-IN"; got != want {
			t.Fatalf("language_code = %q, want %q", got, want)
		}
		if got, want := record.filename, "audio.wav"; got != want {
			t.Fatalf("filename = %q, want %q", got, want)
		}
		if got, want := record.contentType, "audio/wav"; got != want {
			t.Fatalf("file content type = %q, want %q", got, want)
		}
		if got, want := fmt.Sprintf("%x", record.audio), "0102"; got != want {
			t.Fatalf("audio bytes = %s, want %s", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Gnani STT REST request")
	}
}

func TestSimplismartSTTFallbackPassesReferenceOptions(t *testing.T) {
	type restRecord struct {
		authorization string
		contentType   string
		path          string
		payload       map[string]any
	}
	records := make(chan restRecord, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode simplismart stt request: %v", err)
			return
		}
		records <- restRecord{
			authorization: r.Header.Get("Authorization"),
			contentType:   r.Header.Get("Content-Type"),
			path:          r.URL.Path,
			payload:       payload,
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"request_id":"req-1","transcription":["hello ","world"],"timestamps":[[0.1,0.5],[0.6,1.0]],"info":{"language":"de"}}`))
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test HTTP server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	includeTimestamps := true
	maxSpeakers := 2
	provider, err := fallbackSTTFromProvider(AppConfig{
		SimplismartAPIKey:    "test-simplismart-key",
		STTBaseURL:           server.URL,
		STTModel:             "custom/model",
		STTLanguage:          "fr",
		STTTask:              "translate",
		STTIncludeTimestamps: &includeTimestamps,
		STTKeytermsPrompt:    []string{"Chicago", "Joplin"},
		STTMaxSpeakers:       &maxSpeakers,
	}, providerSimplismart)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	if _, ok := provider.(*simplismart.SimplismartSTT); !ok {
		t.Fatalf("provider type = %T, want *simplismart.SimplismartSTT", provider)
	}
	if got, want := provider.Label(), "simplismart.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "custom/model"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "Simplismart"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); caps.Streaming || caps.InterimResults || !caps.Diarization || caps.AlignedTranscript != "word" || !caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want offline word-aligned diarization without streaming/interim", caps)
	}

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "")
	if err != nil {
		t.Fatalf("Recognize() error = %v", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript || event.RequestID != "req-1" || len(event.Alternatives) != 1 {
		t.Fatalf("event = %+v, want one final transcript with request id", event)
	}
	alt := event.Alternatives[0]
	if alt.Text != "hello world" || alt.Language != "de" || alt.StartTime != 0.1 || alt.EndTime != 1.0 {
		t.Fatalf("alternative = %+v, want mapped Simplismart transcript", alt)
	}

	select {
	case record := <-records:
		if got, want := record.authorization, "Bearer test-simplismart-key"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		if got, want := record.contentType, "application/json"; got != want {
			t.Fatalf("Content-Type = %q, want %q", got, want)
		}
		if got, want := record.path, "/"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		expectedPayload := map[string]any{
			"audio_data":         "AQI=",
			"language":           "fr",
			"model":              "custom/model",
			"task":               "translate",
			"without_timestamps": false,
			"hotwords":           "Chicago,Joplin",
			"num_speakers":       float64(2),
		}
		for key, want := range expectedPayload {
			if got := record.payload[key]; got != want {
				t.Fatalf("payload %s = %#v, want %#v", key, got, want)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Simplismart STT REST request")
	}
}

func TestClovaSTTFallbackPassesReferenceOptions(t *testing.T) {
	type restRecord struct {
		secret      string
		path        string
		params      map[string]any
		filename    string
		contentType string
		mediaPrefix string
	}
	records := make(chan restRecord, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse clova stt multipart request: %v", err)
			return
		}
		var params map[string]any
		if err := json.Unmarshal([]byte(r.FormValue("params")), &params); err != nil {
			t.Errorf("decode clova stt params: %v", err)
			return
		}
		file, header, err := r.FormFile("media")
		if err != nil {
			t.Errorf("read clova stt media file: %v", err)
			return
		}
		defer file.Close()
		media, err := io.ReadAll(file)
		if err != nil {
			t.Errorf("read clova stt media bytes: %v", err)
			return
		}
		prefix := string(media)
		if len(prefix) > 12 {
			prefix = prefix[:12]
		}
		records <- restRecord{
			secret:      r.Header.Get("X-CLOVASPEECH-API-KEY"),
			path:        r.URL.Path,
			params:      params,
			filename:    header.Filename,
			contentType: header.Header.Get("Content-Type"),
			mediaPrefix: prefix,
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hello clova","confidence":0.92}`))
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test HTTP server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	threshold := 0.6
	provider, err := fallbackSTTFromProvider(AppConfig{
		ClovaSTTSecret:    "test-clova-secret",
		ClovaSTTInvokeURL: server.URL,
		STTLanguage:       "en",
		STTVADThreshold:   &threshold,
	}, providerClova)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	if _, ok := provider.(*clova.ClovaSTT); !ok {
		t.Fatalf("provider type = %T, want *clova.ClovaSTT", provider)
	}
	if got, want := provider.Label(), "clova.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "unknown"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "Clova"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); caps.Streaming || !caps.InterimResults || caps.Diarization || caps.AlignedTranscript != "" || !caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want offline interim without streaming/diarization", caps)
	}

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "")
	if err != nil {
		t.Fatalf("Recognize() error = %v", err)
	}
	if event.Type != stt.SpeechEventInterimTranscript || len(event.Alternatives) != 1 {
		t.Fatalf("event = %+v, want one interim transcript", event)
	}
	alt := event.Alternatives[0]
	if alt.Text != "hello clova" || alt.Language != "en-US" || alt.Confidence != 0.92 {
		t.Fatalf("alternative = %+v, want mapped Clova transcript", alt)
	}

	select {
	case record := <-records:
		if got, want := record.secret, "test-clova-secret"; got != want {
			t.Fatalf("X-CLOVASPEECH-API-KEY = %q, want %q", got, want)
		}
		if got, want := record.path, "/recognizer/upload"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		if got, want := record.params["language"], "en-US"; got != want {
			t.Fatalf("params language = %#v, want %#v", got, want)
		}
		if got, want := record.params["completion"], "sync"; got != want {
			t.Fatalf("params completion = %#v, want %#v", got, want)
		}
		if got, want := record.filename, "audio.wav"; got != want {
			t.Fatalf("filename = %q, want %q", got, want)
		}
		if got, want := record.contentType, "audio/wav"; got != want {
			t.Fatalf("media content type = %q, want %q", got, want)
		}
		if !strings.HasPrefix(record.mediaPrefix, "RIFF") || !strings.Contains(record.mediaPrefix, "WAVE") {
			t.Fatalf("media prefix = %q, want RIFF/WAVE wav header", record.mediaPrefix)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Clova STT REST request")
	}
}

func TestDeepgramSTTFallbackPassesReferenceOptions(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "test-deepgram-key")
	type wsRecord struct {
		authorization string
		query         map[string][]string
	}
	records := make(chan wsRecord, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		records <- wsRecord{
			authorization: r.Header.Get("Authorization"),
			query:         map[string][]string(r.URL.Query()),
		}
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	interim := false
	punctuate := false
	smartFormat := true
	noDelay := true
	endpointing := 25
	diarization := true
	fillerWords := true
	sampleRate := 8000
	numChannels := 2
	vadEvents := true
	profanityFilter := true
	numerals := true
	mipOptOut := true
	provider, err := fallbackSTTFromProvider(AppConfig{
		STTBaseURL:          "ws" + strings.TrimPrefix(server.URL, "http"),
		STTModel:            "nova-2",
		STTInterimResults:   &interim,
		STTPunctuate:        &punctuate,
		STTSmartFormat:      &smartFormat,
		STTNoDelay:          &noDelay,
		STTEndpointingMS:    &endpointing,
		STTDiarization:      &diarization,
		STTFillerWords:      &fillerWords,
		STTSampleRate:       &sampleRate,
		STTNumberOfChannels: &numChannels,
		STTVADEvents:        &vadEvents,
		STTProfanityFilter:  &profanityFilter,
		STTNumerals:         &numerals,
		STTMIPOptOut:        &mipOptOut,
		STTKeywords:         []deepgram.DeepgramKeyword{{Keyword: "cavos", Boost: 2.5}},
		STTRedact:           []string{"pci", "ssn"},
		STTTags:             []string{"agent", "fallback"},
	}, providerDeepgram)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	if _, ok := provider.(*deepgram.DeepgramSTT); !ok {
		t.Fatalf("provider type = %T, want *deepgram.DeepgramSTT", provider)
	}
	if got, want := provider.Label(), "deepgram.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "nova-2"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "Deepgram"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.InterimResults || !caps.Diarization || caps.AlignedTranscript != "word" || !caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want streaming diarization word-aligned offline without interim", caps)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case record := <-records:
		if got, want := record.authorization, "Token test-deepgram-key"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		expectedQuery := map[string]string{
			"model":            "nova-2",
			"punctuate":        "false",
			"smart_format":     "true",
			"no_delay":         "true",
			"interim_results":  "false",
			"encoding":         "linear16",
			"sample_rate":      "8000",
			"channels":         "2",
			"endpointing":      "25",
			"vad_events":       "true",
			"filler_words":     "true",
			"diarize":          "true",
			"profanity_filter": "true",
			"numerals":         "true",
			"mip_opt_out":      "true",
		}
		for key, want := range expectedQuery {
			if got := firstQueryValue(record.query, key); got != want {
				t.Fatalf("query %s = %q, want %q", key, got, want)
			}
		}
		if got, want := record.query["keywords"], []string{"cavos:2.5"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("keywords = %#v, want %#v", got, want)
		}
		if got, want := record.query["redact"], []string{"pci", "ssn"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("redact = %#v, want %#v", got, want)
		}
		if got, want := record.query["tag"], []string{"agent", "fallback"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("tag = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Deepgram STT websocket request")
	}
}

func TestRtzrSTTFallbackPassesReferenceOptions(t *testing.T) {
	type wsRecord struct {
		authorization string
		query         map[string]string
	}
	records := make(chan wsRecord, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		query := map[string]string{}
		for key, values := range r.URL.Query() {
			if len(values) > 0 {
				query[key] = values[0]
			}
		}
		records <- wsRecord{
			authorization: r.Header.Get("Authorization"),
			query:         query,
		}
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	sampleRate := 16000
	epdTime := 1.2
	noiseThreshold := 0.42
	activeThreshold := 0.77
	punctuate := true
	provider, err := fallbackSTTFromProvider(AppConfig{
		RtzrClientID:                    "test-client-id",
		RtzrClientSecret:                "test-client-secret",
		RtzrAccessToken:                 "test-access-token",
		STTStreamingURL:                 "ws" + strings.TrimPrefix(server.URL, "http"),
		STTModel:                        "sommers_ko-test",
		STTSampleRate:                   &sampleRate,
		STTDomain:                       "GENERAL",
		STTEndpointingSeconds:           &epdTime,
		STTVADThreshold:                 &noiseThreshold,
		STTEndOfTurnConfidenceThreshold: &activeThreshold,
		STTPunctuate:                    &punctuate,
		STTKeytermsPrompt:               []string{"서울", "LiveKit"},
	}, providerRtzr)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	if _, ok := provider.(*rtzr.RtzrSTT); !ok {
		t.Fatalf("provider type = %T, want *rtzr.RtzrSTT", provider)
	}
	if got, want := provider.Label(), "rtzr.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "sommers_ko-test"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "RTZR"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.Diarization || caps.AlignedTranscript != "chunk" || caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want streaming interim chunk-aligned without offline", caps)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case record := <-records:
		if got, want := record.authorization, "bearer test-access-token"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		expectedQuery := map[string]string{
			"model_name":       "sommers_ko-test",
			"domain":           "GENERAL",
			"sample_rate":      "16000",
			"encoding":         "LINEAR16",
			"epd_time":         "1.2",
			"noise_threshold":  "0.42",
			"active_threshold": "0.77",
			"use_punctuation":  "true",
			"keywords":         "서울,LiveKit",
		}
		for key, want := range expectedQuery {
			if got := record.query[key]; got != want {
				t.Fatalf("query %s = %q, want %q", key, got, want)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for RTZR STT websocket request")
	}
}

func TestMistralAISTTFallbackPassesReferenceOptions(t *testing.T) {
	type requestRecord struct {
		apiKey      string
		path        string
		fields      map[string]string
		filename    string
		contentType string
		audio       []byte
	}
	records := make(chan requestRecord, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse mistralai stt multipart request: %v", err)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("read mistralai stt audio file: %v", err)
			return
		}
		defer file.Close()
		audio, err := io.ReadAll(file)
		if err != nil {
			t.Errorf("read mistralai stt audio bytes: %v", err)
			return
		}
		fields := map[string]string{}
		for key, values := range r.MultipartForm.Value {
			if len(values) > 0 {
				fields[key] = values[0]
			}
		}
		records <- requestRecord{
			apiKey:      r.Header.Get("x-api-key"),
			path:        r.URL.Path,
			fields:      fields,
			filename:    header.Filename,
			contentType: header.Header.Get("Content-Type"),
			audio:       audio,
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"bonjour monde","language":"fr","segments":[{"text":"bonjour","start":0.2,"end":0.7},{"text":"monde","start":0.8,"end":1.1}]}`))
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test HTTP server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	provider, err := fallbackSTTFromProvider(AppConfig{
		MistralAPIKey:     "test-mistral-key",
		STTBaseURL:        server.URL,
		STTModel:          "voxtral-mini-2507",
		STTLanguage:       "fr",
		STTKeytermsPrompt: []string{"Chicago", "Joplin"},
	}, providerMistralAI)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	if _, ok := provider.(*mistralai.MistralAISTT); !ok {
		t.Fatalf("provider type = %T, want *mistralai.MistralAISTT", provider)
	}
	if got, want := provider.Label(), "mistralai.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "voxtral-mini-2507"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "MistralAI"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); caps.Streaming || caps.InterimResults || caps.Diarization || caps.AlignedTranscript != "" || !caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want offline-only without streaming/interim/diarization", caps)
	}

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte{0x01, 0x02}}}, "")
	if err != nil {
		t.Fatalf("Recognize() error = %v", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript || len(event.Alternatives) != 1 {
		t.Fatalf("event = %+v, want one final transcript", event)
	}
	alt := event.Alternatives[0]
	if alt.Text != "bonjour monde" || alt.Language != "fr" || alt.StartTime != 0.2 || alt.EndTime != 1.1 {
		t.Fatalf("alternative = %+v, want mapped MistralAI transcript", alt)
	}

	select {
	case record := <-records:
		if got, want := record.apiKey, "test-mistral-key"; got != want {
			t.Fatalf("x-api-key = %q, want %q", got, want)
		}
		if got, want := record.path, "/audio/transcriptions"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		if got, want := record.fields["model"], "voxtral-mini-2507"; got != want {
			t.Fatalf("model = %q, want %q", got, want)
		}
		if got, want := record.fields["language"], "fr"; got != want {
			t.Fatalf("language = %q, want %q", got, want)
		}
		if got, want := record.fields["context_bias"], "Chicago,Joplin"; got != want {
			t.Fatalf("context_bias = %q, want %q", got, want)
		}
		if _, ok := record.fields["timestamp_granularities"]; ok {
			t.Fatalf("timestamp_granularities present with language: %#v", record.fields)
		}
		if got, want := record.filename, "audio.wav"; got != want {
			t.Fatalf("filename = %q, want %q", got, want)
		}
		if got, want := record.contentType, "audio/wav"; got != want {
			t.Fatalf("file content type = %q, want %q", got, want)
		}
		if got, want := fmt.Sprintf("%x", record.audio), "0102"; got != want {
			t.Fatalf("audio bytes = %s, want %s", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for MistralAI STT recognize request")
	}
}

func TestSarvamSTTFallbackPassesReferenceOptions(t *testing.T) {
	type wsRecord struct {
		apiKey    string
		userAgent string
		query     map[string]string
		message   map[string]any
	}
	records := make(chan wsRecord, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		query := map[string]string{}
		for key, values := range r.URL.Query() {
			if len(values) > 0 {
				query[key] = values[0]
			}
		}
		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read sarvam stt config message: %v", err)
			return
		}
		var message map[string]any
		if err := json.Unmarshal(payload, &message); err != nil {
			t.Errorf("decode sarvam stt config message: %v", err)
			return
		}
		records <- wsRecord{
			apiKey:    r.Header.Get("api-subscription-key"),
			userAgent: r.Header.Get("User-Agent"),
			query:     query,
			message:   message,
		}
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	sampleRate := 8000
	highVAD := true
	flushSignal := false
	positiveSpeech := 0.6
	negativeSpeech := 0.2
	minSpeechFrames := 3
	firstTurnMinSpeechFrames := 5
	negativeFramesCount := 2
	negativeFramesWindow := 4
	startSpeechVolume := 0.1
	interruptMinSpeechFrames := 6
	preSpeechPadFrames := 7
	numInitialIgnoredFrames := 8
	provider, err := fallbackSTTFromProvider(AppConfig{
		SarvamAPIKey:                  "test-sarvam-key",
		STTStreamingURL:               "ws" + strings.TrimPrefix(server.URL, "http"),
		STTModel:                      "saaras:v3",
		STTLanguage:                   "hi-IN",
		STTTask:                       "translate",
		STTPrompt:                     "domain prompt",
		STTSampleRate:                 &sampleRate,
		STTVADEvents:                  &highVAD,
		STTVADFlush:                   &flushSignal,
		STTEncoding:                   "audio/pcm",
		STTPositiveSpeechThreshold:    &positiveSpeech,
		STTNegativeSpeechThreshold:    &negativeSpeech,
		STTMinSpeechFrames:            &minSpeechFrames,
		STTFirstTurnMinSpeechFrames:   &firstTurnMinSpeechFrames,
		STTNegativeFramesCount:        &negativeFramesCount,
		STTNegativeFramesWindow:       &negativeFramesWindow,
		STTStartSpeechVolumeThreshold: &startSpeechVolume,
		STTInterruptMinSpeechFrames:   &interruptMinSpeechFrames,
		STTPreSpeechPadFrames:         &preSpeechPadFrames,
		STTNumInitialIgnoredFrames:    &numInitialIgnoredFrames,
	}, providerSarvam)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}

	if _, ok := provider.(*sarvam.SarvamSTT); !ok {
		t.Fatalf("provider type = %T, want *sarvam.SarvamSTT", provider)
	}
	if got, want := provider.Label(), "sarvam.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := stt.Model(provider), "saaras:v3"; got != want {
		t.Fatalf("stt.Model() = %q, want %q", got, want)
	}
	if got, want := stt.Provider(provider), "Sarvam"; got != want {
		t.Fatalf("stt.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || !caps.InterimResults || caps.Diarization || caps.AlignedTranscript != "" || !caps.OfflineRecognize {
		t.Fatalf("Capabilities() = %+v, want streaming interim offline without diarization", caps)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case record := <-records:
		if got, want := record.apiKey, "test-sarvam-key"; got != want {
			t.Fatalf("api-subscription-key = %q, want %q", got, want)
		}
		if got, want := record.userAgent, "LiveKit Agents Sarvam Plugin/Go"; got != want {
			t.Fatalf("User-Agent = %q, want %q", got, want)
		}
		expectedQuery := map[string]string{
			"language-code":                 "hi-IN",
			"model":                         "saaras:v3",
			"mode":                          "translate",
			"vad_signals":                   "true",
			"sample_rate":                   "8000",
			"high_vad_sensitivity":          "true",
			"flush_signal":                  "false",
			"input_audio_codec":             "audio/pcm",
			"positive_speech_threshold":     "0.6",
			"negative_speech_threshold":     "0.2",
			"min_speech_frames":             "3",
			"first_turn_min_speech_frames":  "5",
			"negative_frames_count":         "2",
			"negative_frames_window":        "4",
			"start_speech_volume_threshold": "0.1",
			"interrupt_min_speech_frames":   "6",
			"pre_speech_pad_frames":         "7",
			"num_initial_ignored_frames":    "8",
		}
		for key, want := range expectedQuery {
			if got := record.query[key]; got != want {
				t.Fatalf("query %s = %q, want %q", key, got, want)
			}
		}
		if got, want := record.message["type"], "config"; got != want {
			t.Fatalf("config type = %#v, want %#v", got, want)
		}
		if got, want := record.message["prompt"], "domain prompt"; got != want {
			t.Fatalf("config prompt = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Sarvam STT websocket config")
	}
}

func TestSLNGSTTFallbackPassesModelOptions(t *testing.T) {
	initPayloads := make(chan map[string]any, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read init payload: %v", err)
			return
		}
		var init map[string]any
		if err := json.Unmarshal(payload, &init); err != nil {
			t.Errorf("decode init payload: %v", err)
			return
		}
		initPayloads <- init
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	minSpeakers := 2
	maxSpeakers := 4
	interimResults := false
	diarization := true
	provider, err := fallbackSTTFromProvider(AppConfig{
		SLNGAPIKey:        "test-slng-key",
		STTBaseURL:        endpoint,
		STTModel:          "deepgram/nova:3",
		STTLanguage:       "en",
		STTEncoding:       "pcm_s16le",
		STTInterimResults: &interimResults,
		STTDiarization:    &diarization,
		STTMinSpeakers:    &minSpeakers,
		STTMaxSpeakers:    &maxSpeakers,
		STTModelOptions:   map[string]any{"target_language_code": "en-US", "enable_partials": true, "custom_flag": "kept"},
	}, providerSLNG)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}
	if got, want := provider.Label(), "slng.STT"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || !caps.InterimResults {
		t.Fatalf("Capabilities() = %+v, want streaming interim STT", caps)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case init := <-initPayloads:
		if got, want := init["type"], "init"; got != want {
			t.Fatalf("init.type = %#v, want %#v", got, want)
		}
		if got, want := init["model"], "nova-3"; got != want {
			t.Fatalf("init.model = %#v, want %#v", got, want)
		}
		config, ok := init["config"].(map[string]any)
		if !ok {
			t.Fatalf("init.config = %#v, want object", init["config"])
		}
		wantConfig := map[string]any{
			"language":                   "en-US",
			"encoding":                   "linear16",
			"enable_diarization":         true,
			"min_speakers":               float64(2),
			"max_speakers":               float64(4),
			"enable_partials":            true,
			"enable_partial_transcripts": true,
			"custom_flag":                "kept",
		}
		for key, want := range wantConfig {
			if got := config[key]; got != want {
				t.Fatalf("config.%s = %#v, want %#v", key, got, want)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SLNG STT init payload")
	}
}

func TestSLNGSTTFallbackPassesAudioAndVADOptions(t *testing.T) {
	initPayloads := make(chan map[string]any, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read init payload: %v", err)
			return
		}
		var init map[string]any
		if err := json.Unmarshal(payload, &init); err != nil {
			t.Errorf("decode init payload: %v", err)
			return
		}
		initPayloads <- init
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	sampleRate := 8000
	vadThreshold := 0.73
	provider, err := fallbackSTTFromProvider(AppConfig{
		SLNGAPIKey:      "test-slng-key",
		STTBaseURL:      endpoint,
		STTSampleRate:   &sampleRate,
		STTVADThreshold: &vadThreshold,
	}, providerSLNG)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case init := <-initPayloads:
		config, ok := init["config"].(map[string]any)
		if !ok {
			t.Fatalf("init.config = %#v, want object", init["config"])
		}
		if got, want := config["sample_rate"], float64(8000); got != want {
			t.Fatalf("config.sample_rate = %#v, want %#v", got, want)
		}
		if got, want := config["vad_threshold"], 0.73; got != want {
			t.Fatalf("config.vad_threshold = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SLNG STT init payload")
	}
}

func TestSLNGSTTFallbackPassesVADSilenceOption(t *testing.T) {
	initPayloads := make(chan map[string]any, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read init payload: %v", err)
			return
		}
		var init map[string]any
		if err := json.Unmarshal(payload, &init); err != nil {
			t.Errorf("decode init payload: %v", err)
			return
		}
		initPayloads <- init
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	silenceSeconds := 0.45
	provider, err := fallbackSTTFromProvider(AppConfig{
		SLNGAPIKey:                    "test-slng-key",
		STTBaseURL:                    endpoint,
		STTVADSilenceThresholdSeconds: &silenceSeconds,
	}, providerSLNG)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case init := <-initPayloads:
		config, ok := init["config"].(map[string]any)
		if !ok {
			t.Fatalf("init.config = %#v, want object", init["config"])
		}
		if got, want := config["vad_min_silence_duration_ms"], float64(450); got != want {
			t.Fatalf("config.vad_min_silence_duration_ms = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SLNG STT init payload")
	}
}

func TestSLNGSTTFallbackPassesVADSpeechPadOption(t *testing.T) {
	initPayloads := make(chan map[string]any, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read init payload: %v", err)
			return
		}
		var init map[string]any
		if err := json.Unmarshal(payload, &init); err != nil {
			t.Errorf("decode init payload: %v", err)
			return
		}
		initPayloads <- init
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	speechPadMS := 75
	provider, err := fallbackSTTFromProvider(AppConfig{
		SLNGAPIKey:        "test-slng-key",
		STTBaseURL:        endpoint,
		STTVADSpeechPadMS: &speechPadMS,
	}, providerSLNG)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case init := <-initPayloads:
		config, ok := init["config"].(map[string]any)
		if !ok {
			t.Fatalf("init.config = %#v, want object", init["config"])
		}
		if got, want := config["vad_speech_pad_ms"], float64(75); got != want {
			t.Fatalf("config.vad_speech_pad_ms = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SLNG STT init payload")
	}
}

func TestSLNGSTTFallbackPassesModelEndpoints(t *testing.T) {
	initPayloads := make(chan map[string]any, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read init payload: %v", err)
			return
		}
		var init map[string]any
		if err := json.Unmarshal(payload, &init); err != nil {
			t.Errorf("decode init payload: %v", err)
			return
		}
		initPayloads <- init
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	provider, err := fallbackSTTFromProvider(AppConfig{
		SLNGAPIKey: "test-slng-key",
		STTModelEndpoints: []string{
			"ws://127.0.0.1:1/v1/stt/deepgram/failing",
			"ws" + strings.TrimPrefix(server.URL, "http") + "/v1/stt/deepgram/nova:3",
		},
	}, providerSLNG)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case init := <-initPayloads:
		if got, want := init["model"], "nova-3"; got != want {
			t.Fatalf("init.model = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fallback SLNG STT init payload")
	}
}

func TestSLNGSTTFallbackBuffersAudioByReferenceWindow(t *testing.T) {
	binaryLengths := make(chan int, 2)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read init payload: %v", err)
			return
		}
		for {
			msgType, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if msgType == websocket.BinaryMessage {
				binaryLengths <- len(payload)
			}
		}
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	sampleRate := 8000
	bufferSizeSeconds := 0.02
	provider, err := fallbackSTTFromProvider(AppConfig{
		SLNGAPIKey:           "test-slng-key",
		STTBaseURL:           endpoint,
		STTSampleRate:        &sampleRate,
		STTBufferSizeSeconds: &bufferSizeSeconds,
	}, providerSLNG)
	if err != nil {
		t.Fatalf("fallbackSTTFromProvider() error = %v", err)
	}
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	halfWindow := make([]byte, 160)
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              halfWindow,
		SampleRate:        uint32(sampleRate),
		NumChannels:       1,
		SamplesPerChannel: 80,
	}); err != nil {
		t.Fatalf("PushFrame(first half) error = %v", err)
	}
	select {
	case got := <-binaryLengths:
		t.Fatalf("sent %d bytes before reference buffer window was full", got)
	case <-time.After(100 * time.Millisecond):
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              halfWindow,
		SampleRate:        uint32(sampleRate),
		NumChannels:       1,
		SamplesPerChannel: 80,
	}); err != nil {
		t.Fatalf("PushFrame(second half) error = %v", err)
	}
	select {
	case got := <-binaryLengths:
		if want := 320; got != want {
			t.Fatalf("buffered binary length = %d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SLNG buffered audio")
	}
}

func firstQueryValue(values map[string][]string, key string) string {
	if len(values[key]) == 0 {
		return ""
	}
	return values[key][0]
}

func TestDefaultConfigFromEnvWrapsTTSFallbackProviders(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_TTS_FALLBACK_PROVIDERS", "cartesia")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.TTS.Label(); got != "FallbackAdapter(openai.TTS)" {
		t.Fatalf("TTS label = %q, want fallback adapter around primary openai TTS", got)
	}
}

func TestDefaultConfigFromEnvAcceptsLiveKitTTSFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_TTS_FALLBACK_PROVIDERS", "livekit")
	t.Setenv("RTP_AGENT_TTS_MODEL", "cartesia/sonic-3")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("LIVEKIT_INFERENCE_API_KEY", "test-livekit-key")
	t.Setenv("LIVEKIT_INFERENCE_API_SECRET", "test-livekit-secret")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.TTS.Label(); got != "FallbackAdapter(openai.TTS)" {
		t.Fatalf("TTS label = %q, want fallback adapter around primary openai TTS", got)
	}
}

func TestAWSTTSFallbackPassesReferenceOptions(t *testing.T) {
	sampleRate := 22050
	provider, err := fallbackTTSFromProvider(AppConfig{
		AWSRegion:     "us-west-2",
		TTSVoice:      "Joanna",
		TTSModel:      "standard",
		TTSTextType:   "ssml",
		TTSLanguage:   "en-US",
		TTSSampleRate: &sampleRate,
	}, providerAWS)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	awsProvider, ok := provider.(*adapteraws.AWSTTS)
	if !ok {
		t.Fatalf("provider type = %T, want *aws.AWSTTS", provider)
	}
	if got, want := provider.Label(), "aws.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 22050; got != want {
		t.Fatalf("SampleRate() = %d, want reference configured sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "standard"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "Amazon Polly"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference non-streaming without aligned transcript", caps)
	}

	state := reflect.ValueOf(awsProvider).Elem()
	if got, want := state.FieldByName("voice").String(), "Joanna"; got != want {
		t.Fatalf("voice = %q, want %q", got, want)
	}
	if got, want := state.FieldByName("engine").String(), "standard"; got != want {
		t.Fatalf("engine = %q, want %q", got, want)
	}
	if got, want := state.FieldByName("textType").String(), "ssml"; got != want {
		t.Fatalf("textType = %q, want %q", got, want)
	}
	if got, want := state.FieldByName("language").String(), "en-US"; got != want {
		t.Fatalf("language = %q, want %q", got, want)
	}
}

func TestAzureTTSFallbackPassesReferenceOptions(t *testing.T) {
	t.Setenv("AZURE_SPEECH_KEY", "test-azure-key")
	t.Setenv("AZURE_SPEECH_REGION", "eastus")

	provider, err := fallbackTTSFromProvider(AppConfig{
		TTSVoice:    "id-ID-GadisNeural",
		TTSLanguage: "id-ID",
	}, providerAzure)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	azureProvider, ok := provider.(*azure.AzureTTS)
	if !ok {
		t.Fatalf("provider type = %T, want *azure.AzureTTS", provider)
	}
	if got, want := azureProvider.Label(), "azure.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := azureProvider.SampleRate(), 24000; got != want {
		t.Fatalf("SampleRate() = %d, want reference default sample rate %d", got, want)
	}
	if got, want := tts.Model(azureProvider), "unknown"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(azureProvider), "Azure TTS"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if got, want := azureProvider.Language(), "id-ID"; got != want {
		t.Fatalf("Language() = %q, want %q", got, want)
	}
	if caps := azureProvider.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference non-streaming without aligned transcript", caps)
	}
}

func TestBasetenTTSFallbackPassesReferenceOptions(t *testing.T) {
	t.Setenv("BASETEN_API_KEY", "test-baseten-key")
	temperature := 0.72
	maxTokens := 1200
	bufferSize := 6
	var gotHeaders http.Header
	var gotPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Errorf("decode baseten payload: %v", err)
			return
		}
		_, _ = w.Write([]byte("audio"))
	}))
	defer server.Close()

	provider, err := fallbackTTSFromProvider(AppConfig{
		TTSBaseURL:     server.URL,
		TTSVoice:       "tara-custom",
		TTSLanguage:    "es",
		TTSTemperature: &temperature,
		TTSMaxTokens:   &maxTokens,
		TTSBufferSize:  &bufferSize,
	}, providerBaseten)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*baseten.BasetenTTS); !ok {
		t.Fatalf("provider type = %T, want *baseten.BasetenTTS", provider)
	}
	if got, want := provider.Label(), "baseten.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 24000; got != want {
		t.Fatalf("SampleRate() = %d, want reference default sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "unknown"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "Baseten"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference non-streaming without aligned transcript", caps)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotHeaders.Get("Authorization"), "Api-Key test-baseten-key"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := gotPayload["prompt"], "hello"; got != want {
		t.Fatalf("payload.prompt = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["voice"], "tara-custom"; got != want {
		t.Fatalf("payload.voice = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["language"], "es"; got != want {
		t.Fatalf("payload.language = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["temperature"], 0.72; got != want {
		t.Fatalf("payload.temperature = %#v, want %#v", got, want)
	}
}

func TestCartesiaTTSFallbackPassesReferenceOptions(t *testing.T) {
	t.Setenv("CARTESIA_API_KEY", "test-cartesia-key")
	sampleRate := 16000
	wordTimestamps := false
	volume := 1.1
	var gotHeaders http.Header
	var gotPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		if got, want := r.URL.Path, "/tts/bytes"; got != want {
			t.Errorf("request path = %q, want %q", got, want)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Errorf("decode cartesia payload: %v", err)
			return
		}
		_, _ = w.Write([]byte("audio"))
	}))
	defer server.Close()

	provider, err := fallbackTTSFromProvider(AppConfig{
		TTSBaseURL:             server.URL,
		TTSModel:               "sonic-3",
		TTSVoice:               "voice-id-ignored-for-embedding",
		TTSVoiceEmbedding:      []float64{0.1, 0.2, 0.3},
		TTSLanguage:            "es",
		TTSEncoding:            "pcm_mulaw",
		TTSSampleRate:          &sampleRate,
		TTSAPIVersion:          "2025-01-01",
		TTSWordTimestamps:      &wordTimestamps,
		TTSSpeed:               1.2,
		TTSEmotion:             "happy",
		TTSVolume:              &volume,
		TTSPronunciationDictID: "dict-1",
	}, providerCartesia)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*cartesia.CartesiaTTS); !ok {
		t.Fatalf("provider type = %T, want *cartesia.CartesiaTTS", provider)
	}
	if got, want := provider.Label(), "cartesia.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 16000; got != want {
		t.Fatalf("SampleRate() = %d, want configured sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "sonic-3"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "Cartesia"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want streaming without aligned transcript", caps)
	}

	stream, err := provider.Synthesize(context.Background(), "hola")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotHeaders.Get("X-API-Key"), "test-cartesia-key"; got != want {
		t.Fatalf("X-API-Key = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("Cartesia-Version"), "2025-01-01"; got != want {
		t.Fatalf("Cartesia-Version = %q, want %q", got, want)
	}
	if got, want := gotPayload["transcript"], "hola"; got != want {
		t.Fatalf("transcript = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["language"], "es"; got != want {
		t.Fatalf("language = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["pronunciation_dict_id"], "dict-1"; got != want {
		t.Fatalf("pronunciation_dict_id = %#v, want %#v", got, want)
	}
	voice, _ := gotPayload["voice"].(map[string]any)
	if got, want := voice["mode"], "embedding"; got != want {
		t.Fatalf("voice.mode = %#v, want %#v in %#v", got, want, voice)
	}
	embedding, _ := voice["embedding"].([]any)
	if len(embedding) != 3 || embedding[0] != 0.1 || embedding[1] != 0.2 || embedding[2] != 0.3 {
		t.Fatalf("voice.embedding = %#v, want [0.1 0.2 0.3]", voice["embedding"])
	}
	outputFormat, _ := gotPayload["output_format"].(map[string]any)
	if got, want := outputFormat["encoding"], "pcm_mulaw"; got != want {
		t.Fatalf("output_format.encoding = %#v, want %#v", got, want)
	}
	if got, want := outputFormat["sample_rate"], float64(16000); got != want {
		t.Fatalf("output_format.sample_rate = %#v, want %#v", got, want)
	}
	generationConfig, _ := gotPayload["generation_config"].(map[string]any)
	if got, want := generationConfig["speed"], 1.2; got != want {
		t.Fatalf("generation_config.speed = %#v, want %#v", got, want)
	}
	if got, want := generationConfig["emotion"], "happy"; got != want {
		t.Fatalf("generation_config.emotion = %#v, want %#v", got, want)
	}
	if got, want := generationConfig["volume"], 1.1; got != want {
		t.Fatalf("generation_config.volume = %#v, want %#v", got, want)
	}
}

func TestGradiumTTSFallbackPassesReferenceOptions(t *testing.T) {
	type wsRecord struct {
		apiKey    string
		apiSource string
		setup     map[string]any
	}
	records := make(chan wsRecord, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read gradium setup payload: %v", err)
			return
		}
		var setup map[string]any
		if err := json.Unmarshal(payload, &setup); err != nil {
			t.Errorf("decode gradium setup payload: %v", err)
			return
		}
		records <- wsRecord{
			apiKey:    r.Header.Get("x-api-key"),
			apiSource: r.Header.Get("x-api-source"),
			setup:     setup,
		}
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	provider, err := fallbackTTSFromProvider(AppConfig{
		GradiumAPIKey:          "test-gradium-key",
		TTSBaseURL:             endpoint,
		TTSModel:               "tts-test",
		TTSVoice:               "voice-test",
		TTSVoiceID:             "voice-id-test",
		TTSPronunciationDictID: "pronunciation-test",
		TTSJSONConfig:          map[string]any{"style": "clear", "pace": 1.2},
	}, providerGradium)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*gradium.GradiumTTS); !ok {
		t.Fatalf("provider type = %T, want *gradium.GradiumTTS", provider)
	}
	if got, want := provider.Label(), "gradium.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 48000; got != want {
		t.Fatalf("SampleRate() = %d, want reference sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "unknown"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "Gradium"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference streaming without aligned transcript", caps)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case record := <-records:
		if got, want := record.apiKey, "test-gradium-key"; got != want {
			t.Fatalf("x-api-key = %q, want %q", got, want)
		}
		if got, want := record.apiSource, "livekit"; got != want {
			t.Fatalf("x-api-source = %q, want %q", got, want)
		}
		if got, want := record.setup["type"], "setup"; got != want {
			t.Fatalf("setup.type = %#v, want %#v", got, want)
		}
		if got, want := record.setup["model_name"], "tts-test"; got != want {
			t.Fatalf("setup.model_name = %#v, want %#v", got, want)
		}
		if got, want := record.setup["output_format"], "pcm"; got != want {
			t.Fatalf("setup.output_format = %#v, want %#v", got, want)
		}
		if got, want := record.setup["voice"], "voice-test"; got != want {
			t.Fatalf("setup.voice = %#v, want %#v", got, want)
		}
		if got, want := record.setup["voice_id"], "voice-id-test"; got != want {
			t.Fatalf("setup.voice_id = %#v, want %#v", got, want)
		}
		if got, want := record.setup["pronunciation_id"], "pronunciation-test"; got != want {
			t.Fatalf("setup.pronunciation_id = %#v, want %#v", got, want)
		}
		var config map[string]any
		if err := json.Unmarshal([]byte(record.setup["json_config"].(string)), &config); err != nil {
			t.Fatalf("decode setup.json_config: %v", err)
		}
		if got, want := config["style"], "clear"; got != want {
			t.Fatalf("json_config.style = %#v, want %#v", got, want)
		}
		if got, want := config["pace"], 1.2; got != want {
			t.Fatalf("json_config.pace = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Gradium setup payload")
	}
}

func TestSLNGTTSFallbackPassesModelOptions(t *testing.T) {
	initPayloads := make(chan map[string]any, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read init payload: %v", err)
			return
		}
		var init map[string]any
		if err := json.Unmarshal(payload, &init); err != nil {
			t.Errorf("decode init payload: %v", err)
			return
		}
		initPayloads <- init
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	provider, err := fallbackTTSFromProvider(AppConfig{
		TTSBaseURL: endpoint,
		TTSModel:   "sarvam/bulbul:v3",
		TTSVoice:   "voice-1",
		TTSModelOptions: map[string]any{
			"target_language_code": "hi",
			"pace":                 0.85,
			"min_buffer_size":      2,
			"auto_mode":            true,
		},
		SLNGAPIKey: "test-slng-key",
	}, providerSLNG)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case init := <-initPayloads:
		if init["language"] != "hi-IN" {
			t.Fatalf("language = %#v, want hi-IN in %#v", init["language"], init)
		}
		config, _ := init["config"].(map[string]any)
		if config["language"] != "hi-IN" {
			t.Fatalf("config.language = %#v, want hi-IN in %#v", config["language"], init)
		}
		if config["speech_sample_rate"] != "24000" {
			t.Fatalf("config.speech_sample_rate = %#v, want 24000 in %#v", config["speech_sample_rate"], init)
		}
		if config["pace"] != 0.85 {
			t.Fatalf("config.pace = %#v, want 0.85 in %#v", config["pace"], init)
		}
		if config["min_buffer_size"] != float64(2) {
			t.Fatalf("config.min_buffer_size = %#v, want 2 in %#v", config["min_buffer_size"], init)
		}
		if _, ok := config["target_language_code"]; ok {
			t.Fatalf("config.target_language_code present in %#v", init)
		}
		if _, ok := config["auto_mode"]; ok {
			t.Fatalf("config.auto_mode present in %#v", init)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SLNG init payload")
	}
}

func TestResembleTTSFallbackPassesReferenceOptions(t *testing.T) {
	sampleRate := 24000
	provider, err := fallbackTTSFromProvider(AppConfig{
		ResembleAPIKey: "test-resemble-key",
		TTSModel:       "chatterbox-turbo",
		TTSVoice:       "voice-2",
		TTSSampleRate:  &sampleRate,
	}, providerResemble)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*resemble.ResembleTTS); !ok {
		t.Fatalf("provider type = %T, want *resemble.ResembleTTS", provider)
	}
	if got, want := provider.Label(), "resemble.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 24000; got != want {
		t.Fatalf("SampleRate() = %d, want reference configured sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "chatterbox-turbo"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "Resemble"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference streaming without aligned transcript", caps)
	}
}

func TestRespeecherTTSFallbackPassesReferenceOptions(t *testing.T) {
	sampleRate := 16000
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]any
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("audio")),
			Header:     make(http.Header),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	provider, err := fallbackTTSFromProvider(AppConfig{
		RespeecherAPIKey: "test-respeecher-key",
		TTSBaseURL:       "https://respeecher.example/v1/",
		TTSModel:         "/public/tts/ua-rt",
		TTSVoice:         "custom-voice",
		TTSSampleRate:    &sampleRate,
		TTSJSONConfig: map[string]any{
			"temperature": 0.45,
			"pitch":       1.1,
		},
	}, providerRespeecher)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if got, want := provider.Label(), "respeecher.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 16000; got != want {
		t.Fatalf("SampleRate() = %d, want reference configured sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "/public/tts/ua-rt"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "Respeecher"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotURL, "https://respeecher.example/v1/public/tts/ua-rt/tts/bytes"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("X-API-Key"), "test-respeecher-key"; got != want {
		t.Fatalf("X-API-Key = %q, want %q", got, want)
	}
	outputFormat, _ := gotPayload["output_format"].(map[string]any)
	if got, want := outputFormat["sample_rate"], float64(16000); got != want {
		t.Fatalf("output_format.sample_rate = %#v, want %#v", got, want)
	}
	voice, _ := gotPayload["voice"].(map[string]any)
	if got, want := voice["id"], "custom-voice"; got != want {
		t.Fatalf("voice.id = %#v, want %#v", got, want)
	}
	samplingParams, _ := voice["sampling_params"].(map[string]any)
	if got, want := samplingParams["temperature"], float64(0.45); got != want {
		t.Fatalf("sampling_params.temperature = %#v, want %#v", got, want)
	}
	if got, want := samplingParams["pitch"], float64(1.1); got != want {
		t.Fatalf("sampling_params.pitch = %#v, want %#v", got, want)
	}
}

func TestSarvamTTSFallbackPassesReferenceOptions(t *testing.T) {
	sampleRate := 24000
	temperature := 0.7
	bitRate := 96
	bufferSize := 80
	chunkLength := 240
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]any
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("audio")),
			Header:     make(http.Header),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	provider, err := fallbackTTSFromProvider(AppConfig{
		SarvamAPIKey:           "test-sarvam-key",
		TTSBaseURL:             "https://sarvam.example/tts",
		TTSModel:               "bulbul:v3",
		TTSVoice:               "ritu",
		TTSLanguage:            "hi-IN",
		TTSSampleRate:          &sampleRate,
		TTSTemperature:         &temperature,
		TTSBitRate:             &bitRate,
		TTSBufferSize:          &bufferSize,
		TTSChunkLength:         &chunkLength,
		TTSPronunciationDictID: "dict-123",
		TTSEncoding:            "wav",
	}, providerSarvam)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if got, want := provider.Label(), "sarvam.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 24000; got != want {
		t.Fatalf("SampleRate() = %d, want reference configured sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "bulbul:v3"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "Sarvam"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotURL, "https://sarvam.example/tts"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("api-subscription-key"), "test-sarvam-key"; got != want {
		t.Fatalf("api-subscription-key = %q, want %q", got, want)
	}
	if got, want := gotPayload["target_language_code"], "hi-IN"; got != want {
		t.Fatalf("target_language_code = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["speaker"], "ritu"; got != want {
		t.Fatalf("speaker = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["model"], "bulbul:v3"; got != want {
		t.Fatalf("model = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["speech_sample_rate"], float64(24000); got != want {
		t.Fatalf("speech_sample_rate = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["temperature"], float64(0.7); got != want {
		t.Fatalf("temperature = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["output_audio_bitrate"], "96"; got != want {
		t.Fatalf("output_audio_bitrate = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["min_buffer_size"], float64(80); got != want {
		t.Fatalf("min_buffer_size = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["max_chunk_length"], float64(240); got != want {
		t.Fatalf("max_chunk_length = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["dict_id"], "dict-123"; got != want {
		t.Fatalf("dict_id = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["output_audio_codec"], "wav"; got != want {
		t.Fatalf("output_audio_codec = %#v, want %#v", got, want)
	}
}

func TestSmallestAITTSFallbackPassesReferenceOptions(t *testing.T) {
	sampleRate := 44100
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]any
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("audio")),
			Header:     make(http.Header),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	provider, err := fallbackTTSFromProvider(AppConfig{
		SmallestAIAPIKey:  "test-smallest-key",
		TTSBaseURL:        "https://smallest.example/waves/v1/",
		TTSWebsocketURL:   "wss://smallest.example/waves/v1/tts/live",
		TTSModel:          "lightning_v3.1",
		TTSVoice:          "sophia",
		TTSSampleRate:     &sampleRate,
		TTSSpeed:          1.4,
		TTSLanguage:       "auto",
		TTSResponseFormat: "wav",
	}, providerSmallestAI)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if got, want := provider.Label(), "smallestai.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 44100; got != want {
		t.Fatalf("SampleRate() = %d, want reference configured sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "lightning_v3.1"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "SmallestAI"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotURL, "https://smallest.example/waves/v1/tts"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("Authorization"), "Bearer test-smallest-key"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := gotPayload["model"], "lightning_v3.1"; got != want {
		t.Fatalf("model = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["voice_id"], "sophia"; got != want {
		t.Fatalf("voice_id = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["sample_rate"], float64(44100); got != want {
		t.Fatalf("sample_rate = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["speed"], float64(1.4); got != want {
		t.Fatalf("speed = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["language"], "auto"; got != want {
		t.Fatalf("language = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["output_format"], "wav"; got != want {
		t.Fatalf("output_format = %#v, want %#v", got, want)
	}
}

func TestSonioxTTSFallbackPassesReferenceOptions(t *testing.T) {
	sampleRate := 48000
	bitRate := 192000
	provider, err := fallbackTTSFromProvider(AppConfig{
		SonioxAPIKey:    "test-soniox-key",
		TTSWebsocketURL: "ws://soniox.example/tts",
		TTSModel:        "tts-custom",
		TTSLanguage:     "es",
		TTSVoice:        "Adrian",
		TTSEncoding:     "mp3",
		TTSSampleRate:   &sampleRate,
		TTSBitRate:      &bitRate,
	}, providerSoniox)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*soniox.SonioxTTS); !ok {
		t.Fatalf("provider type = %T, want *soniox.SonioxTTS", provider)
	}
	if got, want := provider.Label(), "soniox.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 48000; got != want {
		t.Fatalf("SampleRate() = %d, want reference configured sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "tts-custom"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "Soniox"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference streaming without aligned transcript", caps)
	}
}

func TestSpeechmaticsTTSFallbackPassesReferenceOptions(t *testing.T) {
	sampleRate := 24000
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]string
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("audio")),
			Header:     make(http.Header),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	provider, err := fallbackTTSFromProvider(AppConfig{
		SpeechmaticsAPIKey: "test-speechmatics-key",
		TTSBaseURL:         "https://tts.example.com",
		TTSVoice:           "theo",
		TTSSampleRate:      &sampleRate,
	}, providerSpeechmatics)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*speechmatics.SpeechmaticsTTS); !ok {
		t.Fatalf("provider type = %T, want *speechmatics.SpeechmaticsTTS", provider)
	}
	if got, want := provider.Label(), "speechmatics.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 24000; got != want {
		t.Fatalf("SampleRate() = %d, want reference configured sample rate %d", got, want)
	}
	if got, want := tts.Provider(provider), "Speechmatics"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference non-streaming without aligned transcript", caps)
	}
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if !strings.HasPrefix(gotURL, "https://tts.example.com/generate/theo?") {
		t.Fatalf("request URL = %q, want configured generate endpoint", gotURL)
	}
	if !strings.Contains(gotURL, "output_format=pcm_24000") {
		t.Fatalf("request URL = %q, want output_format=pcm_24000", gotURL)
	}
	if got, want := gotHeaders.Get("Authorization"), "Bearer test-speechmatics-key"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := gotPayload["text"], "hello"; got != want {
		t.Fatalf("payload text = %q, want %q", got, want)
	}
}

func TestSpitchTTSFallbackPassesReferenceOptions(t *testing.T) {
	sampleRate := 24000
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]any
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("audio")),
			Header:     make(http.Header),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	provider, err := fallbackTTSFromProvider(AppConfig{
		SpitchAPIKey:      "test-spitch-key",
		TTSBaseURL:        "https://spitch.example/",
		TTSVoice:          "amina",
		TTSLanguage:       "fr",
		TTSResponseFormat: "wav",
		TTSSampleRate:     &sampleRate,
	}, providerSpitch)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*spitch.SpitchTTS); !ok {
		t.Fatalf("provider type = %T, want *spitch.SpitchTTS", provider)
	}
	if got, want := provider.Label(), "spitch.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 24000; got != want {
		t.Fatalf("SampleRate() = %d, want reference sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "unknown"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "Spitch"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference non-streaming without aligned transcript", caps)
	}
	stream, err := provider.Synthesize(context.Background(), "bonjour")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotURL, "https://spitch.example/tts/v1/synthesize"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("Authorization"), "Bearer test-spitch-key"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := gotPayload["text"], "bonjour"; got != want {
		t.Fatalf("payload text = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["voice"], "amina"; got != want {
		t.Fatalf("payload voice = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["language"], "fr"; got != want {
		t.Fatalf("payload language = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["format"], "wav"; got != want {
		t.Fatalf("payload format = %#v, want %#v", got, want)
	}
}

func TestXaiTTSFallbackPassesReferenceOptions(t *testing.T) {
	type wsRecord struct {
		voice         string
		language      string
		codec         string
		sampleRate    string
		authorization string
		messages      []map[string]any
	}
	records := make(chan wsRecord, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		record := wsRecord{
			voice:         r.URL.Query().Get("voice"),
			language:      r.URL.Query().Get("language"),
			codec:         r.URL.Query().Get("codec"),
			sampleRate:    r.URL.Query().Get("sample_rate"),
			authorization: r.Header.Get("Authorization"),
		}
		for i := 0; i < 2; i++ {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Errorf("read xai tts message: %v", err)
				return
			}
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Errorf("decode xai tts message: %v", err)
				return
			}
			record.messages = append(record.messages, message)
		}
		records <- record
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	provider, err := fallbackTTSFromProvider(AppConfig{
		XAIAPIKey:       "test-xai-key",
		TTSWebsocketURL: endpoint,
		TTSVoice:        "eve",
		TTSLanguage:     "ja",
	}, providerXAI)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*xai.XaiTTS); !ok {
		t.Fatalf("provider type = %T, want *xai.XaiTTS", provider)
	}
	if got, want := provider.Label(), "xai.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 24000; got != want {
		t.Fatalf("SampleRate() = %d, want reference sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "unknown"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "xAI"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference streaming without aligned transcript", caps)
	}
	stream, err := provider.Synthesize(context.Background(), "hello xai")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	select {
	case record := <-records:
		if got, want := record.voice, "eve"; got != want {
			t.Fatalf("query voice = %q, want %q", got, want)
		}
		if got, want := record.language, "ja"; got != want {
			t.Fatalf("query language = %q, want %q", got, want)
		}
		if got, want := record.codec, "pcm"; got != want {
			t.Fatalf("query codec = %q, want %q", got, want)
		}
		if got, want := record.sampleRate, "24000"; got != want {
			t.Fatalf("query sample_rate = %q, want %q", got, want)
		}
		if got, want := record.authorization, "Bearer test-xai-key"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		if len(record.messages) != 2 {
			t.Fatalf("messages = %#v, want text.delta and text.done", record.messages)
		}
		if got, want := record.messages[0]["type"], "text.delta"; got != want {
			t.Fatalf("first message type = %#v, want %#v", got, want)
		}
		if got, want := record.messages[0]["delta"], "hello xai"; got != want {
			t.Fatalf("first message delta = %#v, want %#v", got, want)
		}
		if got, want := record.messages[1]["type"], "text.done"; got != want {
			t.Fatalf("second message type = %#v, want %#v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for xai websocket request")
	}
}

func TestFishAudioTTSFallbackPassesReferenceOptions(t *testing.T) {
	sampleRate := 48000
	chunkLength := 250
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]any
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		if err := msgpack.Unmarshal(body, &gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("audio")),
			Header:     make(http.Header),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	provider, err := fallbackTTSFromProvider(AppConfig{
		FishAudioAPIKey:   "test-fish-key",
		TTSBaseURL:        "https://fish.example/",
		TTSModel:          "s1",
		TTSVoice:          "voice-2",
		TTSResponseFormat: "opus",
		TTSSampleRate:     &sampleRate,
		TTSLatencyMode:    "low",
		TTSChunkLength:    &chunkLength,
	}, providerFishAudio)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*fishaudio.FishAudioTTS); !ok {
		t.Fatalf("provider type = %T, want *fishaudio.FishAudioTTS", provider)
	}
	if got, want := provider.Label(), "fishaudio.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 48000; got != want {
		t.Fatalf("SampleRate() = %d, want configured reference sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "s1"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "FishAudio"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference streaming without aligned transcript", caps)
	}
	stream, err := provider.Synthesize(context.Background(), "hello fish")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotURL, "https://fish.example/v1/tts"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("Authorization"), "Bearer test-fish-key"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("Content-Type"), "application/msgpack"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("model"), "s1"; got != want {
		t.Fatalf("model header = %q, want %q", got, want)
	}
	if got, want := gotPayload["text"], "hello fish"; got != want {
		t.Fatalf("payload text = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["reference_id"], "voice-2"; got != want {
		t.Fatalf("payload reference_id = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["format"], "opus"; got != want {
		t.Fatalf("payload format = %#v, want %#v", got, want)
	}
	if got, want := fmt.Sprint(gotPayload["sample_rate"]), "48000"; got != want {
		t.Fatalf("payload sample_rate = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["latency"], "low"; got != want {
		t.Fatalf("payload latency = %#v, want %#v", got, want)
	}
	if got, want := fmt.Sprint(gotPayload["chunk_length"]), "250"; got != want {
		t.Fatalf("payload chunk_length = %#v, want %#v", got, want)
	}
}

func TestAsyncAITTSFallbackPassesReferenceOptions(t *testing.T) {
	t.Setenv("ASYNCAI_API_KEY", "test-asyncai-key")
	sampleRate := 24000
	initPayloads := make(chan map[string]any, 1)
	requests := make(chan *http.Request, 1)
	upgrader := websocket.Upgrader{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Clone(r.Context())
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read asyncai init payload: %v", err)
			return
		}
		var init map[string]any
		if err := json.Unmarshal(payload, &init); err != nil {
			t.Errorf("decode asyncai init payload: %v", err)
			return
		}
		initPayloads <- init
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test websocket server: %v", err)
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	server.Start()
	defer server.Close()

	provider, err := fallbackTTSFromProvider(AppConfig{
		TTSBaseURL:    server.URL,
		TTSModel:      "async_custom",
		TTSVoice:      "voice-2",
		TTSLanguage:   "hi",
		TTSEncoding:   "pcm_mulaw",
		TTSSampleRate: &sampleRate,
	}, providerAsyncAI)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*asyncai.AsyncAITTS); !ok {
		t.Fatalf("provider type = %T, want *asyncai.AsyncAITTS", provider)
	}
	if got, want := provider.Label(), "asyncai.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 24000; got != want {
		t.Fatalf("SampleRate() = %d, want configured reference sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "async_custom"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "AsyncAI"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference streaming without aligned transcript", caps)
	}
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case req := <-requests:
		if got, want := req.URL.Path, "/text_to_speech/websocket/ws"; got != want {
			t.Fatalf("websocket path = %q, want %q", got, want)
		}
		if got, want := req.URL.Query().Get("api_key"), "test-asyncai-key"; got != want {
			t.Fatalf("api_key query = %q, want %q", got, want)
		}
		if got, want := req.URL.Query().Get("version"), "v1"; got != want {
			t.Fatalf("version query = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for AsyncAI websocket request")
	}

	select {
	case init := <-initPayloads:
		if got, want := init["model_id"], "async_custom"; got != want {
			t.Fatalf("model_id = %#v, want %#v", got, want)
		}
		if got, want := init["language"], "hi"; got != want {
			t.Fatalf("language = %#v, want %#v", got, want)
		}
		voice, _ := init["voice"].(map[string]any)
		if got, want := voice["mode"], "id"; got != want {
			t.Fatalf("voice.mode = %#v, want %#v in %#v", got, want, init)
		}
		if got, want := voice["id"], "voice-2"; got != want {
			t.Fatalf("voice.id = %#v, want %#v in %#v", got, want, init)
		}
		output, _ := init["output_format"].(map[string]any)
		if got, want := output["container"], "raw"; got != want {
			t.Fatalf("output_format.container = %#v, want %#v in %#v", got, want, init)
		}
		if got, want := output["encoding"], "pcm_mulaw"; got != want {
			t.Fatalf("output_format.encoding = %#v, want %#v in %#v", got, want, init)
		}
		if got, want := output["sample_rate"], float64(24000); got != want {
			t.Fatalf("output_format.sample_rate = %#v, want %#v in %#v", got, want, init)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for AsyncAI init payload")
	}
}

func TestCambaiTTSFallbackPassesReferenceOptions(t *testing.T) {
	t.Setenv("CAMB_API_KEY", "test-cambai-key")
	enhanceNamedEntities := true
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]any
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("audio")),
			Header:     make(http.Header),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	provider, err := fallbackTTSFromProvider(AppConfig{
		TTSBaseURL:              "https://cambai.example/apis/",
		TTSVoice:                "42",
		TTSLanguage:             "fr-fr",
		TTSModel:                "mars-pro",
		TTSEncoding:             "wav",
		TTSInstructions:         "warm and concise",
		TTSEnhanceNamedEntities: &enhanceNamedEntities,
	}, providerCambai)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*cambai.CambaiTTS); !ok {
		t.Fatalf("provider type = %T, want *cambai.CambaiTTS", provider)
	}
	if got, want := provider.Label(), "cambai.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 48000; got != want {
		t.Fatalf("SampleRate() = %d, want mars-pro reference sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "mars-pro"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "Camb.ai"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference non-streaming without aligned transcript", caps)
	}
	stream, err := provider.Synthesize(context.Background(), "bonjour")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotURL, "https://cambai.example/apis/tts-stream"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("x-api-key"), "test-cambai-key"; got != want {
		t.Fatalf("x-api-key = %q, want %q", got, want)
	}
	if got, want := gotPayload["text"], "bonjour"; got != want {
		t.Fatalf("payload text = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["voice_id"], float64(42); got != want {
		t.Fatalf("payload voice_id = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["language"], "fr-fr"; got != want {
		t.Fatalf("payload language = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["speech_model"], "mars-pro"; got != want {
		t.Fatalf("payload speech_model = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["user_instructions"], "warm and concise"; got != want {
		t.Fatalf("payload user_instructions = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["enhance_named_entities_pronunciation"], true; got != want {
		t.Fatalf("payload enhance_named_entities_pronunciation = %#v, want %#v", got, want)
	}
	outputConfig, _ := gotPayload["output_configuration"].(map[string]any)
	if got, want := outputConfig["format"], "wav"; got != want {
		t.Fatalf("payload output_configuration.format = %#v, want %#v in %#v", got, want, gotPayload)
	}
}

func TestGnaniTTSFallbackPassesReferenceOptions(t *testing.T) {
	sampleRate := 22050
	numChannels := 2
	sampleWidth := 3
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]any
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("audio")),
			Header:     make(http.Header),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	provider, err := fallbackTTSFromProvider(AppConfig{
		GnaniAPIKey:         "test-gnani-key",
		TTSBaseURL:          "https://gnani.example/",
		TTSVoice:            "Simran",
		TTSModel:            "vachana-custom",
		TTSSampleRate:       &sampleRate,
		TTSEncoding:         "oggopus",
		TTSResponseFormat:   "ogg",
		TTSNumberOfChannels: &numChannels,
		TTSSampleWidth:      &sampleWidth,
		TTSLanguage:         "kn",
	}, providerGnani)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*gnani.TTS); !ok {
		t.Fatalf("provider type = %T, want *gnani.TTS", provider)
	}
	if got, want := provider.Label(), "gnani.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 22050; got != want {
		t.Fatalf("SampleRate() = %d, want configured reference sample rate %d", got, want)
	}
	if got, want := provider.NumChannels(), 2; got != want {
		t.Fatalf("NumChannels() = %d, want configured channels %d", got, want)
	}
	if got, want := tts.Model(provider), "vachana-custom"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "Gnani"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference streaming without aligned transcript", caps)
	}
	stream, err := provider.Synthesize(context.Background(), "namaste")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotURL, "https://gnani.example/api/v1/tts/inference"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("X-API-Key-ID"), "test-gnani-key"; got != want {
		t.Fatalf("X-API-Key-ID = %q, want %q", got, want)
	}
	if got, want := gotPayload["text"], "namaste"; got != want {
		t.Fatalf("payload text = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["voice"], "Simran"; got != want {
		t.Fatalf("payload voice = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["model"], "vachana-custom"; got != want {
		t.Fatalf("payload model = %#v, want %#v", got, want)
	}
	audioConfig, _ := gotPayload["audio_config"].(map[string]any)
	if got, want := audioConfig["sample_rate"], float64(22050); got != want {
		t.Fatalf("audio_config.sample_rate = %#v, want %#v in %#v", got, want, gotPayload)
	}
	if got, want := audioConfig["encoding"], "oggopus"; got != want {
		t.Fatalf("audio_config.encoding = %#v, want %#v in %#v", got, want, gotPayload)
	}
	if got, want := audioConfig["container"], "ogg"; got != want {
		t.Fatalf("audio_config.container = %#v, want %#v in %#v", got, want, gotPayload)
	}
	if got, want := audioConfig["num_channels"], float64(2); got != want {
		t.Fatalf("audio_config.num_channels = %#v, want %#v in %#v", got, want, gotPayload)
	}
	if got, want := audioConfig["sample_width"], float64(3); got != want {
		t.Fatalf("audio_config.sample_width = %#v, want %#v in %#v", got, want, gotPayload)
	}
}

func TestHumeTTSFallbackPassesReferenceOptions(t *testing.T) {
	speed := 1.2
	trailingSilence := 0.4
	instantMode := false
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]any
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"audio":"AAE="}` + "\n")),
			Header:     make(http.Header),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	provider, err := fallbackTTSFromProvider(AppConfig{
		HumeAPIKey:         "test-hume-key",
		TTSBaseURL:         "https://hume.example/",
		TTSModel:           "2",
		TTSVoice:           "Narrator",
		TTSVoiceProvider:   "CUSTOM_VOICE",
		TTSInstructions:    "calm",
		TTSSpeed:           speed,
		TTSTrailingSilence: &trailingSilence,
		TTSInstantMode:     &instantMode,
		TTSResponseFormat:  "wav",
	}, providerHume)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*hume.HumeTTS); !ok {
		t.Fatalf("provider type = %T, want *hume.HumeTTS", provider)
	}
	if got, want := provider.Label(), "hume.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 48000; got != want {
		t.Fatalf("SampleRate() = %d, want reference sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "Octave"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "Hume"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference non-streaming without aligned transcript", caps)
	}
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotURL, "https://hume.example/v0/tts/stream/json"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("X-Hume-Api-Key"), "test-hume-key"; got != want {
		t.Fatalf("X-Hume-Api-Key = %q, want %q", got, want)
	}
	if got, want := gotPayload["version"], "2"; got != want {
		t.Fatalf("payload version = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["strip_headers"], true; got != want {
		t.Fatalf("payload strip_headers = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["instant_mode"], false; got != want {
		t.Fatalf("payload instant_mode = %#v, want %#v", got, want)
	}
	format, _ := gotPayload["format"].(map[string]any)
	if got, want := format["type"], "wav"; got != want {
		t.Fatalf("payload format.type = %#v, want %#v in %#v", got, want, gotPayload)
	}
	utterances, _ := gotPayload["utterances"].([]any)
	if len(utterances) != 1 {
		t.Fatalf("payload utterances = %#v, want one utterance", gotPayload["utterances"])
	}
	utterance, _ := utterances[0].(map[string]any)
	if got, want := utterance["text"], "hello"; got != want {
		t.Fatalf("utterance text = %#v, want %#v", got, want)
	}
	if got, want := utterance["description"], "calm"; got != want {
		t.Fatalf("utterance description = %#v, want %#v", got, want)
	}
	if got, want := utterance["speed"], float64(1.2); got != want {
		t.Fatalf("utterance speed = %#v, want %#v", got, want)
	}
	if got, want := utterance["trailing_silence"], float64(0.4); got != want {
		t.Fatalf("utterance trailing_silence = %#v, want %#v", got, want)
	}
	voice, _ := utterance["voice"].(map[string]any)
	if got, want := voice["name"], "Narrator"; got != want {
		t.Fatalf("voice name = %#v, want %#v", got, want)
	}
	if got, want := voice["provider"], "CUSTOM_VOICE"; got != want {
		t.Fatalf("voice provider = %#v, want %#v", got, want)
	}
}

func TestMinimaxTTSFallbackPassesReferenceOptions(t *testing.T) {
	sampleRate := 44100
	bitRate := 256000
	volume := 2.0
	pitch := -2
	textNormalization := true
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]any
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("data:{}\n")),
			Header:     make(http.Header),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	provider, err := fallbackTTSFromProvider(AppConfig{
		MinimaxAPIKey:        "test-minimax-key",
		TTSBaseURL:           "https://minimax.example/",
		TTSModel:             "speech-2.6-hd",
		TTSVoice:             "voice-2",
		TTSSampleRate:        &sampleRate,
		TTSBitRate:           &bitRate,
		TTSResponseFormat:    "wav",
		TTSEmotion:           "fluent",
		TTSSpeed:             1.4,
		TTSVolume:            &volume,
		TTSPitch:             &pitch,
		TTSTextNormalization: &textNormalization,
	}, providerMinimax)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*minimax.MinimaxTTS); !ok {
		t.Fatalf("provider type = %T, want *minimax.MinimaxTTS", provider)
	}
	if got, want := provider.Label(), "minimax.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 44100; got != want {
		t.Fatalf("SampleRate() = %d, want configured reference sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "speech-2.6-hd"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "MiniMax"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference streaming without aligned transcript", caps)
	}
	stream, err := provider.Synthesize(context.Background(), "hola")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotURL, "https://minimax.example/v1/t2a_v2"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("Authorization"), "Bearer test-minimax-key"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := gotPayload["model"], "speech-2.6-hd"; got != want {
		t.Fatalf("payload model = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["text"], "hola"; got != want {
		t.Fatalf("payload text = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["stream"], true; got != want {
		t.Fatalf("payload stream = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["text_normalization"], true; got != want {
		t.Fatalf("payload text_normalization = %#v, want %#v", got, want)
	}
	voiceSetting, _ := gotPayload["voice_setting"].(map[string]any)
	if got, want := voiceSetting["voice_id"], "voice-2"; got != want {
		t.Fatalf("voice_setting.voice_id = %#v, want %#v in %#v", got, want, gotPayload)
	}
	if got, want := voiceSetting["emotion"], "fluent"; got != want {
		t.Fatalf("voice_setting.emotion = %#v, want %#v in %#v", got, want, gotPayload)
	}
	if got, want := voiceSetting["speed"], float64(1.4); got != want {
		t.Fatalf("voice_setting.speed = %#v, want %#v in %#v", got, want, gotPayload)
	}
	if got, want := voiceSetting["vol"], float64(2.0); got != want {
		t.Fatalf("voice_setting.vol = %#v, want %#v in %#v", got, want, gotPayload)
	}
	if got, want := voiceSetting["pitch"], float64(-2); got != want {
		t.Fatalf("voice_setting.pitch = %#v, want %#v in %#v", got, want, gotPayload)
	}
	audioSetting, _ := gotPayload["audio_setting"].(map[string]any)
	if got, want := audioSetting["sample_rate"], float64(44100); got != want {
		t.Fatalf("audio_setting.sample_rate = %#v, want %#v in %#v", got, want, gotPayload)
	}
	if got, want := audioSetting["bitrate"], float64(256000); got != want {
		t.Fatalf("audio_setting.bitrate = %#v, want %#v in %#v", got, want, gotPayload)
	}
	if got, want := audioSetting["format"], "wav"; got != want {
		t.Fatalf("audio_setting.format = %#v, want %#v in %#v", got, want, gotPayload)
	}
}

func TestInworldTTSFallbackPassesReferenceOptions(t *testing.T) {
	bitRate := 128000
	sampleRate := 44100
	speakingRate := 1.2
	temperature := 0.8
	textNormalization := false
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]any
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"audioContent":"AAE="}` + "\n")),
			Header:     make(http.Header),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	provider, err := fallbackTTSFromProvider(AppConfig{
		InworldAPIKey:                 "test-inworld-key",
		TTSBaseURL:                    "https://inworld.example/",
		TTSWebsocketURL:               "wss://inworld.example/",
		TTSVoice:                      "Ava",
		TTSModel:                      "inworld-tts-2",
		TTSEncoding:                   "MP3",
		TTSBitRate:                    &bitRate,
		TTSSampleRate:                 &sampleRate,
		TTSSpeakingRate:               &speakingRate,
		TTSTemperature:                &temperature,
		TTSLanguage:                   "en-US",
		TTSTimestampType:              "WORD",
		TTSTextNormalization:          &textNormalization,
		TTSDeliveryMode:               "STABLE",
		TTSTimestampTransportStrategy: "SYNC",
	}, providerInworld)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*inworld.InworldTTS); !ok {
		t.Fatalf("provider type = %T, want *inworld.InworldTTS", provider)
	}
	if got, want := provider.Label(), "inworld.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 44100; got != want {
		t.Fatalf("SampleRate() = %d, want configured reference sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "inworld-tts-2"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "Inworld"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || !caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference streaming with aligned transcript when timestamp type is WORD", caps)
	}
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotURL, "https://inworld.example/tts/v1/voice:stream"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("Authorization"), "Basic test-inworld-key"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := gotPayload["text"], "hello"; got != want {
		t.Fatalf("payload text = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["voiceId"], "Ava"; got != want {
		t.Fatalf("payload voiceId = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["modelId"], "inworld-tts-2"; got != want {
		t.Fatalf("payload modelId = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["language"], "en-US"; got != want {
		t.Fatalf("payload language = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["timestampType"], "WORD"; got != want {
		t.Fatalf("payload timestampType = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["applyTextNormalization"], "OFF"; got != want {
		t.Fatalf("payload applyTextNormalization = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["deliveryMode"], "STABLE"; got != want {
		t.Fatalf("payload deliveryMode = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["timestampTransportStrategy"], "SYNC"; got != want {
		t.Fatalf("payload timestampTransportStrategy = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["temperature"], float64(0.8); got != want {
		t.Fatalf("payload temperature = %#v, want %#v", got, want)
	}
	audioConfig, _ := gotPayload["audioConfig"].(map[string]any)
	if got, want := audioConfig["audioEncoding"], "MP3"; got != want {
		t.Fatalf("audioConfig.audioEncoding = %#v, want %#v in %#v", got, want, gotPayload)
	}
	if got, want := audioConfig["sampleRateHertz"], float64(44100); got != want {
		t.Fatalf("audioConfig.sampleRateHertz = %#v, want %#v in %#v", got, want, gotPayload)
	}
	if got, want := audioConfig["bitrate"], float64(128000); got != want {
		t.Fatalf("audioConfig.bitrate = %#v, want %#v in %#v", got, want, gotPayload)
	}
	if got, want := audioConfig["speakingRate"], float64(1.2); got != want {
		t.Fatalf("audioConfig.speakingRate = %#v, want %#v in %#v", got, want, gotPayload)
	}
}

func TestDefaultConfigFromEnvAcceptsTelnyxTTSFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_TTS_FALLBACK_PROVIDERS", "telnyx")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("TELNYX_API_KEY", "test-telnyx-key")
	t.Setenv("RTP_AGENT_TTS_VOICE", "Telnyx.NaturalHD.astra")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.TTS.Label(); got != "FallbackAdapter(openai.TTS)" {
		t.Fatalf("TTS label = %q, want fallback adapter around primary openai TTS", got)
	}
}

func TestDefaultConfigFromEnvAcceptsGroqTTSFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_TTS_FALLBACK_PROVIDERS", "groq")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("GROQ_API_KEY", "test-groq-key")
	t.Setenv("RTP_AGENT_TTS_MODEL", "playai-tts")
	t.Setenv("RTP_AGENT_TTS_VOICE", "Fritz-PlayAI")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.TTS.Label(); got != "FallbackAdapter(openai.TTS)" {
		t.Fatalf("TTS label = %q, want fallback adapter around primary openai TTS", got)
	}
}

func TestGroqTTSFallbackPassesReferenceOptions(t *testing.T) {
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]any
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/wav"}},
			Body:       io.NopCloser(strings.NewReader("audio")),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	provider, err := fallbackTTSFromProvider(AppConfig{
		GroqAPIKey: "test-groq-key",
		TTSBaseURL: "https://groq.example/openai/v1/",
		TTSModel:   "canopylabs/orpheus-arabic-saudi",
		TTSVoice:   "noura",
	}, providerGroq)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*groq.GroqTTS); !ok {
		t.Fatalf("provider type = %T, want *groq.GroqTTS", provider)
	}
	if got, want := provider.Label(), "groq.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := tts.Model(provider), "canopylabs/orpheus-arabic-saudi"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "Groq"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference non-streaming without aligned transcript", caps)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotURL, "https://groq.example/openai/v1/audio/speech"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("Authorization"), "Bearer test-groq-key"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := gotPayload["model"], "canopylabs/orpheus-arabic-saudi"; got != want {
		t.Fatalf("payload model = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["voice"], "noura"; got != want {
		t.Fatalf("payload voice = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["input"], "hello"; got != want {
		t.Fatalf("payload input = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["response_format"], "wav"; got != want {
		t.Fatalf("payload response_format = %#v, want %#v", got, want)
	}
}

func TestDefaultConfigFromEnvAcceptsNvidiaTTSFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_TTS_FALLBACK_PROVIDERS", "nvidia")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("NVIDIA_API_KEY", "test-nvidia-key")
	t.Setenv("RTP_AGENT_TTS_VOICE", "Magpie-Multilingual.EN-US.Leo")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.TTS.Label(); got != "FallbackAdapter(openai.TTS)" {
		t.Fatalf("TTS label = %q, want fallback adapter around primary openai TTS", got)
	}
}

func TestNvidiaTTSFallbackPassesReferenceLanguage(t *testing.T) {
	provider, err := fallbackTTSFromProvider(AppConfig{
		NvidiaAPIKey: "test-nvidia-key",
		TTSVoice:     "Magpie-Multilingual.ID-ID.Ayu",
		TTSLanguage:  "id-ID",
	}, providerNvidia)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	nvidiaProvider, ok := provider.(*nvidia.NvidiaTTS)
	if !ok {
		t.Fatalf("provider type = %T, want *nvidia.NvidiaTTS", provider)
	}
	if got, want := nvidiaProvider.Label(), "nvidia.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := nvidiaProvider.SampleRate(), 16000; got != want {
		t.Fatalf("SampleRate() = %d, want reference sample rate %d", got, want)
	}
	state := reflect.ValueOf(nvidiaProvider).Elem()
	if got, want := state.FieldByName("voice").String(), "Magpie-Multilingual.ID-ID.Ayu"; got != want {
		t.Fatalf("voice = %q, want %q", got, want)
	}
	if got, want := state.FieldByName("languageCode").String(), "id-ID"; got != want {
		t.Fatalf("languageCode = %q, want %q", got, want)
	}
}

func TestNvidiaTTSFallbackAllowsLocalRivaWithoutAPIKey(t *testing.T) {
	provider, err := fallbackTTSFromProvider(AppConfig{
		TTSBaseURL: "localhost:50051",
		TTSModelOptions: map[string]any{
			"use_ssl": false,
		},
	}, providerNvidia)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	nvidiaProvider, ok := provider.(*nvidia.NvidiaTTS)
	if !ok {
		t.Fatalf("provider type = %T, want *nvidia.NvidiaTTS", provider)
	}
	state := reflect.ValueOf(nvidiaProvider).Elem()
	if got, want := state.FieldByName("server").String(), "localhost:50051"; got != want {
		t.Fatalf("server = %q, want %q", got, want)
	}
	if state.FieldByName("useSSL").Bool() {
		t.Fatal("useSSL = true, want false for local Riva")
	}
}

func TestDefaultConfigFromEnvAcceptsMistralAITTSFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_TTS_FALLBACK_PROVIDERS", "mistralai")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("MISTRAL_API_KEY", "test-mistral-key")
	t.Setenv("RTP_AGENT_TTS_MODEL", "voxtral-tts-test")
	t.Setenv("RTP_AGENT_TTS_VOICE", "en_paul_neutral")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "pcm")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.TTS.Label(); got != "FallbackAdapter(openai.TTS)" {
		t.Fatalf("TTS label = %q, want fallback adapter around primary openai TTS", got)
	}
}

func TestMistralAITTSFallbackPassesReferenceOptions(t *testing.T) {
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]any
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(strings.Join([]string{
				`data: {"event":"speech.audio.delta","data":{"audio_data":"YXVkaW8="}}`,
				`data: {"event":"speech.audio.done","data":{"usage":{"prompt_tokens":2,"completion_tokens":4,"total_tokens":6}}}`,
				"",
			}, "\n"))),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	provider, err := fallbackTTSFromProvider(AppConfig{
		MistralAPIKey:     "test-mistral-key",
		TTSBaseURL:        "https://mistral.example/v1/",
		TTSModel:          "voxtral-mini-tts-2603",
		TTSVoice:          "voice-1",
		TTSResponseFormat: "opus",
	}, providerMistralAI)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*mistralai.MistralAITTS); !ok {
		t.Fatalf("provider type = %T, want *mistralai.MistralAITTS", provider)
	}
	if got, want := provider.Label(), "mistralai.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 24000; got != want {
		t.Fatalf("SampleRate() = %d, want reference sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "voxtral-mini-tts-2603"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "MistralAI"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference non-streaming without aligned transcript", caps)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotURL, "https://mistral.example/v1/audio/speech"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("Authorization"), "Bearer test-mistral-key"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("Accept"), "text/event-stream"; got != want {
		t.Fatalf("Accept = %q, want %q", got, want)
	}
	if got, want := gotPayload["model"], "voxtral-mini-tts-2603"; got != want {
		t.Fatalf("payload model = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["voice_id"], "voice-1"; got != want {
		t.Fatalf("payload voice_id = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["input"], "hello"; got != want {
		t.Fatalf("payload input = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["response_format"], "opus"; got != want {
		t.Fatalf("payload response_format = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["stream"], true; got != want {
		t.Fatalf("payload stream = %#v, want %#v", got, want)
	}
	if _, ok := gotPayload["ref_audio"]; ok {
		t.Fatalf("payload ref_audio present with voice request: %#v", gotPayload)
	}
}

func TestDefaultConfigFromEnvAcceptsLMNTTTSFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_TTS_FALLBACK_PROVIDERS", "lmnt")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("LMNT_API_KEY", "test-lmnt-key")
	t.Setenv("RTP_AGENT_TTS_MODEL", "aurora")
	t.Setenv("RTP_AGENT_TTS_VOICE", "ava")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "wav")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.TTS.Label(); got != "FallbackAdapter(openai.TTS)" {
		t.Fatalf("TTS label = %q, want fallback adapter around primary openai TTS", got)
	}
}

func TestLMNTTTSFallbackPassesReferenceOptions(t *testing.T) {
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]any
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/wav"}},
			Body:       io.NopCloser(strings.NewReader("audio")),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	sampleRate := 16000
	temperature := 0.4
	topP := 0.6
	provider, err := fallbackTTSFromProvider(AppConfig{
		LMNTAPIKey:        "test-lmnt-key",
		TTSModel:          "aurora",
		TTSVoice:          "ava",
		TTSLanguage:       "en",
		TTSResponseFormat: "wav",
		TTSSampleRate:     &sampleRate,
		TTSTemperature:    &temperature,
		TTSTopP:           &topP,
	}, providerLMNT)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*lmnt.LMNTTTS); !ok {
		t.Fatalf("provider type = %T, want *lmnt.LMNTTTS", provider)
	}
	if got, want := provider.Label(), "lmnt.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 16000; got != want {
		t.Fatalf("SampleRate() = %d, want reference configured sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "aurora"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "LMNT"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference non-streaming without aligned transcript", caps)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotURL, "https://api.lmnt.com/v1/ai/speech/bytes"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("X-API-Key"), "test-lmnt-key"; got != want {
		t.Fatalf("X-API-Key = %q, want %q", got, want)
	}
	if got, want := gotPayload["text"], "hello"; got != want {
		t.Fatalf("payload text = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["voice"], "ava"; got != want {
		t.Fatalf("payload voice = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["language"], "en"; got != want {
		t.Fatalf("payload language = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["sample_rate"], float64(16000); got != want {
		t.Fatalf("payload sample_rate = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["model"], "aurora"; got != want {
		t.Fatalf("payload model = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["format"], "wav"; got != want {
		t.Fatalf("payload format = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["temperature"], 0.4; got != want {
		t.Fatalf("payload temperature = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["top_p"], 0.6; got != want {
		t.Fatalf("payload top_p = %#v, want %#v", got, want)
	}
}

func TestDefaultConfigFromEnvAcceptsNeuphonicTTSFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_TTS_FALLBACK_PROVIDERS", "neuphonic")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("NEUPHONIC_API_KEY", "test-neuphonic-key")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://neuphonic.example")
	t.Setenv("RTP_AGENT_TTS_VOICE", "voice-2")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "es")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "pcm_mulaw")
	t.Setenv("RTP_AGENT_TTS_SPEED", "0.75")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.TTS.Label(); got != "FallbackAdapter(openai.TTS)" {
		t.Fatalf("TTS label = %q, want fallback adapter around primary openai TTS", got)
	}
}

func TestNeuphonicTTSFallbackPassesReferenceOptions(t *testing.T) {
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]any
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(strings.Join([]string{
				`event: message`,
				`data: {"status_code":200,"data":{"audio":"AQI="}}`,
				"",
			}, "\n"))),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	sampleRate := 16000
	provider, err := fallbackTTSFromProvider(AppConfig{
		NeuphonicAPIKey: "test-neuphonic-key",
		TTSBaseURL:      "https://neuphonic.example/",
		TTSVoice:        "voice-2",
		TTSLanguage:     "es",
		TTSEncoding:     "pcm_mulaw",
		TTSSampleRate:   &sampleRate,
		TTSSpeed:        0.75,
	}, providerNeuphonic)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*neuphonic.NeuphonicTTS); !ok {
		t.Fatalf("provider type = %T, want *neuphonic.NeuphonicTTS", provider)
	}
	if got, want := provider.Label(), "neuphonic.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 16000; got != want {
		t.Fatalf("SampleRate() = %d, want reference configured sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "Octave"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "Neuphonic"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference streaming without aligned transcript", caps)
	}

	stream, err := provider.Synthesize(context.Background(), "hola")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotURL, "https://neuphonic.example/sse/speak/es"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("x-api-key"), "test-neuphonic-key"; got != want {
		t.Fatalf("x-api-key = %q, want %q", got, want)
	}
	if got, want := gotPayload["text"], "hola"; got != want {
		t.Fatalf("payload text = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["voice_id"], "voice-2"; got != want {
		t.Fatalf("payload voice_id = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["lang_code"], "es"; got != want {
		t.Fatalf("payload lang_code = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["encoding"], "pcm_mulaw"; got != want {
		t.Fatalf("payload encoding = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["sampling_rate"], float64(16000); got != want {
		t.Fatalf("payload sampling_rate = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["speed"], 0.75; got != want {
		t.Fatalf("payload speed = %#v, want %#v", got, want)
	}
}

func TestDefaultConfigFromEnvAcceptsRimeTTSFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_TTS_FALLBACK_PROVIDERS", "rime")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("RIME_API_KEY", "test-rime-key")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://rime.example/v1/rime-tts")
	t.Setenv("RTP_AGENT_TTS_MODEL", "coda")
	t.Setenv("RTP_AGENT_TTS_VOICE", "lyra")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "spa")
	t.Setenv("RTP_AGENT_TTS_SPEED", "1.1")
	t.Setenv("RTP_AGENT_TTS_DELIVERY_MODE", "immediate")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.TTS.Label(); got != "FallbackAdapter(openai.TTS)" {
		t.Fatalf("TTS label = %q, want fallback adapter around primary openai TTS", got)
	}
}

func TestRimeTTSFallbackPassesReferenceOptions(t *testing.T) {
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]any
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(strings.NewReader("audio")),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	sampleRate := 24000
	provider, err := fallbackTTSFromProvider(AppConfig{
		RimeAPIKey:    "test-rime-key",
		TTSBaseURL:    "https://rime.example/v1/rime-tts",
		TTSModel:      "coda",
		TTSVoice:      "lyra",
		TTSLanguage:   "spa",
		TTSSampleRate: &sampleRate,
		TTSSpeed:      1.1,
	}, providerRime)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*rime.RimeTTS); !ok {
		t.Fatalf("provider type = %T, want *rime.RimeTTS", provider)
	}
	if got, want := provider.Label(), "rime.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 24000; got != want {
		t.Fatalf("SampleRate() = %d, want reference configured sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "coda"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "Rime"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference HTTP mode without streaming", caps)
	}

	stream, err := provider.Synthesize(context.Background(), "hola")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotURL, "https://rime.example/v1/rime-tts"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("Authorization"), "Bearer test-rime-key"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("Accept"), "audio/pcm"; got != want {
		t.Fatalf("Accept = %q, want %q", got, want)
	}
	if got, want := gotPayload["speaker"], "lyra"; got != want {
		t.Fatalf("payload speaker = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["text"], "hola"; got != want {
		t.Fatalf("payload text = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["modelId"], "coda"; got != want {
		t.Fatalf("payload modelId = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["lang"], "spa"; got != want {
		t.Fatalf("payload lang = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["samplingRate"], float64(24000); got != want {
		t.Fatalf("payload samplingRate = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["timeScaleFactor"], 1.1; got != want {
		t.Fatalf("payload timeScaleFactor = %#v, want %#v", got, want)
	}
	if _, ok := gotPayload["audioFormat"]; ok {
		t.Fatalf("payload audioFormat = %#v, want omitted for HTTP reference payload", gotPayload["audioFormat"])
	}
}

func TestDefaultConfigFromEnvAcceptsMurfTTSFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_TTS_FALLBACK_PROVIDERS", "murf")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("MURF_API_KEY", "test-murf-key")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://murf.example")
	t.Setenv("RTP_AGENT_TTS_MODEL", "GEN2")
	t.Setenv("RTP_AGENT_TTS_VOICE", "en-US-natalie")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en-US")
	t.Setenv("RTP_AGENT_TTS_INSTRUCTIONS", "Promo")
	t.Setenv("RTP_AGENT_TTS_SPEED", "12")
	t.Setenv("RTP_AGENT_TTS_PITCH", "-4")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.TTS.Label(); got != "FallbackAdapter(openai.TTS)" {
		t.Fatalf("TTS label = %q, want fallback adapter around primary openai TTS", got)
	}
}

func TestMurfTTSFallbackPassesReferenceOptions(t *testing.T) {
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]any
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(strings.NewReader("audio")),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	sampleRate := 44100
	pitch := -4
	provider, err := fallbackTTSFromProvider(AppConfig{
		MurfAPIKey:      "test-murf-key",
		TTSBaseURL:      "https://murf.example/",
		TTSModel:        "GEN2",
		TTSVoice:        "en-US-natalie",
		TTSLanguage:     "en-US",
		TTSInstructions: "Promo",
		TTSSpeed:        12,
		TTSPitch:        &pitch,
		TTSSampleRate:   &sampleRate,
		TTSEncoding:     "mp3",
	}, providerMurf)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*murf.MurfTTS); !ok {
		t.Fatalf("provider type = %T, want *murf.MurfTTS", provider)
	}
	if got, want := provider.Label(), "murf.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 44100; got != want {
		t.Fatalf("SampleRate() = %d, want reference configured sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "GEN2"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "Murf"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference streaming without aligned transcript", caps)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotURL, "https://murf.example/v1/speech/stream"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("api-key"), "test-murf-key"; got != want {
		t.Fatalf("api-key = %q, want %q", got, want)
	}
	if got, want := gotPayload["text"], "hello"; got != want {
		t.Fatalf("payload text = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["model"], "GEN2"; got != want {
		t.Fatalf("payload model = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["multiNativeLocale"], "en-US"; got != want {
		t.Fatalf("payload multiNativeLocale = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["voice_id"], "en-US-natalie"; got != want {
		t.Fatalf("payload voice_id = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["style"], "Promo"; got != want {
		t.Fatalf("payload style = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["rate"], float64(12); got != want {
		t.Fatalf("payload rate = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["pitch"], float64(-4); got != want {
		t.Fatalf("payload pitch = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["format"], "mp3"; got != want {
		t.Fatalf("payload format = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["sample_rate"], float64(44100); got != want {
		t.Fatalf("payload sample_rate = %#v, want %#v", got, want)
	}
}

func TestDefaultConfigFromEnvAcceptsSpeechifyTTSFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_TTS_FALLBACK_PROVIDERS", "speechify")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("SPEECHIFY_API_KEY", "test-speechify-key")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://speechify.example/v1")
	t.Setenv("RTP_AGENT_TTS_MODEL", "simba-english")
	t.Setenv("RTP_AGENT_TTS_VOICE", "cliff")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en-US")
	t.Setenv("RTP_AGENT_TTS_ENCODING", "mp3_24000")
	t.Setenv("RTP_AGENT_TTS_LOUDNESS_NORMALIZATION", "true")
	t.Setenv("RTP_AGENT_TTS_TEXT_NORMALIZATION", "false")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.TTS.Label(); got != "FallbackAdapter(openai.TTS)" {
		t.Fatalf("TTS label = %q, want fallback adapter around primary openai TTS", got)
	}
}

func TestSpeechifyTTSFallbackPassesReferenceOptions(t *testing.T) {
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]any
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/mpeg"}},
			Body:       io.NopCloser(strings.NewReader("audio")),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	loudnessNormalization := true
	textNormalization := false
	provider, err := fallbackTTSFromProvider(AppConfig{
		SpeechifyAPIKey:          "test-speechify-key",
		TTSBaseURL:               "https://speechify.example/v1/",
		TTSModel:                 "simba-english",
		TTSVoice:                 "cliff",
		TTSLanguage:              "en-US",
		TTSEncoding:              "mp3_24000",
		TTSLoudnessNormalization: &loudnessNormalization,
		TTSTextNormalization:     &textNormalization,
	}, providerSpeechify)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*speechify.SpeechifyTTS); !ok {
		t.Fatalf("provider type = %T, want *speechify.SpeechifyTTS", provider)
	}
	if got, want := provider.Label(), "speechify.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 24000; got != want {
		t.Fatalf("SampleRate() = %d, want reference encoding-derived sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "simba-english"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "Speechify"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference non-streaming without aligned transcript", caps)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotURL, "https://speechify.example/v1/audio/stream"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("Authorization"), "Bearer test-speechify-key"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("Accept"), "audio/mpeg"; got != want {
		t.Fatalf("Accept = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("x-caller"), "livekit"; got != want {
		t.Fatalf("x-caller = %q, want %q", got, want)
	}
	if got, want := gotPayload["input"], "hello"; got != want {
		t.Fatalf("payload input = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["voice_id"], "cliff"; got != want {
		t.Fatalf("payload voice_id = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["language"], "en-US"; got != want {
		t.Fatalf("payload language = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["model"], "simba-english"; got != want {
		t.Fatalf("payload model = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["audio_format"], "mp3"; got != want {
		t.Fatalf("payload audio_format = %#v, want %#v", got, want)
	}
	options, ok := gotPayload["options"].(map[string]any)
	if !ok {
		t.Fatalf("payload options = %#v, want object", gotPayload["options"])
	}
	if got, want := options["loudness_normalization"], true; got != want {
		t.Fatalf("payload options.loudness_normalization = %#v, want %#v", got, want)
	}
	if got, want := options["text_normalization"], false; got != want {
		t.Fatalf("payload options.text_normalization = %#v, want %#v", got, want)
	}
}

func TestDefaultConfigFromEnvAcceptsSimplismartTTSFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_TTS_FALLBACK_PROVIDERS", "simplismart")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("SIMPLISMART_API_KEY", "test-simplismart-key")
	t.Setenv("RTP_AGENT_TTS_VOICE", "voice-1")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.TTS.Label(); got != "FallbackAdapter(openai.TTS)" {
		t.Fatalf("TTS label = %q, want fallback adapter around primary openai TTS", got)
	}
}

func TestSimplismartTTSFallbackPassesReferenceOptions(t *testing.T) {
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]any
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/pcm"}},
			Body:       io.NopCloser(strings.NewReader("audio")),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	sampleRate := 16000
	temperature := 0.4
	topP := 0.6
	maxTokens := 256
	provider, err := fallbackTTSFromProvider(AppConfig{
		SimplismartAPIKey: "test-simplismart-key",
		TTSBaseURL:        "https://simplismart.example/tts",
		TTSModel:          "canopylabs/orpheus-3b-test",
		TTSVoice:          "leo",
		TTSSampleRate:     &sampleRate,
		TTSTemperature:    &temperature,
		TTSTopP:           &topP,
		TTSMaxTokens:      &maxTokens,
	}, providerSimplismart)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	if _, ok := provider.(*simplismart.SimplismartTTS); !ok {
		t.Fatalf("provider type = %T, want *simplismart.SimplismartTTS", provider)
	}
	if got, want := provider.Label(), "simplismart.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 16000; got != want {
		t.Fatalf("SampleRate() = %d, want reference configured sample rate %d", got, want)
	}
	if got, want := tts.Model(provider), "canopylabs/orpheus-3b-test"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "SimpliSmart"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if caps := provider.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference non-streaming without aligned transcript", caps)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotURL, "https://simplismart.example/tts"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("Authorization"), "Bearer test-simplismart-key"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := gotPayload["prompt"], "hello"; got != want {
		t.Fatalf("payload prompt = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["voice"], "leo"; got != want {
		t.Fatalf("payload voice = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["model"], "canopylabs/orpheus-3b-test"; got != want {
		t.Fatalf("payload model = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["temperature"], 0.4; got != want {
		t.Fatalf("payload temperature = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["top_p"], 0.6; got != want {
		t.Fatalf("payload top_p = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["max_tokens"], float64(256); got != want {
		t.Fatalf("payload max_tokens = %#v, want %#v", got, want)
	}
}

func TestSimplismartTTSFallbackPassesQwenReferenceOptions(t *testing.T) {
	var gotURL string
	var gotHeaders http.Header
	var gotPayload map[string]any
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: appMCPHTTPRoundTripper(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		if err := json.NewDecoder(req.Body).Decode(&gotPayload); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"audio/L16"}},
			Body:       io.NopCloser(strings.NewReader("audio")),
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = originalClient })

	provider, err := fallbackTTSFromProvider(AppConfig{
		SimplismartAPIKey: "test-simplismart-key",
		TTSModel:          "qwen-tts",
		TTSLanguage:       "Indonesian",
		TTSModelOptions: map[string]any{
			"leading_silence": false,
		},
	}, providerSimplismart)
	if err != nil {
		t.Fatalf("fallbackTTSFromProvider() error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "halo")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream.Close() error = %v", err)
	}

	if got, want := gotURL, "https://api.simplismart.live/v1/audio/speech"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("Accept"), "audio/L16"; got != want {
		t.Fatalf("Accept = %q, want %q", got, want)
	}
	if got, want := gotPayload["model"], "qwen-tts"; got != want {
		t.Fatalf("payload model = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["text"], "halo"; got != want {
		t.Fatalf("payload text = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["language"], "Indonesian"; got != want {
		t.Fatalf("payload language = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["voice"], "Chelsie"; got != want {
		t.Fatalf("payload voice = %#v, want %#v", got, want)
	}
	if got, want := gotPayload["leading_silence"], false; got != want {
		t.Fatalf("payload leading_silence = %#v, want %#v", got, want)
	}
	if _, ok := gotPayload["prompt"]; ok {
		t.Fatalf("payload prompt = %#v, want omitted for Qwen reference payload", gotPayload["prompt"])
	}
}

func TestDefaultConfigFromEnvAcceptsUltravoxTTSFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_TTS_FALLBACK_PROVIDERS", "ultravox")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("ULTRAVOX_API_KEY", "test-ultravox-key")
	t.Setenv("RTP_AGENT_TTS_VOICE", "Mark")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.TTS.Label(); got != "FallbackAdapter(openai.TTS)" {
		t.Fatalf("TTS label = %q, want fallback adapter around primary openai TTS", got)
	}
}

func TestDefaultConfigFromEnvAcceptsUpliftAITTSFallbackProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_TTS_FALLBACK_PROVIDERS", "upliftai")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("UPLIFTAI_API_KEY", "test-upliftai-key")
	t.Setenv("RTP_AGENT_TTS_VOICE", "v_meklc281")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Session.TTS.Label(); got != "FallbackAdapter(openai.TTS)" {
		t.Fatalf("TTS label = %q, want fallback adapter around primary openai TTS", got)
	}
}

func TestEvaluateSessionReturnsEvaluationSummary(t *testing.T) {
	baseAgent := agent.NewAgent("test")
	session := agent.NewAgentSession(baseAgent, nil, agent.AgentSessionOptions{})
	session.ChatCtx.Append(&llm.ChatMessage{
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "hello"}},
	})
	jobCtx := worker.NewJobContext(&livekit.Job{Id: "job_eval"}, "", "", "")
	session.SetJobContext(jobCtx)
	evaluatorLLM := &fakeEvalLLM{
		stream: &fakeEvalLLMStream{chunks: []*llm.ChatChunk{{
			Delta: &llm.ChoiceDelta{ToolCalls: []llm.FunctionToolCall{{
				Name:      "submit_verdict",
				Arguments: `{"verdict":"pass","reasoning":"met the criteria"}`,
			}}},
		}}},
	}
	app := &App{
		Session:   session,
		Evaluator: evals.NewJudgeGroup(evaluatorLLM, []evals.Evaluator{evals.AccuracyJudge(evaluatorLLM)}),
	}

	summary, err := app.EvaluateSession(context.Background(), nil)
	if err != nil {
		t.Fatalf("EvaluateSession() error = %v", err)
	}
	if summary.Score != 1 || !summary.AllPassed || !summary.AnyPassed || !summary.MajorityPassed || !summary.NoneFailed {
		t.Fatalf("summary = %+v, want passing evaluation summary", summary)
	}
	evaluations := jobCtx.Tagger().Evaluations()
	if len(evaluations) != 1 {
		t.Fatalf("job context evaluations = %#v, want one auto-tagged evaluation", evaluations)
	}
	if evaluations[0]["name"] != "accuracy" || evaluations[0]["verdict"] != "pass" {
		t.Fatalf("job context evaluation = %#v, want accuracy pass", evaluations[0])
	}
	if evaluations[0]["tag"] != "lk.judge.accuracy:pass" {
		t.Fatalf("job context evaluation tag = %#v, want generated judge tag", evaluations[0]["tag"])
	}
	if evaluations[0]["reasoning"] != "met the criteria" {
		t.Fatalf("job context evaluation reasoning = %#v, want LLM reasoning", evaluations[0]["reasoning"])
	}
	instructions, ok := evaluations[0]["instructions"].(string)
	if !ok || !strings.Contains(instructions, "All information provided by the agent must be accurate and grounded") {
		t.Fatalf("job context evaluation instructions = %#v, want accuracy judge instructions", evaluations[0]["instructions"])
	}
}

func TestRunSessionRegistersPrimarySessionOnJobContext(t *testing.T) {
	baseAgent := agent.NewAgent("test")
	session := agent.NewAgentSession(baseAgent, nil, agent.AgentSessionOptions{})
	server := worker.NewAgentServer(worker.WorkerOptions{AgentName: "support-agent"})
	application := &App{
		Server:          server,
		Agent:           baseAgent,
		Session:         session,
		MetricsRegistry: telemetry.NewMetricRegistry(),
	}
	ctx := worker.NewJobContext(
		&livekit.Job{
			Id: "job_primary_session",
			Room: &livekit.Room{
				Sid:  "RM_primary",
				Name: "room-primary",
			},
		},
		"wss://livekit.example",
		"key",
		"secret",
	)

	if err := application.runSession(ctx); err != nil {
		t.Fatalf("runSession() error = %v", err)
	}
	primary, err := ctx.PrimarySession()
	if err != nil {
		t.Fatalf("PrimarySession() error = %v", err)
	}
	if primary != session {
		t.Fatal("PrimarySession() did not return app session")
	}
}

func TestConfigureRoomToolsAddsSendDTMFTool(t *testing.T) {
	baseAgent := agent.NewAgent("test")
	publisher := &fakeAppDtmfPublisher{}

	err := configureRoomTools(AppConfig{AppTools: []string{"send_dtmf"}}, baseAgent, publisher)
	if err != nil {
		t.Fatalf("configureRoomTools() error = %v", err)
	}
	if len(baseAgent.Tools) != 1 {
		t.Fatalf("len(Agent.Tools) = %d, want 1", len(baseAgent.Tools))
	}
	if got := baseAgent.Tools[0].Name(); got != "send_dtmf_events" {
		t.Fatalf("tool[0].Name() = %q, want send_dtmf_events", got)
	}
}

func TestConfigureRoomToolsAddsSendDTMFToolForIVRDetection(t *testing.T) {
	baseAgent := agent.NewAgent("test")
	publisher := &fakeAppDtmfPublisher{}

	err := configureRoomTools(AppConfig{IVRDetection: true}, baseAgent, publisher)
	if err != nil {
		t.Fatalf("configureRoomTools() error = %v", err)
	}
	if len(baseAgent.Tools) != 1 {
		t.Fatalf("len(Agent.Tools) = %d, want 1", len(baseAgent.Tools))
	}
	if got := baseAgent.Tools[0].Name(); got != "send_dtmf_events" {
		t.Fatalf("tool[0].Name() = %q, want send_dtmf_events", got)
	}
}

func TestDefaultConfigFromEnvAddsAnthropicComputerTool(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-anthropic-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "anthropic")
	t.Setenv("RTP_AGENT_ANTHROPIC_TOOLS", "computer")
	t.Setenv("RTP_AGENT_ANTHROPIC_COMPUTER_WIDTH", "1280")
	t.Setenv("RTP_AGENT_ANTHROPIC_COMPUTER_HEIGHT", "720")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Agent == nil {
		t.Fatal("Agent is nil")
	}
	if len(app.Agent.Tools) != 1 {
		t.Fatalf("len(Agent.Tools) = %d, want 1", len(app.Agent.Tools))
	}
	tool := app.Agent.Tools[0]
	if tool.ID() != "computer" || tool.Name() != "computer_use" {
		t.Fatalf("tool identity = %q/%q, want computer/computer_use", tool.ID(), tool.Name())
	}
	if specProvider, ok := tool.(interface {
		AnthropicToolSpec() map[string]interface{}
	}); ok {
		spec := specProvider.AnthropicToolSpec()
		if spec["display_width_px"] != 1280 || spec["display_height_px"] != 720 {
			t.Fatalf("computer display spec = %#v, want 1280x720", spec)
		}
	} else {
		t.Fatal("computer tool does not expose AnthropicToolSpec")
	}
}

func TestDefaultConfigFromEnvSelectsAvatarProvider(t *testing.T) {
	cases := []struct {
		name       string
		provider   string
		keyEnv     string
		wantAvatar string
	}{
		{name: "anam", provider: "anam", keyEnv: "ANAM_API_KEY", wantAvatar: "*anam.AnamAvatar"},
		{name: "avatario", provider: "avatario", keyEnv: "AVATARIO_API_KEY", wantAvatar: "*avatario.AvatarioAvatar"},
		{name: "avatartalk", provider: "avatartalk", keyEnv: "AVATARTALK_API_KEY", wantAvatar: "*avatartalk.AvatartalkAvatar"},
		{name: "bey", provider: "bey", keyEnv: "BEY_API_KEY", wantAvatar: "*bey.BeyAvatar"},
		{name: "bithuman", provider: "bithuman", keyEnv: "BITHUMAN_API_KEY", wantAvatar: "*bithuman.BithumanAvatar"},
		{name: "did", provider: "did", keyEnv: "DID_API_KEY", wantAvatar: "*did.DIDAvatar"},
		{name: "hedra", provider: "hedra", keyEnv: "HEDRA_API_KEY", wantAvatar: "*hedra.HedraAvatar"},
		{name: "keyframe", provider: "keyframe", keyEnv: "KEYFRAME_API_KEY", wantAvatar: "*keyframe.KeyframeAgent"},
		{name: "lemonslice", provider: "lemonslice", keyEnv: "LEMONSLICE_API_KEY", wantAvatar: "*lemonslice.LemonsliceAvatar"},
		{name: "liveavatar", provider: "liveavatar", keyEnv: "LIVEAVATAR_API_KEY", wantAvatar: "*liveavatar.LiveAvatar"},
		{name: "simli", provider: "simli", keyEnv: "SIMLI_API_KEY", wantAvatar: "*simli.SimliAvatar"},
		{name: "tavus", provider: "tavus", keyEnv: "TAVUS_API_KEY", wantAvatar: "*tavus.TavusAvatar"},
		{name: "trugen", provider: "trugen", keyEnv: "TRUGEN_API_KEY", wantAvatar: "*trugen.TrugenAvatar"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.keyEnv, "test-avatar-key")
			t.Setenv("RTP_AGENT_AVATAR_PROVIDER", tc.provider)

			app, err := NewApp(DefaultConfigFromEnv())
			if err != nil {
				t.Fatalf("NewApp() error = %v", err)
			}
			if app.Agent == nil {
				t.Fatal("Agent is nil")
			}
			if app.Agent.Avatar == nil {
				t.Fatal("Agent Avatar is nil")
			}
			if got := fmt.Sprintf("%T", app.Agent.Avatar); got != tc.wantAvatar {
				t.Fatalf("Agent Avatar type = %q, want %s", got, tc.wantAvatar)
			}
		})
	}
}

func TestDefaultConfigFromEnvSelectsRunwayAvatarProvider(t *testing.T) {
	t.Setenv("RTP_AGENT_AVATAR_PROVIDER", "runway")
	t.Setenv("RUNWAYML_API_SECRET", "runway-secret")
	t.Setenv("RTP_AGENT_RUNWAY_AVATAR_ID", "avatar-123")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Agent == nil {
		t.Fatal("Agent is nil")
	}
	if got := fmt.Sprintf("%T", app.Agent.Avatar); got != "*runway.RunwayAvatar" {
		t.Fatalf("Agent Avatar type = %q, want *runway.RunwayAvatar", got)
	}
}

func TestDefaultConfigFromEnvSelectsAWSProviders(t *testing.T) {
	t.Setenv("AWS_REGION", "us-west-2")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "aws")
	t.Setenv("RTP_AGENT_LLM_MODEL", "amazon.nova-test")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "aws")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en-US")
	t.Setenv("RTP_AGENT_STT_SPEAKER_LABELS", "true")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "aws")
	t.Setenv("RTP_AGENT_TTS_VOICE", "Joanna")
	t.Setenv("RTP_AGENT_TTS_MODEL", "standard")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en-US")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "22050")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := llm.Provider(app.Session.LLM); got != "AWS Bedrock" {
		t.Fatalf("LLM provider = %q, want AWS Bedrock", got)
	}
	if got := app.Session.STT.Label(); got != "aws.STT" {
		t.Fatalf("STT label = %q, want aws.STT", got)
	}
	if got := app.Session.TTS.Label(); got != "aws.TTS" {
		t.Fatalf("TTS label = %q, want aws.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 22050 {
		t.Fatalf("TTS sample rate = %d, want 22050", got)
	}
}

func TestDefaultConfigFromEnvSelectsAzureSpeechProviders(t *testing.T) {
	t.Setenv("AZURE_SPEECH_KEY", "test-azure-key")
	t.Setenv("AZURE_SPEECH_REGION", "eastus")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "azure")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "azure")
	t.Setenv("RTP_AGENT_TTS_VOICE", "en-US-AvaNeural")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "id-ID")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := app.Session.STT.Label(); got != "azure.STT" {
		t.Fatalf("STT label = %q, want azure.STT", got)
	}
	if got := app.Session.TTS.Label(); got != "StreamAdapter(azure.TTS)" {
		t.Fatalf("TTS label = %q, want Azure TTS wrapped by core stream adapter", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
	if got := tts.Model(app.Session.TTS); got != "unknown" {
		t.Fatalf("TTS model = %q, want StreamAdapter to forward Azure model metadata", got)
	}
	if got := tts.Provider(app.Session.TTS); got != "Azure TTS" {
		t.Fatalf("TTS provider = %q, want StreamAdapter to forward Azure provider metadata", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || !caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want core stream adapter capabilities", caps)
	}
	stream, err := app.Session.TTS.Stream(context.Background())
	if err != nil {
		t.Fatalf("TTS Stream() error = %v, want core stream adapter", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("TTS stream Close() error = %v", err)
	}
}

func TestDefaultConfigFromEnvMapsAzureSTTLanguageAndEndpoint(t *testing.T) {
	t.Setenv("RTP_AGENT_STT_PROVIDER", "azure")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "id-ID")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://southindia.api.cognitive.microsoft.com/")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "8000")
	t.Setenv("AZURE_SPEECH_KEY", "test-azure-key")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	azureProvider, ok := app.Session.STT.(*azure.AzureSTT)
	if !ok {
		t.Fatalf("STT provider = %T, want *azure.AzureSTT", app.Session.STT)
	}
	state := reflect.ValueOf(azureProvider).Elem()
	if got, want := state.FieldByName("language").String(), "id-ID"; got != want {
		t.Fatalf("Azure STT language = %q, want %q", got, want)
	}
	if got, want := state.FieldByName("speechHost").String(), "https://southindia.api.cognitive.microsoft.com/"; got != want {
		t.Fatalf("Azure STT speechHost = %q, want %q", got, want)
	}
	if got, want := stt.InputSampleRate(azureProvider), uint32(8000); got != want {
		t.Fatalf("Azure STT input sample rate = %d, want %d", got, want)
	}
}

func TestNewAppMapsAzureSTTBundleSettingEndpoint(t *testing.T) {
	t.Setenv("AZURE_SPEECH_KEY", "test-azure-key")
	cfg := AppConfig{
		STTProvider: "azure",
		STTLanguage: "id-ID",
		STTModelOptions: map[string]any{
			"setting": map[string]any{
				"azure_endpoint": "https://southindia.api.cognitive.microsoft.com/",
				"sample_rate":    "8000",
			},
		},
	}

	app, err := NewApp(cfg)
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	azureProvider, ok := app.Session.STT.(*azure.AzureSTT)
	if !ok {
		t.Fatalf("STT provider = %T, want *azure.AzureSTT", app.Session.STT)
	}
	state := reflect.ValueOf(azureProvider).Elem()
	if got, want := state.FieldByName("language").String(), "id-ID"; got != want {
		t.Fatalf("Azure STT language = %q, want %q", got, want)
	}
	if got, want := state.FieldByName("speechHost").String(), "https://southindia.api.cognitive.microsoft.com/"; got != want {
		t.Fatalf("Azure STT speechHost = %q, want %q", got, want)
	}
	if got, want := stt.InputSampleRate(azureProvider), uint32(8000); got != want {
		t.Fatalf("Azure STT input sample rate = %d, want %d", got, want)
	}
}

func TestDefaultConfigFromEnvRejectsAzureSTTUnsupportedOpenAIDeploymentConfig(t *testing.T) {
	t.Setenv("RTP_AGENT_STT_PROVIDER", "azure")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "id-ID")
	t.Setenv("RTP_AGENT_STT_MODEL", "telephony")
	t.Setenv("RTP_AGENT_STT_MODEL_OPTIONS", "azure_deployment=whisper,api_version=2024-06-01")
	t.Setenv("AZURE_SPEECH_KEY", "test-azure-key")
	t.Setenv("AZURE_SPEECH_REGION", "southeastasia")

	_, err := NewApp(DefaultConfigFromEnv())
	if err == nil {
		t.Fatal("NewApp() error = nil, want unsupported Azure STT deployment config error")
	}
	if !strings.Contains(err.Error(), "azure_deployment") || !strings.Contains(err.Error(), "RTP_AGENT_STT_PROVIDER=azure") {
		t.Fatalf("NewApp() error = %v, want Azure STT unsupported deployment config context", err)
	}
}

func TestNewAppRejectsAzureSTTUnsupportedBundleDeploymentConfig(t *testing.T) {
	t.Setenv("AZURE_SPEECH_KEY", "test-azure-key")
	cfg := AppConfig{
		STTProvider: "azure",
		STTLanguage: "id-ID",
		STTModelOptions: map[string]any{
			"setting": map[string]any{
				"azure_deployment": "whisper",
				"api_version":      "2024-06-01",
			},
		},
	}

	_, err := NewApp(cfg)
	if err == nil {
		t.Fatal("NewApp() error = nil, want unsupported Azure STT deployment config error")
	}
	if !strings.Contains(err.Error(), "azure_deployment") || !strings.Contains(err.Error(), "RTP_AGENT_STT_PROVIDER=azure") {
		t.Fatalf("NewApp() error = %v, want Azure STT unsupported deployment config context", err)
	}
}

func TestDefaultConfigFromEnvSelectsBasetenProviders(t *testing.T) {
	t.Setenv("BASETEN_API_KEY", "test-baseten-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "baseten")
	t.Setenv("RTP_AGENT_LLM_MODEL", "llama-test")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "baseten")
	t.Setenv("RTP_AGENT_STT_MODEL", "stt-test")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_STT_ENCODING", "pcm_s16le")
	t.Setenv("RTP_AGENT_STT_SAMPLE_RATE", "16000")
	t.Setenv("RTP_AGENT_STT_BUFFER_SIZE_SECONDS", "0.064")
	t.Setenv("RTP_AGENT_STT_VAD_THRESHOLD", "0.7")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "baseten")
	t.Setenv("RTP_AGENT_TTS_MODEL", "tts-test")
	t.Setenv("RTP_AGENT_TTS_VOICE", "tara")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "en")
	t.Setenv("RTP_AGENT_TTS_TEMPERATURE", "0.6")
	t.Setenv("RTP_AGENT_TTS_MAX_TOKENS", "2000")
	t.Setenv("RTP_AGENT_TTS_BUFFER_SIZE", "10")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := llm.Provider(app.Session.LLM); got != "Baseten" {
		t.Fatalf("LLM provider = %q, want Baseten", got)
	}
	if got := app.Session.STT.Label(); got != "baseten.STT" {
		t.Fatalf("STT label = %q, want baseten.STT", got)
	}
	if got := app.Session.TTS.Label(); got != "baseten.TTS" {
		t.Fatalf("TTS label = %q, want baseten.TTS", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 24000 {
		t.Fatalf("TTS sample rate = %d, want 24000", got)
	}
}

func TestDefaultConfigFromEnvSelectsGoogleLLM(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "test-google-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "google")
	t.Setenv("RTP_AGENT_LLM_MODEL", "gemini-test")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Provider(app.Session.LLM); got != "google" {
		t.Fatalf("LLM provider = %q, want google", got)
	}
	if got := llm.Model(app.Session.LLM); got != "gemini-test" {
		t.Fatalf("LLM model = %q, want gemini-test", got)
	}
}

func TestDefaultConfigFromEnvSelectsGoogleTTS(t *testing.T) {
	original := appNewGoogleTTS
	defer func() { appNewGoogleTTS = original }()

	var credentialsFile string
	var googleCfg appGoogleTTSConfig
	appNewGoogleTTS = func(credentials string, cfg appGoogleTTSConfig) (tts.TTS, error) {
		credentialsFile = credentials
		googleCfg = cfg
		return &fakeAppTTS{}, nil
	}

	t.Setenv("RTP_AGENT_TTS_PROVIDER", "google")
	t.Setenv("RTP_AGENT_GOOGLE_CREDENTIALS_FILE", "/tmp/google-credentials.json")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "id-ID")
	t.Setenv("RTP_AGENT_TTS_VOICE", "id-ID-Standard-A")
	t.Setenv("RTP_AGENT_TTS_MODEL", "gemini-custom")
	t.Setenv("RTP_AGENT_TTS_INSTRUCTIONS", "speak warmly")
	t.Setenv("RTP_AGENT_TTS_SPEAKING_RATE", "1.25")
	t.Setenv("RTP_AGENT_TTS_PITCH", "3")
	t.Setenv("RTP_AGENT_TTS_MODEL_OPTIONS", "effects_profile_id=telephony-class-application,volume_gain_db=-2.5")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if credentialsFile != "/tmp/google-credentials.json" {
		t.Fatalf("credentials file = %q, want /tmp/google-credentials.json", credentialsFile)
	}
	if googleCfg.language != "id-ID" || googleCfg.voice != "id-ID-Standard-A" || googleCfg.model != "gemini-custom" {
		t.Fatalf("google cfg voice = %+v, want configured language, voice, and model", googleCfg)
	}
	if googleCfg.prompt != "speak warmly" {
		t.Fatalf("prompt = %q, want speak warmly", googleCfg.prompt)
	}
	if googleCfg.speakingRate != 1.25 {
		t.Fatalf("speaking rate = %v, want 1.25", googleCfg.speakingRate)
	}
	if googleCfg.pitch != 3 {
		t.Fatalf("pitch = %v, want 3", googleCfg.pitch)
	}
	if googleCfg.effectsProfileID != "telephony-class-application" {
		t.Fatalf("effects profile = %q, want telephony-class-application", googleCfg.effectsProfileID)
	}
	if googleCfg.volumeGainDB != -2.5 {
		t.Fatalf("volume gain = %v, want -2.5", googleCfg.volumeGainDB)
	}
}

func TestDefaultConfigFromEnvSelectsGroqProviders(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "test-groq-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "groq")
	t.Setenv("RTP_AGENT_LLM_MODEL", "llama3-70b-8192")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "groq")
	t.Setenv("RTP_AGENT_TTS_MODEL", "canopylabs/orpheus-v1-english")
	t.Setenv("RTP_AGENT_TTS_VOICE", "autumn")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://groq.example/openai/v1")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if got := llm.Provider(app.Session.LLM); got != "groq" {
		t.Fatalf("LLM provider = %q, want groq", got)
	}
	if got := llm.Model(app.Session.LLM); got != "llama3-70b-8192" {
		t.Fatalf("LLM model = %q, want llama3-70b-8192", got)
	}
	if got := app.Session.TTS.Label(); got != "StreamAdapter(groq.TTS)" {
		t.Fatalf("TTS label = %q, want StreamAdapter(groq.TTS)", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 48000 {
		t.Fatalf("TTS sample rate = %d, want 48000", got)
	}
	if got := tts.Provider(app.Session.TTS); got != "Groq" {
		t.Fatalf("TTS provider = %q, want StreamAdapter to forward Groq provider metadata", got)
	}
	if got := tts.Model(app.Session.TTS); got != "canopylabs/orpheus-v1-english" {
		t.Fatalf("TTS model = %q, want StreamAdapter to forward Groq model metadata", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || !caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want stream adapter capabilities", caps)
	}
}

func TestDefaultConfigFromEnvSelectsCavosSpeechProviders(t *testing.T) {
	t.Setenv("RTP_AGENT_STT_PROVIDER", "cavos")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://steno.example/v1")
	t.Setenv("RTP_AGENT_STT_MODEL", "small")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "id")
	t.Setenv("RTP_AGENT_VAD_PROVIDER", "silero")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "cavos")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://cacatua.example/v1")
	t.Setenv("RTP_AGENT_TTS_MODEL", "supertonic-3")
	t.Setenv("RTP_AGENT_TTS_VOICE", "gisa_300521")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "id")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "pcm")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "44100")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.STT == nil {
		t.Fatal("Session STT is nil")
	}
	if got := app.Session.STT.Label(); got != "StreamAdapter(cavos.STT)" {
		t.Fatalf("STT label = %q, want StreamAdapter(cavos.STT)", got)
	}
	if got := stt.Provider(app.Session.STT); got != "cavos" {
		t.Fatalf("STT provider = %q, want StreamAdapter to forward cavos provider metadata", got)
	}
	if got := stt.Model(app.Session.STT); got != "small" {
		t.Fatalf("STT model = %q, want StreamAdapter to forward small model metadata", got)
	}
	if caps := app.Session.STT.Capabilities(); !caps.Streaming || !caps.OfflineRecognize {
		t.Fatalf("STT capabilities = %+v, want stream adapter capabilities", caps)
	}
	if app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "StreamAdapter(cavos.TTS)" {
		t.Fatalf("TTS label = %q, want StreamAdapter(cavos.TTS)", got)
	}
	if got := tts.Provider(app.Session.TTS); got != "cavos" {
		t.Fatalf("TTS provider = %q, want StreamAdapter to forward cavos provider metadata", got)
	}
	if got := tts.Model(app.Session.TTS); got != "supertonic-3" {
		t.Fatalf("TTS model = %q, want StreamAdapter to forward supertonic-3 model metadata", got)
	}
	if got := app.Session.TTS.SampleRate(); got != 44100 {
		t.Fatalf("TTS sample rate = %d, want 44100", got)
	}
	if caps := app.Session.TTS.Capabilities(); !caps.Streaming || !caps.AlignedTranscript {
		t.Fatalf("TTS capabilities = %+v, want stream adapter capabilities", caps)
	}
}

func TestDefaultConfigFromEnvAcceptsCavosSTTFallbackProvider(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "test-deepgram-key")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "deepgram")
	t.Setenv("RTP_AGENT_STT_FALLBACK_PROVIDERS", "cavos")
	t.Setenv("RTP_AGENT_STT_BASE_URL", "https://steno.example/v1")
	t.Setenv("RTP_AGENT_STT_MODEL", "small")
	t.Setenv("RTP_AGENT_STT_LANGUAGE", "id")
	t.Setenv("RTP_AGENT_VAD_PROVIDER", "silero")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.STT == nil {
		t.Fatal("Session STT is nil")
	}
	if got := app.Session.STT.Label(); got != "FallbackAdapter(deepgram.STT)" {
		t.Fatalf("STT label = %q, want fallback adapter around primary deepgram STT", got)
	}
	if app.Session.VAD == nil {
		t.Fatal("Session VAD is nil")
	}
}

func TestDefaultConfigFromEnvAcceptsCavosTTSFallbackProvider(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_TTS_FALLBACK_PROVIDERS", "cavos")
	t.Setenv("RTP_AGENT_TTS_BASE_URL", "https://cacatua.example/v1")
	t.Setenv("RTP_AGENT_TTS_MODEL", "supertonic-3")
	t.Setenv("RTP_AGENT_TTS_VOICE", "gisa_300521")
	t.Setenv("RTP_AGENT_TTS_LANGUAGE", "id")
	t.Setenv("RTP_AGENT_TTS_RESPONSE_FORMAT", "pcm")
	t.Setenv("RTP_AGENT_TTS_SAMPLE_RATE", "44100")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "FallbackAdapter(openai.TTS)" {
		t.Fatalf("TTS label = %q, want fallback adapter around primary openai TTS", got)
	}
}

func TestDefaultConfigFromEnvSelectsCerebrasLLM(t *testing.T) {
	t.Setenv("CEREBRAS_API_KEY", "test-cerebras-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "cerebras")
	t.Setenv("RTP_AGENT_LLM_MODEL", "llama3.1-test")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Provider(app.Session.LLM); got != "cerebras" {
		t.Fatalf("LLM provider = %q, want cerebras", got)
	}
	if got := llm.Model(app.Session.LLM); got != "llama3.1-test" {
		t.Fatalf("LLM model = %q, want llama3.1-test", got)
	}
}

func TestDefaultConfigFromEnvSelectsLiveKitInferenceLLM(t *testing.T) {
	t.Setenv("LIVEKIT_API_KEY", "test-livekit-key")
	t.Setenv("LIVEKIT_API_SECRET", "test-livekit-secret")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "livekit")
	t.Setenv("RTP_AGENT_LLM_MODEL", "openai/gpt-4.1-mini")
	t.Setenv("RTP_AGENT_STT_PROVIDER", "livekit")
	t.Setenv("RTP_AGENT_STT_MODEL", "deepgram/nova-3")
	t.Setenv("RTP_AGENT_TTS_PROVIDER", "livekit")
	t.Setenv("RTP_AGENT_TTS_MODEL", "cartesia/sonic-3")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if got := llm.Provider(app.Session.LLM); got != "livekit" {
		t.Fatalf("LLM provider = %q, want livekit", got)
	}
	if app.Config.LiveKitInferenceAPIKey != "test-livekit-key" || app.Config.LiveKitInferenceAPISecret != "test-livekit-secret" {
		t.Fatalf("LiveKit inference credentials = %q/%q, want environment values", app.Config.LiveKitInferenceAPIKey, app.Config.LiveKitInferenceAPISecret)
	}
	if app.Session.STT == nil {
		t.Fatal("Session STT is nil")
	}
	if got := app.Session.STT.Label(); got != "livekit.STT" {
		t.Fatalf("STT label = %q, want livekit.STT", got)
	}
	if app.Session.TTS == nil {
		t.Fatal("Session TTS is nil")
	}
	if got := app.Session.TTS.Label(); got != "livekit.TTS" {
		t.Fatalf("TTS label = %q, want livekit.TTS", got)
	}
}

func TestDefaultConfigFromEnvSelectsLiveKitTTSTokenizer(t *testing.T) {
	cases := []struct {
		name         string
		provider     string
		wantTypeName string
	}{
		{name: "advanced", provider: "advanced", wantTypeName: "*tokenize.AdvancedSentenceTokenizer"},
		{name: "blingfire", provider: "blingfire", wantTypeName: "*blingfire.SentenceTokenizer"},
		{name: "nltk", provider: "nltk", wantTypeName: "*nltk.SentenceTokenizer"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LIVEKIT_API_KEY", "test-livekit-key")
			t.Setenv("LIVEKIT_API_SECRET", "test-livekit-secret")
			t.Setenv("RTP_AGENT_TTS_PROVIDER", "livekit")
			t.Setenv("RTP_AGENT_TTS_TOKENIZER_PROVIDER", tc.provider)

			app, err := NewApp(DefaultConfigFromEnv())
			if err != nil {
				t.Fatalf("NewApp() error = %v", err)
			}
			if app.Session == nil || app.Session.TTS == nil {
				t.Fatal("Session TTS is nil")
			}
			field := reflect.ValueOf(app.Session.TTS).Elem().FieldByName("sentenceTokenizer")
			if !field.IsValid() {
				t.Fatal("livekit TTS sentenceTokenizer field is missing")
			}
			if field.IsNil() {
				t.Fatal("livekit TTS sentenceTokenizer is nil")
			}
			if got := field.Elem().Type().String(); got != tc.wantTypeName {
				t.Fatalf("sentenceTokenizer type = %q, want %s", got, tc.wantTypeName)
			}
		})
	}
}

func TestDefaultConfigFromEnvSelectsWordTokenizer(t *testing.T) {
	cases := []struct {
		name         string
		provider     string
		wantTypeName string
	}{
		{name: "basic", provider: "basic", wantTypeName: "*tokenize.BasicWordTokenizer"},
		{name: "blingfire", provider: "blingfire", wantTypeName: "*blingfire.WordTokenizer"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RTP_AGENT_WORD_TOKENIZER_PROVIDER", tc.provider)

			app, err := NewApp(DefaultConfigFromEnv())
			if err != nil {
				t.Fatalf("NewApp() error = %v", err)
			}
			if app.Session == nil {
				t.Fatal("Session is nil")
			}
			if app.Session.Options.WordTokenizer == nil {
				t.Fatal("WordTokenizer is nil")
			}
			if got := reflect.TypeOf(app.Session.Options.WordTokenizer).String(); got != tc.wantTypeName {
				t.Fatalf("WordTokenizer type = %q, want %s", got, tc.wantTypeName)
			}
		})
	}
}

func TestDefaultConfigFromEnvConfiguresTTSStreamPacer(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_STREAM_PACER_ENABLED", "true")
	t.Setenv("RTP_AGENT_TTS_STREAM_PACER_MIN_REMAINING_AUDIO_MS", "250")
	t.Setenv("RTP_AGENT_TTS_STREAM_PACER_MAX_TEXT_LENGTH", "120")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.Options.TTSStreamPacer == nil {
		t.Fatal("Session TTSStreamPacer is nil")
	}
	if got := app.Session.Options.TTSStreamPacer.MinRemainingAudio; got != 250*time.Millisecond {
		t.Fatalf("MinRemainingAudio = %v, want 250ms", got)
	}
	if got := app.Session.Options.TTSStreamPacer.MaxTextLength; got != 120 {
		t.Fatalf("MaxTextLength = %d, want 120", got)
	}
}

func TestDefaultConfigFromEnvPreservesExplicitZeroTTSStreamPacerOptions(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_STREAM_PACER_ENABLED", "true")
	t.Setenv("RTP_AGENT_TTS_STREAM_PACER_MIN_REMAINING_AUDIO_MS", "0")
	t.Setenv("RTP_AGENT_TTS_STREAM_PACER_MAX_TEXT_LENGTH", "0")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.Options.TTSStreamPacer == nil {
		t.Fatal("Session TTSStreamPacer is nil")
	}
	if got := app.Session.Options.TTSStreamPacer.MinRemainingAudio; got != 0 {
		t.Fatalf("MinRemainingAudio = %v, want explicit zero", got)
	}
	if !app.Session.Options.TTSStreamPacer.MinRemainingAudioSet {
		t.Fatal("MinRemainingAudioSet = false, want true for explicit env zero")
	}
	if got := app.Session.Options.TTSStreamPacer.MaxTextLength; got != 0 {
		t.Fatalf("MaxTextLength = %d, want explicit zero", got)
	}
	if !app.Session.Options.TTSStreamPacer.MaxTextLengthSet {
		t.Fatal("MaxTextLengthSet = false, want true for explicit env zero")
	}
}

func TestDefaultConfigFromEnvConfiguresTTSTextReplacements(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_TEXT_REPLACEMENTS", "OpenAI=Open A I,world=there")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got := app.Config.TTSTextReplacements["OpenAI"]; got != "Open A I" {
		t.Fatalf("Config.TTSTextReplacements[OpenAI] = %q, want Open A I", got)
	}
	if got := app.Session.Options.TTSTextReplacements["world"]; got != "there" {
		t.Fatalf("Session.Options.TTSTextReplacements[world] = %q, want there", got)
	}
}

func TestDefaultConfigFromEnvDisablesTTSTextTransforms(t *testing.T) {
	t.Setenv("RTP_AGENT_DISABLE_TTS_TEXT_TRANSFORMS", "true")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if !app.Config.DisableTTSTextTransforms {
		t.Fatal("Config.DisableTTSTextTransforms = false, want true")
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if !app.Session.Options.DisableTTSTextTransforms {
		t.Fatal("Session.Options.DisableTTSTextTransforms = false, want true")
	}
}

func TestDefaultConfigFromEnvConfiguresTTSTextTransforms(t *testing.T) {
	t.Setenv("RTP_AGENT_TTS_TEXT_TRANSFORMS", "filter_emoji, filter_markdown")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if got, want := app.Config.TTSTextTransforms, []string{"filter_emoji", "filter_markdown"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Config.TTSTextTransforms = %#v, want %#v", got, want)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if !app.Session.Options.TTSTextTransformsSet {
		t.Fatal("Session.Options.TTSTextTransformsSet = false, want true")
	}
	if got, want := app.Session.Options.TTSTextTransforms, []string{"filter_emoji", "filter_markdown"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Session.Options.TTSTextTransforms = %#v, want %#v", got, want)
	}
}

func TestDefaultConfigFromEnvConfiguresBackgroundAudio(t *testing.T) {
	t.Setenv("RTP_AGENT_BACKGROUND_AUDIO_AMBIENT", "city-ambience.ogg")
	t.Setenv("RTP_AGENT_BACKGROUND_AUDIO_THINKING", "/tmp/thinking.wav")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil {
		t.Fatal("Session is nil")
	}
	if app.Session.Options.BackgroundAudio == nil {
		t.Fatal("Session BackgroundAudio is nil")
	}
	if _, ok := backgroundAudioSource("city-ambience.ogg").(agent.BuiltinAudioClip); !ok {
		t.Fatalf("backgroundAudioSource(city-ambience.ogg) = %T, want BuiltinAudioClip", backgroundAudioSource("city-ambience.ogg"))
	}
	if got := backgroundAudioSource("/tmp/thinking.wav"); got != "/tmp/thinking.wav" {
		t.Fatalf("backgroundAudioSource(/tmp/thinking.wav) = %#v, want path string", got)
	}
}

func TestRunSessionConnectsRoomIOToSession(t *testing.T) {
	baseAgent := agent.NewAgent("test")
	baseAgent.VAD = &fakeAppVAD{}
	baseAgent.STT = &fakeAppSTT{}
	baseAgent.LLM = &fakeAppLLM{}
	baseAgent.TTS = &fakeAppTTS{}
	session := agent.NewAgentSession(baseAgent, nil, agent.AgentSessionOptions{})
	app := &App{
		Session:     session,
		Server:      worker.NewAgentServer(worker.WorkerOptions{}),
		RoomOptions: worker.RoomOptions{DisablePreConnectAudio: true, DisableTextInput: true},
	}
	jobCtx := &worker.JobContext{Room: lksdk.NewRoom(nil)}

	if err := app.runSession(jobCtx); err != nil {
		t.Fatalf("runSession() error = %v", err)
	}

	if app.RoomIO == nil {
		t.Fatal("RoomIO is nil")
	}
	if session.Room != jobCtx.Room {
		t.Fatal("session Room was not set from job context")
	}
	pipeline, ok := session.Assistant.(*agent.PipelineAgent)
	if !ok {
		t.Fatalf("session assistant = %T, want *agent.PipelineAgent", session.Assistant)
	}
	if pipeline.PublishAudio == nil {
		t.Fatal("session assistant PublishAudio was not connected to RoomIO")
	}
}

func TestRunSessionWiresRoomDeleteToJobContext(t *testing.T) {
	baseAgent := agent.NewAgent("test")
	baseAgent.VAD = &fakeAppVAD{}
	baseAgent.STT = &fakeAppSTT{}
	baseAgent.LLM = &fakeAppLLM{}
	baseAgent.TTS = &fakeAppTTS{}
	session := agent.NewAgentSession(baseAgent, nil, agent.AgentSessionOptions{})
	app := &App{
		Session:     session,
		Server:      worker.NewAgentServer(worker.WorkerOptions{}),
		RoomOptions: worker.RoomOptions{DisablePreConnectAudio: true, DisableTextInput: true},
	}
	jobCtx := worker.NewJobContext(&livekit.Job{Id: "job_delete_room", Room: &livekit.Room{Name: "room-a"}}, "", "", "")
	jobCtx.Room = lksdk.NewRoom(nil)

	if err := app.runSession(jobCtx); err != nil {
		t.Fatalf("runSession() error = %v", err)
	}

	if app.RoomIO == nil {
		t.Fatal("RoomIO is nil")
	}
	if app.RoomIO.Options.DeleteRoom == nil {
		t.Fatal("RoomIO DeleteRoom option = nil, want JobContext.DeleteRoom wiring")
	}
	if err := app.RoomIO.Options.DeleteRoom(context.Background(), "room-a"); err != nil {
		t.Fatalf("RoomIO DeleteRoom() error = %v, want best-effort nil", err)
	}
}

func TestRunSessionStartsAudioRecorderForRecordedJob(t *testing.T) {
	baseAgent := agent.NewAgent("test")
	baseAgent.VAD = &fakeAppVAD{}
	baseAgent.STT = &fakeAppSTT{}
	baseAgent.LLM = &fakeAppLLM{}
	baseAgent.TTS = &fakeAppTTS{}
	session := agent.NewAgentSession(baseAgent, nil, agent.AgentSessionOptions{})
	app := &App{
		Session:     session,
		Server:      worker.NewAgentServer(worker.WorkerOptions{}),
		RoomOptions: worker.RoomOptions{DisablePreConnectAudio: true, DisableTextInput: true},
	}
	jobCtx := worker.NewJobContext(&livekit.Job{Id: "job_record_audio", Room: &livekit.Room{Name: "room-a"}}, "", "", "")
	jobCtx.Room = lksdk.NewRoom(nil)
	sessionDir := t.TempDir()
	jobCtx.SetSessionDirectory(sessionDir)
	jobCtx.Report.RecordingOptions.Audio = true

	if err := app.runSession(jobCtx); err != nil {
		t.Fatalf("runSession() error = %v", err)
	}
	t.Cleanup(func() {
		if app.RoomIO != nil && app.RoomIO.Recorder != nil {
			_ = app.RoomIO.Recorder.Stop()
		}
	})

	if jobCtx.Report.AudioRecordingPath == nil {
		t.Fatal("AudioRecordingPath = nil, want recorder output path")
	}
	if got, want := *jobCtx.Report.AudioRecordingPath, filepath.Join(sessionDir, "audio.ogg"); got != want {
		t.Fatalf("AudioRecordingPath = %q, want %q", got, want)
	}
}

func TestRunSessionInstallsJobContextOnSession(t *testing.T) {
	baseAgent := agent.NewAgent("test")
	session := agent.NewAgentSession(baseAgent, nil, agent.AgentSessionOptions{})
	app := &App{
		Session: session,
		Server:  worker.NewAgentServer(worker.WorkerOptions{}),
	}
	jobCtx := worker.NewJobContext(&livekit.Job{Id: "job_run_context", Room: &livekit.Room{Name: "room-a"}}, "", "", "")

	if err := app.runSession(jobCtx); err != nil {
		t.Fatalf("runSession() error = %v", err)
	}

	value, err := session.JobContext()
	if err != nil {
		t.Fatalf("session JobContext() error = %v, want nil", err)
	}
	if value != jobCtx {
		t.Fatalf("session JobContext() = %#v, want active job context", value)
	}
}

func TestDefaultConfigFromEnvConfiguresLLMTurnDetector(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "openai")
	t.Setenv("RTP_AGENT_LLM_MODEL", "gpt-4o-mini")
	t.Setenv("RTP_AGENT_TURN_DETECTOR_PROVIDER", "llm")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Agent == nil {
		t.Fatal("Agent is nil")
	}
	if got := fmt.Sprintf("%T", app.Agent.TurnDetector); got != "*agent.LLMTurnDetector" {
		t.Fatalf("TurnDetector type = %q, want *agent.LLMTurnDetector", got)
	}
}

func TestDefaultConfigFromEnvConfiguresPipecatAudioTurnDetector(t *testing.T) {
	t.Setenv("RTP_AGENT_TURN_DETECTOR_PROVIDER", "pipecat")
	fake := &fakeAppAudioTurnDetector{}
	oldFactory := appNewPipecatSmartTurn
	appNewPipecatSmartTurn = func() (agent.AudioTurnDetector, error) {
		return fake, nil
	}
	t.Cleanup(func() { appNewPipecatSmartTurn = oldFactory })

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Agent == nil {
		t.Fatal("Agent is nil")
	}
	if app.Agent.AudioTurnDetector != fake {
		t.Fatalf("AudioTurnDetector = %T, want configured Pipecat detector", app.Agent.AudioTurnDetector)
	}
}

func TestDefaultConfigFromEnvConfiguresLiveKitTurnDetector(t *testing.T) {
	t.Setenv("RTP_AGENT_TURN_DETECTOR_PROVIDER", "livekit")
	t.Setenv("LIVEKIT_REMOTE_EOT_URL", "https://turn.example")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Agent == nil {
		t.Fatal("Agent is nil")
	}
	if got := fmt.Sprintf("%T", app.Agent.TurnDetector); got != "*livekit.Model" {
		t.Fatalf("TurnDetector type = %q, want *livekit.Model", got)
	}
	model, ok := app.Agent.TurnDetector.(*adapterlivekit.Model)
	if !ok {
		t.Fatalf("TurnDetector = %T, want *livekit.Model", app.Agent.TurnDetector)
	}
	if model.Model() != adapterlivekit.ModelMultilingual {
		t.Fatalf("TurnDetector model = %q, want multilingual", model.Model())
	}
	if got := model.RemoteInferenceURL(); got != "https://turn.example/eot/multi" {
		t.Fatalf("RemoteInferenceURL() = %q, want configured remote EOT URL", got)
	}
	if app.Agent.AudioTurnDetector != nil {
		t.Fatalf("AudioTurnDetector = %T, want nil for LiveKit text turn detector", app.Agent.AudioTurnDetector)
	}
}

func TestDefaultConfigFromEnvConfiguresLiveKitLocalTurnDetector(t *testing.T) {
	chdirAppTest(t, t.TempDir())
	t.Setenv("RTP_AGENT_TURN_DETECTOR_PROVIDER", "livekit")

	_, err := NewApp(DefaultConfigFromEnv())
	if err == nil || !strings.Contains(err.Error(), "tokenizer") {
		t.Fatalf("NewApp() error = %v, want missing local tokenizer error", err)
	}
}

func TestDefaultConfigFromEnvSelectsPhonicRealtimeModel(t *testing.T) {
	t.Setenv("PHONIC_API_KEY", "test-phonic-key")
	t.Setenv("RTP_AGENT_REALTIME_PROVIDER", "phonic")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.RealtimeModel == nil {
		t.Fatal("RealtimeModel is nil")
	}
	if got := llm.RealtimeModelName(app.RealtimeModel); got != "phonic" {
		t.Fatalf("Realtime model = %q, want phonic", got)
	}
	if got := llm.RealtimeProvider(app.RealtimeModel); got != "phonic" {
		t.Fatalf("Realtime provider = %q, want phonic", got)
	}
	if _, ok := app.Session.Assistant.(*agent.MultimodalAgent); !ok {
		t.Fatalf("Session assistant = %T, want *agent.MultimodalAgent", app.Session.Assistant)
	}
}

func TestDefaultConfigFromEnvSelectsAnthropicLLM(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-anthropic-key")
	t.Setenv("RTP_AGENT_LLM_PROVIDER", "anthropic")
	t.Setenv("RTP_AGENT_LLM_MODEL", "claude-test")
	t.Setenv("RTP_AGENT_LLM_BASE_URL", "https://anthropic.example/")

	app, err := NewApp(DefaultConfigFromEnv())
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	if app.Session == nil || app.Session.LLM == nil {
		t.Fatal("Session LLM is nil")
	}
	if _, ok := app.Session.LLM.(*anthropic.AnthropicLLM); !ok {
		t.Fatalf("Session LLM = %T, want *anthropic.AnthropicLLM", app.Session.LLM)
	}
	if got := llm.Provider(app.Session.LLM); got != "anthropic.example" {
		t.Fatalf("LLM provider = %q, want configured Anthropic base URL host", got)
	}
	if got := llm.Model(app.Session.LLM); got != "claude-test" {
		t.Fatalf("LLM model = %q, want claude-test", got)
	}
}

func TestInitRegistersWorkerEntrypoint(t *testing.T) {
	app, err := Init(AppConfig{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if app.Server == nil {
		t.Fatal("Server is nil")
	}
	err = app.Server.Run(context.Background())
	if err == nil {
		t.Fatal("Run() error = nil, want missing ws_url precondition error")
	}
	if err.Error() != "ws_url is required, or set LIVEKIT_URL environment variable" {
		t.Fatalf("Run() error = %q, want missing ws_url after registered entrypoint", err.Error())
	}
}

type fakeAppVAD struct{}

func (f *fakeAppVAD) Label() string { return "fake-vad" }
func (f *fakeAppVAD) Model() string { return "fake-vad" }
func (f *fakeAppVAD) Provider() string {
	return "fake"
}
func (f *fakeAppVAD) Capabilities() vad.VADCapabilities { return vad.VADCapabilities{} }
func (f *fakeAppVAD) OnMetricsCollected(vad.VADMetricsHandler) func() {
	return func() {}
}
func (f *fakeAppVAD) Stream(context.Context) (vad.VADStream, error) {
	return &fakeAppVADStream{}, nil
}

type fakeAppVADStream struct{}

func (f *fakeAppVADStream) PushFrame(*model.AudioFrame) error { return nil }
func (f *fakeAppVADStream) Flush() error                      { return nil }
func (f *fakeAppVADStream) EndInput() error                   { return nil }
func (f *fakeAppVADStream) Close() error                      { return nil }
func (f *fakeAppVADStream) Next() (*vad.VADEvent, error)      { return nil, io.EOF }

type fakeAppSTT struct{}

func (f *fakeAppSTT) Label() string { return "fake-stt" }
func (f *fakeAppSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true}
}
func (f *fakeAppSTT) Stream(context.Context, string) (stt.RecognizeStream, error) {
	return &fakeAppSTTStream{}, nil
}
func (f *fakeAppSTT) Recognize(context.Context, []*model.AudioFrame, string) (*stt.SpeechEvent, error) {
	return nil, nil
}

type fakeAppSTTStream struct{}

func (f *fakeAppSTTStream) PushFrame(*model.AudioFrame) error { return nil }
func (f *fakeAppSTTStream) Flush() error                      { return nil }
func (f *fakeAppSTTStream) Close() error                      { return nil }
func (f *fakeAppSTTStream) Next() (*stt.SpeechEvent, error)   { return nil, io.EOF }

type fakeAppLLM struct{}

func (f *fakeAppLLM) Chat(context.Context, *llm.ChatContext, ...llm.ChatOption) (llm.LLMStream, error) {
	return &fakeAppLLMStream{}, nil
}

type fakeAppLLMStream struct{}

func (f *fakeAppLLMStream) Next() (*llm.ChatChunk, error) { return nil, io.EOF }
func (f *fakeAppLLMStream) Close() error                  { return nil }

type fakeEvalLLM struct {
	stream llm.LLMStream
}

func (f *fakeEvalLLM) Chat(context.Context, *llm.ChatContext, ...llm.ChatOption) (llm.LLMStream, error) {
	return f.stream, nil
}

type fakeEvalLLMStream struct {
	chunks []*llm.ChatChunk
	index  int
}

func (f *fakeEvalLLMStream) Next() (*llm.ChatChunk, error) {
	if f.index >= len(f.chunks) {
		return nil, io.EOF
	}
	chunk := f.chunks[f.index]
	f.index++
	return chunk, nil
}
func (f *fakeEvalLLMStream) Close() error { return nil }

type fakeAppTTS struct{}

func (f *fakeAppTTS) Label() string { return "fake-tts" }
func (f *fakeAppTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true}
}
func (f *fakeAppTTS) SampleRate() int  { return 24000 }
func (f *fakeAppTTS) NumChannels() int { return 1 }
func (f *fakeAppTTS) Synthesize(context.Context, string) (tts.ChunkedStream, error) {
	return nil, nil
}
func (f *fakeAppTTS) Stream(context.Context) (tts.SynthesizeStream, error) {
	return &fakeAppTTSStream{}, nil
}

type fakeAppTTSStream struct{}

func (f *fakeAppTTSStream) PushText(string) error { return nil }
func (f *fakeAppTTSStream) Flush() error          { return nil }
func (f *fakeAppTTSStream) Close() error          { return nil }
func (f *fakeAppTTSStream) Next() (*tts.SynthesizedAudio, error) {
	return nil, io.EOF
}

type fakeAppDtmfPublisher struct{}

func (f *fakeAppDtmfPublisher) PublishDTMF(code int32, digit string) error {
	return nil
}

type fakeAppAudioTurnDetector struct{}

func (f *fakeAppAudioTurnDetector) PredictEndOfTurnAudio(context.Context, []*model.AudioFrame) (float64, error) {
	return 0.9, nil
}

func chdirAppTest(t *testing.T, dir string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore Chdir error = %v", err)
		}
	})
}

type appLogEntry struct {
	level string
	msg   string
	err   error
	kv    []any
}

func (e appLogEntry) value(key string) any {
	for i := 0; i+1 < len(e.kv); i += 2 {
		if e.kv[i] == key {
			return e.kv[i+1]
		}
	}
	return nil
}

type appRecordingLogger struct {
	mu           sync.Mutex
	entries      []appLogEntry
	entriesCh    chan appLogEntry
	blockMsg     string
	blockStarted chan struct{}
	unblock      chan struct{}
}

func (l *appRecordingLogger) record(entry appLogEntry) {
	if l.blockMsg != "" && entry.msg == l.blockMsg {
		if l.blockStarted != nil {
			select {
			case l.blockStarted <- struct{}{}:
			default:
			}
		}
		if l.unblock != nil {
			<-l.unblock
		}
	}
	l.mu.Lock()
	l.entries = append(l.entries, entry)
	ch := l.entriesCh
	l.mu.Unlock()
	if ch != nil {
		select {
		case ch <- entry:
		default:
		}
	}
}

func (l *appRecordingLogger) Debugw(msg string, keysAndValues ...any) {
	l.record(appLogEntry{level: "debug", msg: msg, kv: append([]any(nil), keysAndValues...)})
}
func (l *appRecordingLogger) Infow(msg string, keysAndValues ...any) {
	l.record(appLogEntry{level: "info", msg: msg, kv: append([]any(nil), keysAndValues...)})
}
func (l *appRecordingLogger) Warnw(msg string, err error, keysAndValues ...any) {
	l.record(appLogEntry{level: "warn", msg: msg, err: err, kv: append([]any(nil), keysAndValues...)})
}
func (l *appRecordingLogger) Errorw(msg string, err error, keysAndValues ...any) {
	l.record(appLogEntry{level: "error", msg: msg, err: err, kv: append([]any(nil), keysAndValues...)})
}
func (l *appRecordingLogger) WithValues(keysAndValues ...any) livekitlogger.Logger {
	return l
}
func (l *appRecordingLogger) WithUnlikelyValues(keysAndValues ...any) livekitlogger.UnlikelyLogger {
	return livekitlogger.GetDiscardLogger().WithUnlikelyValues(keysAndValues...)
}
func (l *appRecordingLogger) WithName(name string) livekitlogger.Logger {
	return l
}
func (l *appRecordingLogger) WithComponent(component string) livekitlogger.Logger {
	return l
}
func (l *appRecordingLogger) WithCallDepth(depth int) livekitlogger.Logger {
	return l
}
func (l *appRecordingLogger) WithItemSampler() livekitlogger.Logger {
	return l
}
func (l *appRecordingLogger) WithoutSampler() livekitlogger.Logger {
	return l
}
func (l *appRecordingLogger) WithDeferredValues() (livekitlogger.Logger, livekitlogger.DeferredFieldResolver) {
	return livekitlogger.GetDiscardLogger().WithDeferredValues()
}
