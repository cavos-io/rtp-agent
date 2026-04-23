package evals

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

type mockLLM struct {
	chatFunc func(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error)
}

func (m *mockLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return m.chatFunc(ctx, chatCtx, opts...)
}

type mockStream struct {
	chunks []*llm.ChatChunk
	index  int
}

func (s *mockStream) Next() (*llm.ChatChunk, error) {
	if s.index >= len(s.chunks) {
		return nil, io.EOF
	}
	chunk := s.chunks[s.index]
	s.index++
	return chunk, nil
}

func (s *mockStream) Close() error { return nil }

func TestJudgmentResult_Passed(t *testing.T) {
	j := &JudgmentResult{Verdict: VerdictPass}
	if !j.Passed() {
		t.Error("Expected true")
	}
	j.Verdict = VerdictFail
	if j.Passed() {
		t.Error("Expected false")
	}
}

func TestEvaluationResult_Score(t *testing.T) {
	res := &EvaluationResult{
		Judgments: map[string]*JudgmentResult{
			"j1": {Verdict: VerdictPass},
			"j2": {Verdict: VerdictFail},
			"j3": {Verdict: VerdictMaybe},
		},
	}
	expected := (1.0 + 0.0 + 0.5) / 3.0
	if res.Score() != expected {
		t.Errorf("Expected %v, got %v", expected, res.Score())
	}

	if res.AllPassed() {
		t.Error("Expected AllPassed to be false")
	}
	if !res.AnyPassed() {
		t.Error("Expected AnyPassed to be true")
	}
	if res.MajorityPassed() {
		t.Error("Expected MajorityPassed to be false (score 0.5)")
	}
	if res.NoneFailed() {
		t.Error("Expected NoneFailed to be false")
	}
}

func TestJudge_Evaluate(t *testing.T) {
	verdictArgs := map[string]interface{}{
		"verdict":   "pass",
		"reasoning": "Criteria met",
	}
	argsJSON, _ := json.Marshal(verdictArgs)

	mock := &mockLLM{
		chatFunc: func(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
			return &mockStream{
				chunks: []*llm.ChatChunk{
					{
						Delta: &llm.ChoiceDelta{
							ToolCalls: []llm.FunctionToolCall{
								{
									Name:      "submit_verdict",
									Arguments: string(argsJSON),
								},
							},
						},
					},
				},
			}, nil
		},
	}

	judge := NewJudge("test_judge", "Be nice", mock)
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "Hello"}}})

	res, err := judge.Evaluate(context.Background(), chatCtx, nil, nil)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}

	if res.Verdict != VerdictPass {
		t.Errorf("Expected pass, got %v", res.Verdict)
	}
	if res.Reasoning != "Criteria met" {
		t.Errorf("Expected 'Criteria met', got %q", res.Reasoning)
	}
}

func TestJudgeGroup_Evaluate(t *testing.T) {
	verdictArgs := map[string]interface{}{
		"verdict":   "pass",
		"reasoning": "OK",
	}
	argsJSON, _ := json.Marshal(verdictArgs)

	mock := &mockLLM{
		chatFunc: func(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
			return &mockStream{
				chunks: []*llm.ChatChunk{
					{
						Delta: &llm.ChoiceDelta{
							ToolCalls: []llm.FunctionToolCall{
								{
									Name:      "submit_verdict",
									Arguments: string(argsJSON),
								},
							},
						},
					},
				},
			}, nil
		},
	}

	j1 := NewJudge("j1", "i1", mock)
	j2 := NewJudge("j2", "i2", mock)

	group := NewJudgeGroup(mock, []Evaluator{j1, j2})
	res, err := group.Evaluate(context.Background(), llm.NewChatContext(), nil)
	if err != nil {
		t.Fatalf("Group evaluate failed: %v", err)
	}

	if len(res.Judgments) != 2 {
		t.Errorf("Expected 2 judgments, got %d", len(res.Judgments))
	}
}

func TestBuiltInJudges(t *testing.T) {
	// Just verify they don't panic and return valid evaluators
	TaskCompletionJudge(nil)
	AccuracyJudge(nil)
	RelevancyJudge(nil)
	SafetyJudge(nil)
	CoherenceJudge(nil)
	ConcisenessJudge(nil)
	HandoffJudge(nil)
	ToolUseJudge(nil)
}
