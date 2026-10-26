package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// TracingOptions controls content sent to a custom trace provider. Project
// redaction always wins. Nil AllowPII uses LIVEKIT_TELEMETRY_ALLOW_PII (default true).
type TracingOptions struct{ AllowPII *bool }

type contentPolicy struct{ stripPII, stripGenAI bool }

func newContentPolicy(options TracingOptions, redacted, cloud bool) contentPolicy {
	allow := true
	if raw := os.Getenv("LIVEKIT_TELEMETRY_ALLOW_PII"); raw != "" {
		allow = !falseSetting(raw)
	}
	if options.AllowPII != nil {
		allow = *options.AllowPII
	}
	return contentPolicy{stripPII: redacted || (!cloud && !allow), stripGenAI: falseSetting(os.Getenv("OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT"))}
}

func falseSetting(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

func genAIContent(key string) bool {
	switch key {
	case AttrGenAIInputMessages, AttrGenAIOutputMessages, AttrGenAISystemInstructions, AttrGenAIToolDefinitions,
		"gen_ai.tool.call.arguments", "gen_ai.tool.call.result", "gen_ai.tool.description", "gen_ai.retrieval.query.text", "gen_ai.retrieval.documents", "gen_ai.memory.query.text", "gen_ai.memory.records", "gen_ai.evaluation.explanation":
		return true
	}
	return strings.HasPrefix(key, "gen_ai.prompt.variable.")
}

func (p contentPolicy) attributes(attrs []attribute.KeyValue) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		key := string(a.Key)
		if p.stripPII && strings.Contains("."+key+".", ".pii.") || (p.stripPII || p.stripGenAI) && genAIContent(key) {
			continue
		}
		if p.stripPII {
			if key == "exception.stacktrace" {
				continue
			}
			if key == "exception.message" {
				a = attribute.String(key, "exception details redacted")
			}
		}
		out = append(out, a)
	}
	return out
}

type policyTracer struct {
	trace.Tracer
	options         TracingOptions
	metadata        []attribute.KeyValue
	redacted, cloud bool
}

func (t policyTracer) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	policy := newContentPolicy(t.options, t.redacted || RedactionEnabled(ctx), t.cloud)
	opts = append(opts, trace.WithAttributes(t.metadata...))
	config := trace.NewSpanStartConfig(opts...)
	filtered := []trace.SpanStartOption{trace.WithAttributes(policy.attributes(config.Attributes())...), trace.WithTimestamp(config.Timestamp()), trace.WithSpanKind(config.SpanKind())}
	if config.NewRoot() {
		filtered = append(filtered, trace.WithNewRoot())
	}
	for _, link := range config.Links() {
		link.Attributes = policy.attributes(link.Attributes)
		filtered = append(filtered, trace.WithLinks(link))
	}
	ctx, span := t.Tracer.Start(ctx, name, filtered...)
	wrapped := policySpan{Span: span, policy: policy}
	return trace.ContextWithSpan(ctx, wrapped), wrapped
}

type policySpan struct {
	trace.Span
	policy contentPolicy
}

// End uses current wall time, matching span starts and timestamped audio events.
// The SDK's default reconstructs wall time from each span's monotonic start;
// clock adjustments can then make a later-ending parent appear to end first.
func (s policySpan) End(opts ...trace.SpanEndOption) {
	config := trace.NewSpanEndConfig(opts...)
	if config.Timestamp().IsZero() {
		opts = append(opts, trace.WithTimestamp(time.Now()))
	}
	s.Span.End(opts...)
}

func (s policySpan) SetAttributes(attrs ...attribute.KeyValue) {
	s.Span.SetAttributes(s.policy.attributes(attrs)...)
}
func (s policySpan) AddEvent(name string, opts ...trace.EventOption) {
	if s.policy.stripPII || s.policy.stripGenAI {
		switch name {
		case EventGenAISystemMessage, EventGenAIUserMessage, EventGenAIAssistantMessage, EventGenAIToolMessage, EventGenAIChoice, "gen_ai.client.inference.operation.details":
			return
		}
	}
	config := trace.NewEventConfig(opts...)
	s.Span.AddEvent(name, trace.WithTimestamp(config.Timestamp()), trace.WithAttributes(s.policy.attributes(config.Attributes())...))
}
func (s policySpan) RecordError(err error, opts ...trace.EventOption) {
	if err == nil {
		return
	}
	if !s.policy.stripPII {
		s.Span.RecordError(err, opts...)
		return
	}
	config := trace.NewEventConfig(opts...)
	attrs := append(s.policy.attributes(config.Attributes()), attribute.String("exception.type", fmt.Sprintf("%T", err)))
	s.Span.RecordError(errors.New("exception details redacted"), trace.WithTimestamp(config.Timestamp()), trace.WithAttributes(attrs...))
}
func (s policySpan) SetStatus(code codes.Code, description string) {
	if s.policy.stripPII && code == codes.Error && description != "" {
		description = "exception details redacted"
	}
	s.Span.SetStatus(code, description)
}
