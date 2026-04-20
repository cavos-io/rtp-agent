package console

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/model"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// AudioIOSource represents any audio I/O that can provide mic frames
type AudioIOSource interface {
	MicTapFrames() <-chan *model.AudioFrame
}

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4")).
			Padding(0, 1).
			MarginTop(1).
			MarginBottom(1)

	stateIdleStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	stateThinkingStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#E8D13E")).Bold(true)
	stateSpeakingStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#56F4A8")).Bold(true)
	stateListeningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#3E8EE8")).Bold(true)

	logStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#AAAAAA"))
)

type tickMsg time.Time
type logMsg string
type metricsMsg telemetry.UsageSummary
type agentStateMsg agent.AgentState
type userStateMsg agent.UserState
type audioLevelMsg float64

// ConsoleModel drives the interactive terminal UI
type ConsoleModel struct {
	session    *agent.AgentSession
	agentState agent.AgentState
	userState  agent.UserState

	logs       []string
	metrics    *telemetry.UsageSummary
	audioLevel float64 // 0.0 to 1.0

	ctx    context.Context
	cancel context.CancelFunc

	audioIO AudioIOSource
}

func NewConsoleModel(ctx context.Context, audioIO AudioIOSource, session *agent.AgentSession) *ConsoleModel {
	ctx, cancel := context.WithCancel(ctx)
	return &ConsoleModel{
		session:    session,
		agentState: agent.AgentStateIdle,
		userState:  agent.UserStateListening,
		logs:       make([]string, 0),
		ctx:        ctx,
		cancel:     cancel,
		audioIO:    audioIO,
	}
}

func (m *ConsoleModel) Init() tea.Cmd {
	return tea.Batch(
		tickCmd(),
		m.listenToAudio(),
		m.listenToAgentState(),
		m.listenToUserState(),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*50, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m *ConsoleModel) listenToAudio() tea.Cmd {
	return func() tea.Msg {
		if m.audioIO == nil {
			return nil
		}

		select {
		case <-m.ctx.Done():
			return nil
		case frame := <-m.audioIO.MicTapFrames():
			if frame == nil {
				return nil
			}

			// Calculate simple RMS amplitude for visualizer
			var sum float64
			pcm := make([]int16, len(frame.Data)/2)
			for i := 0; i < len(pcm); i++ {
				pcm[i] = int16(frame.Data[i*2]) | (int16(frame.Data[i*2+1]) << 8)
				sum += float64(pcm[i]) * float64(pcm[i])
			}

			rms := 0.0
			if len(pcm) > 0 {
				rms = math.Sqrt(sum / float64(len(pcm)))
			}

			// Normalize to 0-1 range roughly (max int16 is 32767)
			level := math.Min(1.0, rms/10000.0)
			return audioLevelMsg(level)
		}
	}
}

func (m *ConsoleModel) listenToAgentState() tea.Cmd {
	return func() tea.Msg {
		if m.session == nil {
			return nil
		}

		select {
		case <-m.ctx.Done():
			return nil
		case ev := <-m.session.AgentStateChangedCh:
			fmt.Printf("[Console UI] Agent state: %s → %s\n", ev.OldState, ev.NewState)
			return agentStateMsg(ev.NewState)
		}
	}
}

func (m *ConsoleModel) listenToUserState() tea.Cmd {
	return func() tea.Msg {
		if m.session == nil {
			return nil
		}

		select {
		case <-m.ctx.Done():
			return nil
		case ev := <-m.session.UserStateChangedCh:
			fmt.Printf("[Console UI] User state: %s → %s\n", ev.OldState, ev.NewState)
			return userStateMsg(ev.NewState)
		}
	}
}

func (m *ConsoleModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.cancel()
			return m, tea.Quit
		}

	case agentStateMsg:
		m.agentState = agent.AgentState(msg)
		// Keep listening for more state changes
		return m, m.listenToAgentState()

	case userStateMsg:
		m.userState = agent.UserState(msg)
		// Keep listening for more state changes
		return m, m.listenToUserState()

	case logMsg:
		m.logs = append(m.logs, string(msg))
		if len(m.logs) > 10 {
			m.logs = m.logs[len(m.logs)-10:]
		}

	case metricsMsg:
		sum := telemetry.UsageSummary(msg)
		m.metrics = &sum

	case audioLevelMsg:
		m.audioLevel = float64(msg)
		// immediately ask for next frame
		return m, m.listenToAudio()

	case tickMsg:
		// decay audio level smoothly
		m.audioLevel *= 0.8
		return m, tickCmd()
	}

	return m, nil
}

func (m *ConsoleModel) View() string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("LiveKit Agent Console") + "\n\n")

	// Render States
	agentStateStr := "Agent: "
	switch m.agentState {
	case agent.AgentStateIdle:
		agentStateStr += stateIdleStyle.Render("Idle")
	case agent.AgentStateThinking:
		agentStateStr += stateThinkingStyle.Render("Thinking...")
	case agent.AgentStateSpeaking:
		agentStateStr += stateSpeakingStyle.Render("Speaking 🔉")
	default:
		agentStateStr += string(m.agentState)
	}

	userStateStr := "User: "
	switch m.userState {
	case agent.UserStateListening:
		userStateStr += stateIdleStyle.Render("Listening")
	case agent.UserStateSpeaking:
		userStateStr += stateListeningStyle.Render("Speaking 🎤")
	default:
		userStateStr += string(m.userState)
	}

	b.WriteString(fmt.Sprintf("%s  |  %s\n\n", agentStateStr, userStateStr))

	// Render Visualizer
	bars := int(m.audioLevel * 40)
	if bars == 0 && m.audioLevel > 0 {
		bars = 1
	}
	visualizer := strings.Repeat("█", bars)
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00")).Render(fmt.Sprintf("Mic: [%-40s]\n\n", visualizer)))

	// Render Metrics
	if m.metrics != nil {
		metricsBox := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Render(
			fmt.Sprintf("Tokens - Prompt: %d | Completion: %d | Total: %d\nTTS Duration: %.1fs | STT Duration: %.1fs",
				m.metrics.LLMPromptTokens,
				m.metrics.LLMCompletionTokens,
				m.metrics.LLMPromptTokens+m.metrics.LLMCompletionTokens,
				m.metrics.TTSAudioDuration,
				m.metrics.STTAudioDuration,
			),
		)
		b.WriteString(metricsBox + "\n\n")
	}

	// Render Logs
	b.WriteString("Event Log:\n")
	for _, l := range m.logs {
		b.WriteString(logStyle.Render(l) + "\n")
	}

	b.WriteString("\nPress 'q' or 'ctrl+c' to quit.\n")
	return b.String()
}

