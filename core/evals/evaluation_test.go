package evals

import (
	"context"
	"errors"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestJudgmentResultStateHelpers(t *testing.T) {
	pass := &JudgmentResult{Verdict: VerdictPass}
	fail := &JudgmentResult{Verdict: VerdictFail}
	maybe := &JudgmentResult{Verdict: VerdictMaybe}

	if !pass.Passed() || pass.Failed() || pass.Uncertain() {
		t.Fatalf("pass helpers = passed:%v failed:%v uncertain:%v, want only passed", pass.Passed(), pass.Failed(), pass.Uncertain())
	}
	if fail.Passed() || !fail.Failed() || fail.Uncertain() {
		t.Fatalf("fail helpers = passed:%v failed:%v uncertain:%v, want only failed", fail.Passed(), fail.Failed(), fail.Uncertain())
	}
	if maybe.Passed() || maybe.Failed() || !maybe.Uncertain() {
		t.Fatalf("maybe helpers = passed:%v failed:%v uncertain:%v, want only uncertain", maybe.Passed(), maybe.Failed(), maybe.Uncertain())
	}

	var nilResult *JudgmentResult
	if nilResult.Passed() || nilResult.Failed() || nilResult.Uncertain() {
		t.Fatal("nil JudgmentResult helpers returned true, want all false")
	}
}

func TestEvaluationResultScoreUsesUncertainHelper(t *testing.T) {
	result := &EvaluationResult{Judgments: map[string]*JudgmentResult{
		"pass":  {Verdict: VerdictPass},
		"maybe": {Verdict: VerdictMaybe},
		"fail":  {Verdict: VerdictFail},
	}}

	if got := result.Score(); got != 0.5 {
		t.Fatalf("Score() = %v, want 0.5", got)
	}
	if result.AllPassed() {
		t.Fatal("AllPassed() = true, want false when maybe/fail are present")
	}
	if !result.AnyPassed() {
		t.Fatal("AnyPassed() = false, want true when one pass is present")
	}
	if result.NoneFailed() {
		t.Fatal("NoneFailed() = true, want false when fail is present")
	}
}

func TestEvaluationResultMajorityPassedRequiresExplicitPassMajority(t *testing.T) {
	result := &EvaluationResult{Judgments: map[string]*JudgmentResult{
		"pass":  {Verdict: VerdictPass},
		"maybe": {Verdict: VerdictMaybe},
	}}

	if result.MajorityPassed() {
		t.Fatal("MajorityPassed() = true, want false when only half of judgments explicitly pass")
	}
}

func TestJudgeGroupFiltersNilJudgmentResults(t *testing.T) {
	group := NewJudgeGroup(&recordingEvalLLM{}, []Evaluator{
		nilJudgmentEvaluator{name: "empty"},
		fixedJudgmentEvaluator{name: "pass", result: &JudgmentResult{Verdict: VerdictPass}},
	})

	result, err := group.Evaluate(context.Background(), llm.NewChatContext(), nil)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if _, ok := result.Judgments["empty"]; ok {
		t.Fatalf("Judgments contains nil result for failed/non-judgment evaluator: %#v", result.Judgments["empty"])
	}
	if got := len(result.Judgments); got != 1 {
		t.Fatalf("len(Judgments) = %d, want 1", got)
	}
	if !result.Judgments["pass"].Passed() {
		t.Fatalf("Judgments[pass] = %#v, want pass verdict", result.Judgments["pass"])
	}
}

func TestFormatChatCtxAgentConfigUpdateMatchesReferenceShape(t *testing.T) {
	instructions := "be concise"
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.AgentConfigUpdate{
		Instructions: &instructions,
		ToolsAdded:   []string{"lookup"},
		ToolsRemoved: []string{"search"},
	})

	got := formatChatCtx(chatCtx)
	want := "[agent config: instructions='be concise', tools_added=['lookup'], tools_removed=['search']]"
	if got != want {
		t.Fatalf("formatChatCtx() = %q, want %q", got, want)
	}
}

func TestJudgeHandoffShortCircuitCarriesInstructions(t *testing.T) {
	chatCtx := llm.NewChatContext()
	judge := NewJudge("handoff", "only evaluate actual handoffs", nil)

	result, err := judge.Evaluate(context.Background(), chatCtx, nil, &recordingEvalLLM{})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !result.Passed() {
		t.Fatalf("Verdict = %q, want pass", result.Verdict)
	}
	if result.Instructions != "only evaluate actual handoffs" {
		t.Fatalf("Instructions = %q, want handoff instructions", result.Instructions)
	}
}

func TestJudgeEvaluateCarriesResolvedInstructions(t *testing.T) {
	latestInstructions := "resolve the latest customer request"
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.AgentConfigUpdate{Instructions: &latestInstructions})
	evaluator := &recordingEvalLLM{arguments: `{"verdict":"maybe","reasoning":"needs review"}`}
	judge := TaskCompletionJudge(nil)

	result, err := judge.Evaluate(context.Background(), chatCtx, nil, evaluator)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !result.Uncertain() {
		t.Fatalf("Verdict = %q, want maybe", result.Verdict)
	}
	if result.Reasoning != "needs review" {
		t.Fatalf("Reasoning = %q, want LLM reasoning", result.Reasoning)
	}
	if result.Instructions != latestInstructions {
		t.Fatalf("Instructions = %q, want latest task instructions", result.Instructions)
	}
	if evaluator.prompt == "" || !containsAll(evaluator.prompt, []string{"Criteria: " + latestInstructions, "Evaluate if the conversation meets the criteria."}) {
		t.Fatalf("prompt = %q, want resolved criteria and evaluation request", evaluator.prompt)
	}
}

func containsAll(s string, parts []string) bool {
	for _, part := range parts {
		if !contains(s, part) {
			return false
		}
	}
	return true
}

func contains(s string, part string) bool {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return true
		}
	}
	return part == ""
}

type recordingEvalLLM struct {
	arguments string
	prompt    string
}

func (l *recordingEvalLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	if len(chatCtx.Items) > 1 {
		if msg, ok := chatCtx.Items[1].(*llm.ChatMessage); ok {
			l.prompt = msg.TextContent()
		}
	}
	arguments := l.arguments
	if arguments == "" {
		arguments = `{"verdict":"pass","reasoning":"ok"}`
	}
	return &recordingEvalStream{arguments: arguments}, nil
}

type recordingEvalStream struct {
	arguments string
	sent      bool
}

func (s *recordingEvalStream) Next() (*llm.ChatChunk, error) {
	if s.sent {
		return nil, errors.New("done")
	}
	s.sent = true
	return &llm.ChatChunk{Delta: &llm.ChoiceDelta{ToolCalls: []llm.FunctionToolCall{{
		Name:      "submit_verdict",
		Arguments: s.arguments,
	}}}}, nil
}

func (s *recordingEvalStream) Close() error { return nil }

type nilJudgmentEvaluator struct {
	name string
}

func (e nilJudgmentEvaluator) Name() string { return e.name }

func (e nilJudgmentEvaluator) Evaluate(context.Context, *llm.ChatContext, *llm.ChatContext, llm.LLM) (*JudgmentResult, error) {
	return nil, nil
}

type fixedJudgmentEvaluator struct {
	name   string
	result *JudgmentResult
}

func (e fixedJudgmentEvaluator) Name() string { return e.name }

func (e fixedJudgmentEvaluator) Evaluate(context.Context, *llm.ChatContext, *llm.ChatContext, llm.LLM) (*JudgmentResult, error) {
	return e.result, nil
}
