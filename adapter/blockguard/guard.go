package blockguard

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/logger"
)

// GuardrailAdvisor provides advice to the main agent based on conversation monitoring.
type GuardrailAdvisor struct {
	llm             llm.LLM
	instructions    string
	chatCtx         *llm.ChatContext
	maxInterventions int
	interventions   int
	cooldown        time.Duration
	lastIntervention time.Time
	
	mu sync.Mutex
}

func NewGuardrailAdvisor(observerLLM llm.LLM, instructions string) *GuardrailAdvisor {
	return &GuardrailAdvisor{
		llm:             observerLLM,
		instructions:    instructions,
		maxInterventions: 3,
		cooldown:        30 * time.Second,
	}
}

// OnChatUpdate is called whenever the conversation context changes.
// It runs the observer LLM in the background to see if an intervention is needed.
func (g *GuardrailAdvisor) OnChatUpdate(ctx context.Context, chatCtx *llm.ChatContext) {
	g.mu.Lock()
	if g.interventions >= g.maxInterventions {
		g.mu.Unlock()
		return
	}
	if time.Since(g.lastIntervention) < g.cooldown {
		g.mu.Unlock()
		return
	}
	g.mu.Unlock()

	// Run evaluation in background
	go g.evaluate(ctx, chatCtx)
}

func (g *GuardrailAdvisor) evaluate(ctx context.Context, chatCtx *llm.ChatContext) {
	// 1. Prepare evaluation context
	evalCtx := llm.NewChatContext()
	evalCtx.Append(&llm.ChatMessage{
		Role:    llm.ChatRoleSystem,
		Content: []llm.ChatContent{{Text: g.instructions}},
	})
	
	// Copy relevant history
	for _, item := range chatCtx.Items {
		evalCtx.Append(item)
	}
	
	evalCtx.Append(&llm.ChatMessage{
		Role:    llm.ChatRoleSystem,
		Content: []llm.ChatContent{{Text: "Based on the above conversation, do you have any advice for the agent? If so, provide it as a short sentence. If not, respond with 'NONE'."}},
	})

	// 2. Call Observer LLM
	stream, err := g.llm.Chat(ctx, evalCtx)
	if err != nil {
		logger.Logger.Errorw("Guardrail evaluation failed", err)
		return
	}
	defer stream.Close()

	var advice string
	for {
		chunk, err := stream.Next()
		if err != nil {
			break
		}
		if chunk.Delta != nil {
			advice += chunk.Delta.Content
		}
	}

	if advice == "NONE" || advice == "" {
		return
	}

	// 3. Inject Advice
	g.mu.Lock()
	g.interventions++
	g.lastIntervention = time.Now()
	g.mu.Unlock()

	logger.Logger.Infow("Guardrail injecting advice", "advice", advice)
	chatCtx.Append(&llm.ChatMessage{
		Role:    llm.ChatRoleSystem,
		Content: []llm.ChatContent{{Text: fmt.Sprintf("[GUARDRAIL ADVISOR]: %s", advice)}},
	})
}
