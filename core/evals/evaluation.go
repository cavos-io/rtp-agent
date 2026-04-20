package evals

import (
	"context"
	"sync"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/logger"
)

type Verdict string

const (
	VerdictPass  Verdict = "pass"
	VerdictFail  Verdict = "fail"
	VerdictMaybe Verdict = "maybe"
)

type JudgmentResult struct {
	Verdict   Verdict
	Reasoning string
}

func (j *JudgmentResult) Passed() bool {
	return j.Verdict == VerdictPass
}

type Evaluator interface {
	Name() string
	Evaluate(ctx context.Context, chatCtx *llm.ChatContext, reference *llm.ChatContext, evaluatorLLM llm.LLM) (*JudgmentResult, error)
}

type EvaluationResult struct {
	Judgments map[string]*JudgmentResult
}

func (r *EvaluationResult) Score() float64 {
	if len(r.Judgments) == 0 {
		return 0.0
	}
	var total float64
	for _, j := range r.Judgments {
		if j.Passed() {
			total += 1.0
		} else if j.Verdict == VerdictMaybe {
			total += 0.5
		}
	}
	return total / float64(len(r.Judgments))
}

func (r *EvaluationResult) AllPassed() bool {
	for _, j := range r.Judgments {
		if !j.Passed() {
			return false
		}
	}
	return true
}

func (r *EvaluationResult) AnyPassed() bool {
	for _, j := range r.Judgments {
		if j.Passed() {
			return true
		}
	}
	return false
}

func (r *EvaluationResult) MajorityPassed() bool {
	if len(r.Judgments) == 0 {
		return true
	}
	return r.Score() > 0.5
}

func (r *EvaluationResult) NoneFailed() bool {
	for _, j := range r.Judgments {
		if j.Verdict == VerdictFail {
			return false
		}
	}
	return true
}

type JudgeGroup struct {
	LLM    llm.LLM
	Judges []Evaluator
}

func NewJudgeGroup(llm llm.LLM, judges []Evaluator) *JudgeGroup {
	return &JudgeGroup{
		LLM:    llm,
		Judges: judges,
	}
}

func (g *JudgeGroup) Evaluate(ctx context.Context, chatCtx *llm.ChatContext, reference *llm.ChatContext) (*EvaluationResult, error) {
	var wg sync.WaitGroup
	var mu sync.Mutex

	result := &EvaluationResult{
		Judgments: make(map[string]*JudgmentResult),
	}

	for _, j := range g.Judges {
		wg.Add(1)
		go func(judge Evaluator) {
			defer wg.Done()

			res, err := judge.Evaluate(ctx, chatCtx, reference, g.LLM)
			if err != nil {
				logger.Logger.Warnw("Judge failed", err, "name", judge.Name())
				return
			}

			mu.Lock()
			result.Judgments[judge.Name()] = res
			mu.Unlock()
		}(j)
	}

	wg.Wait()

	// Automatically tag if possible (parity with Python)
	// This would require access to JobContext
	// In Go, we can check if rc is in ctx

	return result, nil
}

