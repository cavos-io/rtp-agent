package console

import (
	"context"
	"fmt"
	"sync"

	"github.com/cavos-io/rtp-agent/core/agent"
)

// ConsoleManager is a singleton that manages console I/O for agent sessions
// It follows the pattern from the official Python SDK's AgentsConsole
type ConsoleManager struct {
	mu         sync.Mutex
	ioAcquired bool
	ioSession  *agent.AgentSession
	audioInput *AudioIO
	enabled    bool
}

var (
	consoleInstance *ConsoleManager
	consoleMutex    sync.Once
)

// GetInstance returns the singleton ConsoleManager instance
func GetInstance() *ConsoleManager {
	consoleMutex.Do(func() {
		consoleInstance = &ConsoleManager{
			enabled: true,
		}
	})
	return consoleInstance
}

// Acquire I/O for a session (like python's acquire_io)
// This should be called when the session starts
func (cm *ConsoleManager) AcquireIO(ctx context.Context, session *agent.AgentSession) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.ioAcquired {
		return fmt.Errorf("ConsoleManager I/O already acquired by another session")
	}

	// Create real host audio I/O components (mic + speaker)
	cm.audioInput = NewAudioIO()
	if err := cm.audioInput.Start(ctx); err != nil {
		return fmt.Errorf("failed to start AudioIO: %w", err)
	}

	// Store the session reference
	cm.ioSession = session
	cm.ioAcquired = true

	fmt.Println("[ConsoleManager] I/O acquired successfully")

	// Attach I/O to the session (like _update_sess_io in Python)
	cm.updateSessionIO(session)

	return nil
}

// updateSessionIO sets the I/O on the session
// Equivalent to Python's _update_sess_io
func (cm *ConsoleManager) updateSessionIO(session *agent.AgentSession) {
	if session == nil {
		return
	}

	session.Input.Audio = cm.audioInput
	session.SetAudioOutput(cm.audioInput)

	fmt.Println("[ConsoleManager] Audio I/O attached to session")
}

// IsIOAcquired returns whether I/O has been acquired
func (cm *ConsoleManager) IsIOAcquired() bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.ioAcquired
}

// IsEnabled returns whether console mode is enabled
func (cm *ConsoleManager) IsEnabled() bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.enabled
}

// GetAudioInput returns the console audio input
func (cm *ConsoleManager) GetAudioInput() *AudioIO {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.audioInput
}

// Stop stops the console I/O
func (cm *ConsoleManager) Stop() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.audioInput != nil {
		cm.audioInput.Stop()
	}

	cm.ioAcquired = false
	return nil
}

