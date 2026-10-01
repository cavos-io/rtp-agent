package agent

// Barge-in gating: an optional, pluggable policy the host application can inject
// to decide what happens when a user speaks over the agent (a "barge-in").
//
// The verdict decides one thing only: whether the agent's audio stops. Whether the
// utterance becomes an LLM turn is a separate question, answered by BackchannelClassifier.
// The runtime holds no policy of its own — no word lists, no thresholds. Provide a
// BargeInDecider via AgentSessionOptions.BargeInDecider to enable gating; leave it nil for
// the default runtime behavior.
//
// The decider is consulted only while the agent is speaking AND an
// AudioTurnDetector (smart turn) is active.

// BargeInDecision is the verdict for user speech that overlaps the agent's own
// speech.
type BargeInDecision int

const (
	// BargeInInterrupt stops the agent's audio and commits the user turn.
	BargeInInterrupt BargeInDecision = iota
	// BargeInIgnore leaves the agent's audio playing. The turn still commits, so the reply
	// queues behind the current sentence — only a BackchannelClassifier match drops a turn.
	BargeInIgnore
	// BargeInContinue also leaves the audio playing and still commits; it differs from
	// BargeInIgnore only in the logged reason.
	BargeInContinue
)

func (d BargeInDecision) String() string {
	switch d {
	case BargeInInterrupt:
		return "interrupt"
	case BargeInIgnore:
		return "ignore"
	case BargeInContinue:
		return "continue"
	default:
		return "unknown"
	}
}

// BargeInInput carries the signals the runtime has gathered about the current
// barge-in. All fields describe the accumulated user speech so far.
type BargeInInput struct {
	// Transcript is the accumulated user transcript for the current turn.
	Transcript string
	// SpeechMs is how long the user has been speaking, in milliseconds.
	SpeechMs int
	// WordCount is the tokenized word count of Transcript.
	WordCount int
	// AgentSpeaking reports whether the agent was speaking at user onset (always
	// true when the gate is consulted; provided for decider convenience).
	AgentSpeaking bool
	// SmartTurnPresent reports whether a smart-turn result is available yet.
	SmartTurnPresent bool
	// SmartTurnComplete is the smart-turn verdict (probability >= threshold).
	SmartTurnComplete bool
	// SmartTurnProbability is the raw smart-turn probability [0,1], 0 if absent.
	SmartTurnProbability float64
}

// BargeInDecider decides how to handle a barge-in. Implementations must be safe
// for concurrent use and must not block.
type BargeInDecider interface {
	DecideBargeIn(BargeInInput) (decision BargeInDecision, reason string)
}

// BackchannelClassifier optionally lets the decider mark an utterance as a pure
// acknowledgement. An overlapping backchannel is the ONLY thing dropped from the LLM turn
// (it is woven into the assistant message instead); every other utterance commits,
// interrupted or not. A decider that does not implement this keeps the legacy behavior
// where any non-Interrupt verdict drops the turn.
type BackchannelClassifier interface {
	IsBackchannelOnly(transcript string) bool
}
