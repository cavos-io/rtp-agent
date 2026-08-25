package agent

import "testing"

type fakeJobLogFields struct {
	fields map[string]any
}

func (f fakeJobLogFields) LogContextFields() map[string]any {
	return f.fields
}

func TestSessionLogValuesNilSafe(t *testing.T) {
	if got := SessionLogValues(nil, "a", 1); len(got) != 2 {
		t.Fatalf("SessionLogValues(nil) = %v, want the values unchanged", got)
	}

	session := &AgentSession{}
	if got := SessionLogValues(session, "a", 1); len(got) != 2 {
		t.Fatalf("SessionLogValues without job context = %v, want the values unchanged", got)
	}
}

func TestSessionLogValuesAppendsJobFields(t *testing.T) {
	session := &AgentSession{}
	session.SetJobContext(fakeJobLogFields{fields: map[string]any{
		"job_id":       "AJ_test",
		"call_logs_id": "42",
		"empty":        nil,
		"":             "skipped",
	}})

	got := SessionLogValues(session, "a", 1)

	pairs := map[string]any{}
	for i := 2; i+1 < len(got); i += 2 {
		pairs[got[i].(string)] = got[i+1]
	}
	if got[0] != "a" || got[1] != 1 {
		t.Fatalf("SessionLogValues dropped the caller values: %v", got)
	}
	if pairs["job_id"] != "AJ_test" || pairs["call_logs_id"] != "42" {
		t.Fatalf("SessionLogValues = %v, want job_id and call_logs_id", got)
	}
	if len(pairs) != 2 {
		t.Fatalf("SessionLogValues kept nil or empty keys: %v", pairs)
	}
}

func TestSessionLogValuesClearedWhenJobContextHasNoFields(t *testing.T) {
	session := &AgentSession{}
	session.SetJobContext(fakeJobLogFields{fields: map[string]any{"job_id": "AJ_test"}})
	session.SetJobContext("not a provider")

	if got := SessionLogValues(session, "a", 1); len(got) != 2 {
		t.Fatalf("SessionLogValues after replacing the job context = %v, want the values unchanged", got)
	}
}
