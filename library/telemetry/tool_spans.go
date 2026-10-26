package telemetry

import (
	"context"
	"encoding/json"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type agentNameKey struct{}

// ContextWithAgentName binds immutable attribution to an operation, never globally.
func ContextWithAgentName(ctx context.Context, name string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, agentNameKey{}, name)
}

// StartToolSpan is shared by regular tools and the runtime's built-in task tools.
func StartToolSpan(ctx context.Context, id, name, args, description string) (context.Context, trace.Span) {
	attrs := []attribute.KeyValue{attribute.String(AttrFunctionToolID, id), attribute.String(AttrFunctionToolName, name), attribute.String(AttrFunctionToolArgs, args), attribute.String(AttrGenAIOperationName, "execute_tool"), attribute.String("gen_ai.tool.name", name), attribute.String("gen_ai.tool.call.id", id), attribute.String("gen_ai.tool.type", "function"), attribute.String("gen_ai.tool.call.arguments", args)}
	if description != "" {
		attrs = append(attrs, attribute.String("gen_ai.tool.description", description))
	}
	if ctx != nil {
		if agent, ok := ctx.Value(agentNameKey{}).(string); ok && agent != "" {
			attrs = append(attrs, attribute.String("gen_ai.agent.name", agent))
		}
	}
	return StartSpan(ctx, "function_tool", trace.WithAttributes(attrs...))
}

func RecordToolResult(span trace.Span, output string, isError bool) {
	span.SetAttributes(attribute.String(AttrFunctionToolOutput, output), attribute.Bool(AttrFunctionToolIsError, isError))
	if isError {
		span.SetAttributes(attribute.String(AttrErrorType, "tool_error"))
		return
	}
	var value any = output
	_ = json.Unmarshal([]byte(output), &value)
	encoded, err := json.Marshal(value)
	if err == nil {
		span.SetAttributes(attribute.String("gen_ai.tool.call.result", string(encoded)))
	}
}
