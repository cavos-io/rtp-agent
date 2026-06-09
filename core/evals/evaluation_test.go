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

func TestFormatChatCtxInterruptedMessageMatchesReferenceShape(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{
		Role:        llm.ChatRoleAssistant,
		Content:     []llm.ChatContent{{Text: "I can help with that"}},
		Interrupted: true,
	})

	got := formatChatCtx(chatCtx)
	want := "assistant: I can help with that [interrupted]"
	if got != want {
		t.Fatalf("formatChatCtx() = %q, want %q", got, want)
	}
}

func TestHandoffJudgeShortCircuitMatchesReferenceResult(t *testing.T) {
	chatCtx := llm.NewChatContext()
	judge := NewJudge("handoff", "only evaluate actual handoffs", nil)

	result, err := judge.Evaluate(context.Background(), chatCtx, nil, &recordingEvalLLM{})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !result.Passed() {
		t.Fatalf("Verdict = %q, want pass", result.Verdict)
	}
	if result.Reasoning != "No agent handoffs occurred in this conversation." {
		t.Fatalf("Reasoning = %q, want reference no-handoff reasoning", result.Reasoning)
	}
	if result.Instructions != "" {
		t.Fatalf("Instructions = %q, want empty instructions for no-handoff short-circuit", result.Instructions)
	}
}

func TestHandoffJudgeEvaluateMatchesReferencePromptShape(t *testing.T) {
	oldAgent := "triage"
	chatCtx := llm.NewChatContext()
	chatCtx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleUser, Text: "need billing help"})
	chatCtx.Append(&llm.AgentHandoff{OldAgentID: &oldAgent, NewAgentID: "billing"})
	evaluator := &recordingEvalLLM{arguments: `{"verdict":"pass","reasoning":"context preserved"}`}
	judge := HandoffJudge(nil)
	handoffCriteria := "Evaluate if the conversation maintained context across agent handoffs. " +
		"Handoffs can be silent or explicit, either is acceptable. " +
		"Remembered info (names, details, requests) = pass. " +
		"Break in continuity, repeated questions, context lost = fail."

	result, err := judge.Evaluate(context.Background(), chatCtx, nil, evaluator)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !result.Passed() {
		t.Fatalf("Verdict = %q, want pass", result.Verdict)
	}
	if result.Reasoning != "context preserved" {
		t.Fatalf("Reasoning = %q, want LLM reasoning", result.Reasoning)
	}
	if result.Instructions != handoffCriteria {
		t.Fatalf("Instructions = %q, want handoff criteria", result.Instructions)
	}
	if !containsAll(evaluator.prompt, []string{
		handoffCriteria,
		"Conversation:\nuser: need billing help\n[agent handoff: triage -> billing]",
	}) {
		t.Fatalf("prompt = %q, want reference handoff criteria and conversation", evaluator.prompt)
	}
	if contains(evaluator.prompt, "Criteria: ") {
		t.Fatalf("prompt = %q, want reference handoff prompt without generic Criteria prefix", evaluator.prompt)
	}
	if contains(evaluator.prompt, "Evaluate if the conversation meets the criteria.") {
		t.Fatalf("prompt = %q, want reference handoff prompt without generic evaluation suffix", evaluator.prompt)
	}
}

func TestTaskCompletionJudgeEvaluateMatchesReferencePromptShape(t *testing.T) {
	latestInstructions := "resolve the latest customer request"
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.AgentConfigUpdate{Instructions: &latestInstructions})
	evaluator := &recordingEvalLLM{arguments: `{"verdict":"maybe","reasoning":"needs review"}`}
	judge := TaskCompletionJudge(nil)
	taskCompletionCriteria := "Evaluate if the agent completed its goal based on its instructions. " +
		"Task completed, appropriately handed off, or correctly declined = pass. " +
		"User's need ignored, no resolution, gave up without handoff = fail."

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
	if result.Instructions != taskCompletionCriteria {
		t.Fatalf("Instructions = %q, want task completion criteria", result.Instructions)
	}
	if !containsAll(evaluator.prompt, []string{
		taskCompletionCriteria,
		"Agent Instructions:\n" + latestInstructions,
		"Conversation:\n[agent config: instructions='resolve the latest customer request']",
	}) {
		t.Fatalf("prompt = %q, want reference task completion criteria, agent instructions, and conversation", evaluator.prompt)
	}
	if contains(evaluator.prompt, "Criteria: "+latestInstructions) {
		t.Fatalf("prompt = %q, want latest instructions under Agent Instructions, not generic Criteria", evaluator.prompt)
	}
	if contains(evaluator.prompt, "Evaluate if the conversation meets the criteria.") {
		t.Fatalf("prompt = %q, want task_completion reference prompt without generic evaluation suffix", evaluator.prompt)
	}
}

func TestToolUseJudgeInstructionsMatchReferenceNoToolNeededPass(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleUser, Text: "hello"})
	evaluator := &recordingEvalLLM{arguments: `{"verdict":"pass","reasoning":"no tool needed"}`}
	judge := ToolUseJudge(nil)

	result, err := judge.Evaluate(context.Background(), chatCtx, nil, evaluator)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !result.Passed() {
		t.Fatalf("Verdict = %q, want pass", result.Verdict)
	}
	if !contains(result.Instructions, "Pass if no tools were needed for the conversation") {
		t.Fatalf("Instructions = %q, want reference no-tools-needed pass clause", result.Instructions)
	}
	if !contains(evaluator.prompt, "Pass if no tools were needed for the conversation") {
		t.Fatalf("prompt = %q, want reference no-tools-needed pass clause", evaluator.prompt)
	}
}

func TestJudgeMissingLLMErrorMatchesReferenceGuidance(t *testing.T) {
	oldAgent := "triage"
	cases := []struct {
		name    string
		judge   Evaluator
		chatCtx *llm.ChatContext
		want    string
	}{
		{
			name:    "generic",
			judge:   AccuracyJudge(nil),
			chatCtx: llm.NewChatContext(),
			want:    "No LLM provided for judge 'accuracy'. Pass llm to JudgeGroup or to the judge constructor.",
		},
		{
			name:    "task_completion",
			judge:   TaskCompletionJudge(nil),
			chatCtx: llm.NewChatContext(),
			want:    "No LLM provided for judge 'task_completion'. Pass llm to JudgeGroup or to the judge constructor.",
		},
		{
			name:  "handoff",
			judge: HandoffJudge(nil),
			chatCtx: func() *llm.ChatContext {
				chatCtx := llm.NewChatContext()
				chatCtx.Append(&llm.AgentHandoff{OldAgentID: &oldAgent, NewAgentID: "billing"})
				return chatCtx
			}(),
			want: "No LLM provided for judge 'handoff'. Pass llm to JudgeGroup or to the judge constructor.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.judge.Evaluate(context.Background(), tc.chatCtx, nil, nil)
			if err == nil {
				t.Fatal("Evaluate() error = nil, want reference missing-LLM error")
			}
			if err.Error() != tc.want {
				t.Fatalf("Evaluate() error = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}

func TestJudgeEvaluateReferenceExcludesInstructionMessages(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleUser, Text: "compare this"})
	reference := llm.NewChatContext()
	reference.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleSystem, Text: "hidden rubric"})
	reference.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleUser, Text: "visible reference"})
	evaluator := &recordingEvalLLM{}
	judge := NewJudge("reference", "compare the answer", nil)

	if _, err := judge.Evaluate(context.Background(), chatCtx, reference, evaluator); err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if contains(evaluator.prompt, "system: hidden rubric") {
		t.Fatalf("prompt included reference instruction message: %q", evaluator.prompt)
	}
	if !contains(evaluator.prompt, "user: visible reference") {
		t.Fatalf("prompt = %q, want visible reference message", evaluator.prompt)
	}
}

func TestEvaluateWithLLMPassesReferenceTemperatureForNonGPT5Models(t *testing.T) {
	evaluator := &recordingEvalLLM{model: "gpt-4o-mini"}

	if _, err := evaluateWithLLM(context.Background(), evaluator, "judge this"); err != nil {
		t.Fatalf("evaluateWithLLM() error = %v", err)
	}

	if evaluator.options.ExtraParams == nil {
		t.Fatal("ExtraParams = nil, want temperature parameter")
	}
	if got := evaluator.options.ExtraParams["temperature"]; got != 0.0 {
		t.Fatalf("ExtraParams[temperature] = %#v, want 0.0", got)
	}
}

func TestEvaluateWithLLMOmitsReferenceTemperatureForGPT5Models(t *testing.T) {
	evaluator := &recordingEvalLLM{model: "openai/gpt-5-mini"}

	if _, err := evaluateWithLLM(context.Background(), evaluator, "judge this"); err != nil {
		t.Fatalf("evaluateWithLLM() error = %v", err)
	}

	if _, ok := evaluator.options.ExtraParams["temperature"]; ok {
		t.Fatalf("ExtraParams = %#v, want no temperature for gpt-5 models", evaluator.options.ExtraParams)
	}
}

func TestEvaluateWithLLMUsesReferenceSubmitVerdictToolChoice(t *testing.T) {
	evaluator := &recordingEvalLLM{}

	if _, err := evaluateWithLLM(context.Background(), evaluator, "judge this"); err != nil {
		t.Fatalf("evaluateWithLLM() error = %v", err)
	}

	choice, ok := evaluator.options.ToolChoice.(map[string]any)
	if !ok {
		t.Fatalf("ToolChoice = %#v, want reference function tool_choice map", evaluator.options.ToolChoice)
	}
	if choice["type"] != "function" {
		t.Fatalf("ToolChoice[type] = %#v, want function", choice["type"])
	}
	function, ok := choice["function"].(map[string]any)
	if !ok {
		t.Fatalf("ToolChoice[function] = %#v, want function map", choice["function"])
	}
	if function["name"] != "submit_verdict" {
		t.Fatalf("ToolChoice[function][name] = %#v, want submit_verdict", function["name"])
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
	model     string
	options   llm.ChatOptions
}

func (l *recordingEvalLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	l.options = llm.ChatOptions{}
	for _, opt := range opts {
		opt(&l.options)
	}
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

func (l *recordingEvalLLM) Model() string {
	if l.model != "" {
		return l.model
	}
	return "test-model"
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
