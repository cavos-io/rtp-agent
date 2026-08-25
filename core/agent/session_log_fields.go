package agent

// jobLogFieldsProvider is satisfied by *worker.JobContext without importing it,
// which core/agent must not do.
type jobLogFieldsProvider interface {
	LogContextFields() map[string]any
}

// SessionLogValues appends the job's log context fields (job_id, room,
// worker_id, call_logs_id) to a log call's key/value pairs.
func SessionLogValues(s *AgentSession, values ...any) []any {
	if s == nil {
		return values
	}
	provider := s.jobLogFields.Load()
	if provider == nil {
		return values
	}
	fields := (*provider).LogContextFields()
	if len(fields) == 0 {
		return values
	}
	out := make([]any, 0, len(values)+len(fields)*2)
	out = append(out, values...)
	for key, value := range fields {
		if key == "" || value == nil {
			continue
		}
		out = append(out, key, value)
	}
	return out
}
