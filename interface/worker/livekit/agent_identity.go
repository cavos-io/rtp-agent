package livekit

func AgentIdentityForJobID(jobID string) string {
	return "agent-" + jobID
}
