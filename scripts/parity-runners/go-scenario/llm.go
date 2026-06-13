package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"

	audiomodel "github.com/cavos-io/rtp-agent/core/audio/model"
	lkllm "github.com/cavos-io/rtp-agent/core/llm"
)

func runLLMAPIConnectOptions(input json.RawMessage) (any, error) {
	var payload struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Action == "" {
		payload.Action = "defaults"
	}
	switch payload.Action {
	case "defaults":
		options := lkllm.DefaultAPIConnectOptions()
		err := options.Validate()
		return map[string]any{
			"contract": "llm-api-connect-options",
			"events": []map[string]any{
				{
					"name":                   "defaults",
					"max_retry":              options.MaxRetry,
					"retry_interval_seconds": options.RetryInterval.Seconds(),
					"timeout_seconds":        options.Timeout.Seconds(),
					"validate_error":         err != nil,
					"error_class":            errorClass(err),
				},
			},
		}, nil
	case "validation":
		tests := []struct {
			name    string
			options lkllm.APIConnectOptions
		}{
			{name: "max_retry", options: lkllm.APIConnectOptions{MaxRetry: -1}},
			{name: "retry_interval", options: lkllm.APIConnectOptions{RetryInterval: -time.Nanosecond}},
			{name: "timeout", options: lkllm.APIConnectOptions{Timeout: -time.Nanosecond}},
		}
		events := make([]map[string]any, 0, len(tests))
		for _, test := range tests {
			err := test.options.Validate()
			message := ""
			if err != nil {
				message = err.Error()
			}
			events = append(events, map[string]any{
				"name":        "validation",
				"field":       test.name,
				"error":       err != nil,
				"error_class": errorClass(err),
				"message":     message,
			})
		}
		return map[string]any{"contract": "llm-api-connect-options", "events": events}, nil
	case "interval":
		options := lkllm.APIConnectOptions{RetryInterval: 3 * time.Second}
		return map[string]any{
			"contract": "llm-api-connect-options",
			"events": []map[string]any{
				{
					"name":        "interval",
					"retry":       0,
					"interval_ms": int(options.IntervalForRetry(0) / time.Millisecond),
				},
				{
					"name":        "interval",
					"retry":       1,
					"interval_ms": int(options.IntervalForRetry(1) / time.Millisecond),
				},
			},
		}, nil
	case "effective_validation":
		options := &lkllm.ChatOptions{
			ConnectOptions: &lkllm.APIConnectOptions{Timeout: -time.Nanosecond},
		}
		_, err := options.EffectiveConnectOptions()
		message := ""
		if err != nil {
			message = err.Error()
		}
		return map[string]any{
			"contract": "llm-api-connect-options",
			"events": []map[string]any{
				{
					"name":        "effective_validation",
					"field":       "timeout",
					"error":       err != nil,
					"error_class": errorClass(err),
					"message":     message,
				},
			},
		}, nil
	case "explicit_connect_options":
		connectOptions := lkllm.APIConnectOptions{
			MaxRetry:      1,
			RetryInterval: 50 * time.Millisecond,
			Timeout:       time.Second,
		}
		options := &lkllm.ChatOptions{}
		lkllm.WithConnectOptions(connectOptions)(options)
		if options.ConnectOptions == nil {
			return nil, errors.New("connect options were not stored")
		}
		return map[string]any{
			"contract": "llm-api-connect-options",
			"events": []map[string]any{
				{
					"name":              "explicit_connect_options",
					"max_retry":         options.ConnectOptions.MaxRetry,
					"retry_interval_ms": int(options.ConnectOptions.RetryInterval / time.Millisecond),
					"timeout_ms":        int(options.ConnectOptions.Timeout / time.Millisecond),
				},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported api connect options action %q", payload.Action)
	}
}

func runLLMAPIErrors(input json.RawMessage) (any, error) {
	var payload struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Action == "" {
		payload.Action = "status_retryability"
	}
	switch payload.Action {
	case "status_retryability":
		statuses := []int{400, 401, 408, 429, 499, 500}
		events := make([]map[string]any, 0, len(statuses))
		for _, status := range statuses {
			err := lkllm.NewAPIStatusError("request failed", status, "req_123", nil)
			events = append(events, map[string]any{
				"name":        "status_retryability",
				"status":      err.StatusCode,
				"request_id":  err.RequestID,
				"retryable":   err.Retryable,
				"message":     err.Message,
				"body_is_nil": err.Body == nil,
			})
		}
		return map[string]any{"contract": "llm-api-errors", "events": events}, nil
	case "status_retryable_override":
		tests := []struct {
			name      string
			status    int
			retryable bool
		}{
			{name: "client_forces_false", status: 400, retryable: true},
			{name: "transient_keeps_true", status: 429, retryable: true},
			{name: "server_keeps_false", status: 500, retryable: false},
		}
		events := make([]map[string]any, 0, len(tests))
		for _, test := range tests {
			err := lkllm.NewAPIStatusErrorWithRetryable(test.name, test.status, fmt.Sprintf("req_%d", test.status), nil, test.retryable)
			events = append(events, map[string]any{
				"name":        "status_retryable_override",
				"case":        test.name,
				"status":      err.StatusCode,
				"request_id":  err.RequestID,
				"retryable":   err.Retryable,
				"message":     err.Message,
				"body_is_nil": err.Body == nil,
			})
		}
		return map[string]any{"contract": "llm-api-errors", "events": events}, nil
	case "status_string":
		err := lkllm.NewAPIStatusError("quota exceeded", 429, "req_123", map[string]any{"type": "rate_limit"})
		return map[string]any{
			"contract": "llm-api-errors",
			"events": []map[string]any{
				{
					"name":       "status_string",
					"error":      err.Error(),
					"message":    err.Message,
					"status":     err.StatusCode,
					"request_id": err.RequestID,
					"retryable":  err.Retryable,
				},
			},
		}, nil
	case "status_string_nested_body":
		err := lkllm.NewAPIStatusError("quota exceeded", 429, "req_123", map[string]any{
			"errors": []any{"rate", "quota"},
			"meta":   map[string]any{"retry": false},
		})
		return map[string]any{
			"contract": "llm-api-errors",
			"events": []map[string]any{
				{
					"name":       "status_string_nested_body",
					"error":      err.Error(),
					"message":    err.Message,
					"status":     err.StatusCode,
					"request_id": err.RequestID,
					"retryable":  err.Retryable,
				},
			},
		}, nil
	case "status_string_quotes":
		err := lkllm.NewAPIStatusError("can't retry", 400, "req_400", map[string]any{"detail": "can't retry"})
		return map[string]any{
			"contract": "llm-api-errors",
			"events": []map[string]any{
				{
					"name":       "status_string_quotes",
					"error":      err.Error(),
					"message":    err.Message,
					"status":     err.StatusCode,
					"request_id": err.RequestID,
					"retryable":  err.Retryable,
				},
			},
		}, nil
	case "status_string_floats":
		err := lkllm.NewAPIStatusError("quota exceeded", 429, "req_123", map[string]any{
			"ratio": 1.0,
			"wait":  1.25,
		})
		return map[string]any{
			"contract": "llm-api-errors",
			"events": []map[string]any{
				{
					"name":       "status_string_floats",
					"error":      err.Error(),
					"message":    err.Message,
					"status":     err.StatusCode,
					"request_id": err.RequestID,
					"retryable":  err.Retryable,
				},
			},
		}, nil
	case "base_error":
		err := lkllm.NewAPIError("provider failed", map[string]any{"code": "overloaded"}, true)
		body, _ := err.Body.(map[string]any)
		return map[string]any{
			"contract": "llm-api-errors",
			"events": []map[string]any{
				{
					"name":        "base_error",
					"message":     err.Message,
					"error":       err.Error(),
					"retryable":   err.Retryable,
					"body_is_nil": err.Body == nil,
					"body_code":   body["code"],
				},
			},
		}, nil
	case "http_message":
		err := lkllm.CreateAPIErrorFromHTTP("quota exceeded", 429, "req_123", map[string]any{"type": "rate_limit"})
		return map[string]any{
			"contract": "llm-api-errors",
			"events": []map[string]any{
				{
					"name":        "http_message",
					"message":     err.Message,
					"status":      err.StatusCode,
					"request_id":  err.RequestID,
					"retryable":   err.Retryable,
					"body_is_nil": err.Body == nil,
				},
			},
		}, nil
	case "http_reason":
		tests := []struct {
			name    string
			message string
			status  int
		}{
			{name: "empty", message: "", status: 404},
			{name: "same_as_reason", message: "Not Found", status: 404},
			{name: "unknown", message: "", status: 599},
		}
		events := make([]map[string]any, 0, len(tests))
		for _, test := range tests {
			err := lkllm.CreateAPIErrorFromHTTP(test.message, test.status, "", nil)
			events = append(events, map[string]any{
				"name":        "http_reason",
				"case":        test.name,
				"message":     err.Message,
				"status":      err.StatusCode,
				"retryable":   err.Retryable,
				"body_is_nil": err.Body == nil,
			})
		}
		return map[string]any{"contract": "llm-api-errors", "events": events}, nil
	case "connection_timeout":
		connectionErr := lkllm.NewAPIConnectionError("")
		timeoutErr := lkllm.NewAPITimeoutError("")
		return map[string]any{
			"contract": "llm-api-errors",
			"events": []map[string]any{
				{
					"name":        "connection_error",
					"message":     connectionErr.Message,
					"retryable":   connectionErr.Retryable,
					"body_is_nil": connectionErr.Body == nil,
				},
				{
					"name":        "timeout_error",
					"message":     timeoutErr.Message,
					"retryable":   timeoutErr.Retryable,
					"body_is_nil": timeoutErr.Body == nil,
				},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported api errors action %q", payload.Action)
	}
}

func runLLMRemoteChatContext(input json.RawMessage) (any, error) {
	var payload struct {
		Action     string `json:"action"`
		LookupID   string `json:"lookup_id"`
		Operations []struct {
			Op             string  `json:"op"`
			ID             string  `json:"id"`
			Role           string  `json:"role"`
			Text           string  `json:"text"`
			PreviousItemID *string `json:"previous_item_id"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Action == "" {
		payload.Action = "order"
	}
	if payload.Action == "errors" {
		return runLLMRemoteChatContextErrors()
	}
	if payload.Action != "order" {
		return nil, fmt.Errorf("unsupported remote chat context action %q", payload.Action)
	}

	ctx := lkllm.NewRemoteChatContext()
	for _, operation := range payload.Operations {
		switch operation.Op {
		case "insert":
			message := &lkllm.ChatMessage{
				ID:      operation.ID,
				Role:    lkllm.ChatRole(operation.Role),
				Content: []lkllm.ChatContent{{Text: operation.Text}},
			}
			if err := ctx.Insert(operation.PreviousItemID, message); err != nil {
				return nil, err
			}
		case "delete":
			if err := ctx.Delete(operation.ID); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported remote chat context operation %q", operation.Op)
		}
	}

	chatCtx := ctx.ToChatCtx()
	lookup := ctx.Get(payload.LookupID)
	lookupID := any(nil)
	if lookup != nil {
		lookupID = lookup.GetID()
	}
	return map[string]any{
		"contract": "llm-remote-chat-context",
		"events": []map[string]any{
			{
				"name":          "order",
				"item_ids":      chatItemIDs(chatCtx.Items),
				"lookup_id":     lookupID,
				"lookup_exists": lookup != nil,
			},
		},
	}, nil
}

func runLLMRemoteChatContextErrors() (any, error) {
	ctx := lkllm.NewRemoteChatContext()
	if err := ctx.Insert(nil, &lkllm.ChatMessage{ID: "first", Role: lkllm.ChatRoleUser, Content: []lkllm.ChatContent{{Text: "first"}}}); err != nil {
		return nil, err
	}

	events := make([]map[string]any, 0, 3)
	for _, test := range []struct {
		name string
		run  func() error
	}{
		{
			name: "duplicate",
			run: func() error {
				return ctx.Insert(nil, &lkllm.ChatMessage{ID: "first", Role: lkllm.ChatRoleUser})
			},
		},
		{
			name: "missing_previous",
			run: func() error {
				missing := "missing"
				return ctx.Insert(&missing, &lkllm.ChatMessage{ID: "second", Role: lkllm.ChatRoleAssistant})
			},
		},
		{
			name: "missing_delete",
			run: func() error {
				return ctx.Delete("missing")
			},
		},
	} {
		message := ""
		if err := test.run(); err != nil {
			message = err.Error()
		}
		events = append(events, map[string]any{
			"name":          test.name,
			"error_message": message,
		})
	}
	return map[string]any{"contract": "llm-remote-chat-context", "events": events}, nil
}

func runLLMStrictSchema(input json.RawMessage) (any, error) {
	var payload struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Action == "" {
		payload.Action = "map_value_schema"
	}
	switch payload.Action {
	case "map_value_schema":
		type request struct {
			Metadata map[string]string `json:"metadata"`
		}
		schema := lkllm.GenerateStrictJSONSchema(reflect.TypeOf(request{}))
		props, _ := schema["properties"].(map[string]interface{})
		metadata, _ := props["metadata"].(map[string]interface{})
		return map[string]any{
			"contract": "llm-strict-schema",
			"events": []map[string]any{
				{
					"name":                           "map_value_schema",
					"root_additional_properties":     schema["additionalProperties"],
					"required":                       schema["required"],
					"metadata_type":                  metadata["type"],
					"metadata_additional_properties": metadata["additionalProperties"],
				},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported strict schema action %q", payload.Action)
	}
}

func runLLMChatContext(input json.RawMessage) (any, error) {
	var payload struct {
		Action string `json:"action"`
		Kind   string `json:"kind"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Kind == "parity-scenario" {
		return runLLMChatContextDeclarative(input)
	}
	if payload.Action == "" {
		payload.Action = "empty"
	}

	switch payload.Action {
	case "empty":
		var receiver lkllm.ChatContext
		ctx := receiver.Empty()
		initialCount := len(ctx.Items)
		ctx.Append(&lkllm.ChatMessage{ID: "msg", Role: lkllm.ChatRoleUser})
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":          "empty",
					"initial_count": initialCount,
					"items_is_list": ctx.Items != nil,
					"append_count":  len(ctx.Items),
					"item_ids":      chatItemIDs(ctx.Items),
				},
			},
		}, nil
	case "copy_filters":
		ctx := lkllm.NewChatContext()
		ctx.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{ID: "system", Role: lkllm.ChatRoleSystem, Content: []lkllm.ChatContent{{Text: "instructions"}}},
			&lkllm.ChatMessage{ID: "empty", Role: lkllm.ChatRoleUser},
			&lkllm.ChatMessage{ID: "user", Role: lkllm.ChatRoleUser, Content: []lkllm.ChatContent{{Text: "hello"}}},
			&lkllm.FunctionCall{ID: "call", Name: "lookup"},
			&lkllm.FunctionCallOutput{ID: "output", Name: "lookup"},
			&lkllm.AgentHandoff{ID: "handoff", NewAgentID: "next"},
			&lkllm.AgentConfigUpdate{ID: "config"},
		}
		copied := ctx.Copy(lkllm.ChatContextCopyOptions{
			ExcludeFunctionCall: true,
			ExcludeInstructions: true,
			ExcludeEmptyMessage: true,
			ExcludeHandoff:      true,
			ExcludeConfigUpdate: true,
		})
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{"name": "copy_filters", "item_ids": chatItemIDs(copied.Items)},
			},
		}, nil
	case "merge_filters":
		base := lkllm.NewChatContext()
		base.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{ID: "existing", Role: lkllm.ChatRoleUser, Content: []lkllm.ChatContent{{Text: "hello"}}, CreatedAt: time.Unix(10, 0)},
		}
		other := lkllm.NewChatContext()
		other.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{ID: "system", Role: lkllm.ChatRoleSystem, Content: []lkllm.ChatContent{{Text: "instructions"}}, CreatedAt: time.Unix(1, 0)},
			&lkllm.FunctionCall{ID: "call", Name: "lookup", CreatedAt: time.Unix(11, 0)},
			&lkllm.FunctionCallOutput{ID: "output", Name: "lookup", CreatedAt: time.Unix(12, 0)},
			&lkllm.AgentConfigUpdate{ID: "config", CreatedAt: time.Unix(13, 0)},
			&lkllm.ChatMessage{ID: "new", Role: lkllm.ChatRoleUser, Content: []lkllm.ChatContent{{Text: "new"}}, CreatedAt: time.Unix(14, 0)},
		}
		base.Merge(other, lkllm.ChatContextMergeOptions{
			ExcludeFunctionCall: true,
			ExcludeInstructions: true,
			ExcludeConfigUpdate: true,
		})
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{"name": "merge_filters", "item_ids": chatItemIDs(base.Items)},
			},
		}, nil
	case "copy_tool_filter":
		lookup := &scenarioTool{id: "lookup", name: "lookup"}
		weather := &scenarioTool{id: "weather", name: "weather"}
		toolset := &scenarioToolset{id: "tools", tools: []lkllm.Tool{weather}}
		ctx := lkllm.NewChatContext()
		ctx.Items = []lkllm.ChatItem{
			&lkllm.FunctionCall{ID: "lookup-call", Name: "lookup"},
			&lkllm.FunctionCallOutput{ID: "lookup-output", Name: "lookup"},
			&lkllm.FunctionCall{ID: "weather-call", Name: "weather"},
			&lkllm.FunctionCallOutput{ID: "weather-output", Name: "weather"},
			&lkllm.FunctionCall{ID: "calendar-call", Name: "calendar"},
			&lkllm.FunctionCallOutput{ID: "calendar-output", Name: "calendar"},
		}
		copied := ctx.Copy(lkllm.ChatContextCopyOptions{
			Tools: []interface{}{"calendar", lookup, toolset},
		})
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{"name": "copy_tool_filter", "item_ids": chatItemIDs(copied.Items)},
			},
		}, nil
	case "tool_name_flattening":
		lookup := &scenarioTool{id: "lookup", name: "lookup"}
		weather := &scenarioTool{id: "weather", name: "weather"}
		toolset := &scenarioToolset{id: "tools", tools: []lkllm.Tool{weather}}
		ctx := lkllm.NewChatContext()
		names := ctx.GetToolNames([]interface{}{"calendar", lookup, toolset, 123})
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{"name": "tool_name_flattening", "names": names},
			},
		}, nil
	case "copy_excludes_unselected_tools":
		ctx := lkllm.NewChatContext()
		ctx.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{ID: "user", Role: lkllm.ChatRoleUser, Content: []lkllm.ChatContent{{Text: "hello"}}},
			&lkllm.FunctionCall{ID: "lookup-call", Name: "lookup"},
			&lkllm.FunctionCallOutput{ID: "lookup-output", Name: "lookup"},
			&lkllm.FunctionCall{ID: "calendar-call", Name: "calendar"},
			&lkllm.FunctionCallOutput{ID: "calendar-output", Name: "calendar"},
		}
		copied := ctx.Copy(lkllm.ChatContextCopyOptions{
			Tools: []interface{}{"lookup"},
		})
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":     "copy_excludes_unselected_tools",
					"item_ids": chatItemIDs(copied.Items),
				},
			},
		}, nil
	case "copy_shallow_items":
		item := &lkllm.ChatMessage{
			ID:        "user",
			Role:      lkllm.ChatRoleUser,
			Content:   []lkllm.ChatContent{{Text: "hello"}},
			CreatedAt: time.Unix(10, 0),
		}
		ctx := lkllm.NewChatContext()
		ctx.Items = []lkllm.ChatItem{item}
		copied := ctx.Copy()
		ctx.Items = nil
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":                     "copy_shallow_items",
					"copied_count":             len(copied.Items),
					"same_item":                copied.Items[0] == item,
					"source_count_after_clear": len(ctx.Items),
				},
			},
		}, nil
	case "readonly_view":
		ctx := lkllm.NewChatContext()
		ctx.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{ID: "user", Role: lkllm.ChatRoleUser, Content: []lkllm.ChatContent{{Text: "hello"}}},
		}
		readOnly := ctx.ReadOnly()
		mutationError := capturePanicString(func() {
			readOnly.AddMessage(lkllm.ChatMessageArgs{Role: lkllm.ChatRoleUser, Text: "blocked"})
		})
		mutable := readOnly.Copy()
		mutable.AddMessage(lkllm.ChatMessageArgs{ID: "copy", Role: lkllm.ChatRoleAssistant, Text: "ok"})
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":                      "readonly_view",
					"readonly":                  readOnly.Readonly(),
					"source_readonly":           ctx.Readonly(),
					"readonly_ids":              chatItemIDs(readOnly.Items),
					"mutation_error":            mutationError,
					"source_ids_after_mutation": chatItemIDs(ctx.Items),
					"mutable_readonly":          mutable.Readonly(),
					"mutable_ids":               chatItemIDs(mutable.Items),
					"source_ids_after_copy":     chatItemIDs(ctx.Items),
				},
			},
		}, nil
	case "merge_order_dedup":
		base := lkllm.NewChatContext()
		base.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{ID: "middle", Role: lkllm.ChatRoleUser, Content: []lkllm.ChatContent{{Text: "middle"}}, CreatedAt: time.Unix(20, 0)},
			&lkllm.ChatMessage{ID: "duplicate", Role: lkllm.ChatRoleUser, Content: []lkllm.ChatContent{{Text: "old"}}, CreatedAt: time.Unix(30, 0)},
		}
		other := lkllm.NewChatContext()
		other.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{ID: "early", Role: lkllm.ChatRoleUser, Content: []lkllm.ChatContent{{Text: "early"}}, CreatedAt: time.Unix(10, 0)},
			&lkllm.ChatMessage{ID: "duplicate", Role: lkllm.ChatRoleUser, Content: []lkllm.ChatContent{{Text: "new"}}, CreatedAt: time.Unix(25, 0)},
			&lkllm.ChatMessage{ID: "late", Role: lkllm.ChatRoleUser, Content: []lkllm.ChatContent{{Text: "late"}}, CreatedAt: time.Unix(40, 0)},
		}
		base.Merge(other)
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{"name": "merge_order_dedup", "item_ids": chatItemIDs(base.Items)},
			},
		}, nil
	case "instructions_explicit_equal_text":
		ctx := lkllm.NewChatContext()
		ctx.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{
				ID:   "system",
				Role: lkllm.ChatRoleSystem,
				Content: []lkllm.ChatContent{{
					Instructions: lkllm.NewInstructions("same instructions", "same instructions"),
				}},
			},
		}
		data := ctx.ToDict()
		items := data["items"].([]map[string]any)
		content := items[0]["content"].([]any)
		instructions := content[0].(map[string]any)
		_, textPresent := instructions["text"]
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":         "instructions_explicit_equal_text",
					"text_present": textPresent,
					"text":         instructions["text"],
				},
			},
		}, nil
	case "insert_created_at_order":
		ctx := lkllm.NewChatContext()
		ctx.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{ID: "middle", Role: lkllm.ChatRoleUser, CreatedAt: time.Unix(20, 0)},
		}
		ctx.Insert(
			&lkllm.ChatMessage{ID: "late", Role: lkllm.ChatRoleUser, CreatedAt: time.Unix(30, 0)},
			&lkllm.ChatMessage{ID: "early", Role: lkllm.ChatRoleUser, CreatedAt: time.Unix(10, 0)},
		)
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{"name": "insert_created_at_order", "item_ids": chatItemIDs(ctx.Items)},
			},
		}, nil
	case "upsert_replaces_id":
		ctx := lkllm.NewChatContext()
		ctx.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{ID: "first", Role: lkllm.ChatRoleUser, Content: []lkllm.ChatContent{{Text: "old"}}},
			&lkllm.ChatMessage{ID: "second", Role: lkllm.ChatRoleAssistant, Content: []lkllm.ChatContent{{Text: "kept"}}},
		}
		updated := &lkllm.ChatMessage{ID: "first", Role: lkllm.ChatRoleUser, Content: []lkllm.ChatContent{{Text: "new"}}}
		err := ctx.UpsertItem(updated)
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":             "upsert_replaces_id",
					"error":            err != nil,
					"item_ids":         chatItemIDs(ctx.Items),
					"first_is_updated": ctx.Items[0] == updated,
					"first_text":       ctx.Items[0].(*lkllm.ChatMessage).TextContent(),
				},
			},
		}, nil
	case "upsert_appends_missing":
		ctx := lkllm.NewChatContext()
		ctx.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{ID: "first", Role: lkllm.ChatRoleUser, Content: []lkllm.ChatContent{{Text: "old"}}},
		}
		inserted := &lkllm.FunctionCall{ID: "call", CallID: "call_lookup", Name: "lookup", Arguments: "{}"}
		err := ctx.UpsertItem(inserted)
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":            "upsert_appends_missing",
					"error":           err != nil,
					"item_ids":        chatItemIDs(ctx.Items),
					"inserted_at_end": ctx.Items[1] == inserted,
				},
			},
		}, nil
	case "upsert_rejects_type_mismatch":
		ctx := lkllm.NewChatContext()
		ctx.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{ID: "item", Role: lkllm.ChatRoleUser, Content: []lkllm.ChatContent{{Text: "old"}}},
		}
		err := ctx.UpsertItem(&lkllm.FunctionCall{ID: "item", CallID: "call_lookup", Name: "lookup"})
		message := ""
		if err != nil {
			message = err.Error()
		}
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":          "upsert_rejects_type_mismatch",
					"error":         err != nil,
					"error_message": message,
					"item_ids":      chatItemIDs(ctx.Items),
					"first_type":    ctx.Items[0].GetType(),
				},
			},
		}, nil
	case "upsert_allows_type_mismatch":
		ctx := lkllm.NewChatContext()
		ctx.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{ID: "item", Role: lkllm.ChatRoleUser, Content: []lkllm.ChatContent{{Text: "old"}}},
		}
		replacement := &lkllm.FunctionCall{ID: "item", CallID: "call_lookup", Name: "lookup"}
		err := ctx.UpsertItem(replacement, lkllm.ChatContextUpsertOptions{AllowTypeMismatch: true})
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":                 "upsert_allows_type_mismatch",
					"error":                err != nil,
					"first_is_replacement": ctx.Items[0] == replacement,
					"first_type":           ctx.Items[0].GetType(),
				},
			},
		}, nil
	case "lookup_by_id":
		ctx := lkllm.NewChatContext()
		ctx.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{ID: "first", Role: lkllm.ChatRoleUser},
			&lkllm.FunctionCall{ID: "call", CallID: "call_lookup", Name: "lookup"},
		}
		index := ctx.IndexByID("call")
		missingIndex := ctx.IndexByID("missing")
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":           "lookup_by_id",
					"get_call_found": ctx.GetByID("call") == ctx.Items[1],
					"get_missing":    nil,
					"index_call":     *index,
					"index_missing":  missingIndex,
				},
			},
		}, nil
	case "add_message_created_at_order":
		ctx := lkllm.NewChatContext()
		ctx.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{ID: "late", Role: lkllm.ChatRoleUser, Content: []lkllm.ChatContent{{Text: "late"}}, CreatedAt: time.Unix(30, 0)},
		}
		message := ctx.AddMessage(lkllm.ChatMessageArgs{
			ID:        "early",
			Role:      lkllm.ChatRoleAssistant,
			Content:   []lkllm.ChatContent{{Text: "early"}},
			CreatedAt: time.Unix(10, 0),
		})
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":         "add_message_created_at_order",
					"message_id":   message.ID,
					"role":         string(message.Role),
					"text_content": message.TextContent(),
					"item_ids":     chatItemIDs(ctx.Items),
				},
			},
		}, nil
	case "add_message_default_time":
		ctx := lkllm.NewChatContext()
		ctx.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{ID: "existing", Role: lkllm.ChatRoleUser, Content: []lkllm.ChatContent{{Text: "existing"}}, CreatedAt: time.Unix(30, 0)},
		}
		message := ctx.AddMessage(lkllm.ChatMessageArgs{
			ID:      "new",
			Role:    lkllm.ChatRoleUser,
			Content: []lkllm.ChatContent{{Text: "new"}},
		})
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":           "add_message_default_time",
					"created_at_set": !message.CreatedAt.IsZero(),
					"item_ids":       chatItemIDs(ctx.Items),
				},
			},
		}, nil
	case "add_message_default_id":
		ctx := lkllm.NewChatContext()
		message := ctx.AddMessage(lkllm.ChatMessageArgs{
			Role:    lkllm.ChatRoleUser,
			Content: []lkllm.ChatContent{{Text: "hello"}},
		})
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":           "add_message_default_id",
					"id_prefix":      hasItemPrefix(message.GetID()),
					"stored_same_id": ctx.Items[0].GetID() == message.ID,
				},
			},
		}, nil
	case "add_message_text_content":
		ctx := lkllm.NewChatContext()
		message := ctx.AddMessage(lkllm.ChatMessageArgs{
			Role: lkllm.ChatRoleUser,
			Text: "hello",
		})
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{"name": "add_message_text_content", "text_content": message.TextContent()},
			},
		}, nil
	case "insert_config_update_default_id":
		ctx := lkllm.NewChatContext()
		config := &lkllm.AgentConfigUpdate{CreatedAt: time.Unix(10, 0)}
		ctx.Insert(config)
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":         "insert_config_update_default_id",
					"id_prefix":    hasItemPrefix(config.ID),
					"lookup_found": ctx.GetByID(config.ID) == config,
				},
			},
		}, nil
	case "insert_config_update_created_at":
		ctx := lkllm.NewChatContext()
		config := &lkllm.AgentConfigUpdate{ID: "config"}
		ctx.Insert(config)
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{"name": "insert_config_update_created_at", "created_at_set": !config.CreatedAt.IsZero()},
			},
		}, nil
	case "append_config_update_defaults":
		ctx := lkllm.NewChatContext()
		config := &lkllm.AgentConfigUpdate{}
		ctx.Append(config)
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":           "append_config_update_defaults",
					"id_prefix":      hasItemPrefix(config.ID),
					"created_at_set": !config.CreatedAt.IsZero(),
					"lookup_found":   ctx.GetByID(config.ID) == config,
				},
			},
		}, nil
	case "append_item_defaults":
		ctx := lkllm.NewChatContext()
		message := &lkllm.ChatMessage{Role: lkllm.ChatRoleUser}
		call := &lkllm.FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: "{}"}
		output := &lkllm.FunctionCallOutput{CallID: "call_lookup", Name: "lookup", Output: "ok"}
		handoff := &lkllm.AgentHandoff{NewAgentID: "next"}
		items := []lkllm.ChatItem{message, call, output, handoff}
		for _, item := range items {
			ctx.Append(item)
		}
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":           "append_item_defaults",
					"types":          chatItemTypes(items),
					"id_prefixes":    chatItemIDPrefixes(items),
					"created_at_set": chatItemCreatedAtSet(items),
					"lookup_found":   chatItemsFound(ctx, items),
				},
			},
		}, nil
	case "upsert_config_update_defaults":
		ctx := lkllm.NewChatContext()
		config := &lkllm.AgentConfigUpdate{}
		if err := ctx.UpsertItem(config); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":           "upsert_config_update_defaults",
					"id_prefix":      hasItemPrefix(config.ID),
					"created_at_set": !config.CreatedAt.IsZero(),
					"lookup_found":   ctx.GetByID(config.ID) == config,
				},
			},
		}, nil
	case "chat_message_text_content":
		message := &lkllm.ChatMessage{
			Role: lkllm.ChatRoleSystem,
			Content: []lkllm.ChatContent{
				{Instructions: lkllm.NewInstructions("voice instructions", "text instructions")},
				{Text: "plain text"},
				{Image: &lkllm.ImageContent{Image: "https://example.com/image.jpg"}},
				{Audio: &lkllm.AudioContent{Transcript: "spoken words"}},
			},
		}
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{"name": "chat_message_text_content", "text_content": message.TextContent()},
			},
		}, nil
	case "chat_message_text_content_empty_parts":
		message := &lkllm.ChatMessage{
			Role: lkllm.ChatRoleSystem,
			Content: []lkllm.ChatContent{
				{Text: ""},
				{Text: "instructions"},
			},
		}
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{"name": "chat_message_text_content_empty_parts", "text_content": message.TextContent()},
			},
		}, nil
	case "instructions_variant_selection":
		instructions := lkllm.NewInstructions("speak plainly", "write tersely")
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":            "instructions_variant_selection",
					"default":         instructions.String(),
					"text":            instructions.AsModality("text").String(),
					"roundtrip_audio": instructions.AsModality("text").AsModality("audio").String(),
				},
			},
		}, nil
	case "instructions_format_nested_variants":
		template := lkllm.NewInstructions("Say: %s", "Write: %s")
		value := lkllm.NewInstructions("hello out loud", "hello in text")
		formatted := template.Format(value)
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":    "instructions_format_nested_variants",
					"default": formatted.String(),
					"audio":   formatted.AsModality("audio").String(),
					"text":    formatted.AsModality("text").String(),
				},
			},
		}, nil
	case "instructions_format_active_representation":
		template := lkllm.NewInstructions("Say: %s", "Write: %s").AsModality("text")
		formatted := template.Format("hello")
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":    "instructions_format_active_representation",
					"default": formatted.String(),
					"audio":   formatted.AsModality("audio").String(),
				},
			},
		}, nil
	case "instructions_concat_variants":
		left := lkllm.NewInstructions("audio A", "text A").AsModality("text")
		right := lkllm.NewInstructions(" audio B", " text B")
		combined := left.Concat(right)
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":    "instructions_concat_variants",
					"default": combined.String(),
					"audio":   combined.AsModality("audio").String(),
					"text":    combined.AsModality("text").String(),
				},
			},
		}, nil
	case "instructions_append_string_variant":
		appended := lkllm.NewInstructions("audio", "text").AppendString(" suffix")
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":  "instructions_append_string_variant",
					"audio": appended.AsModality("audio").String(),
					"text":  appended.AsModality("text").String(),
				},
			},
		}, nil
	case "instructions_prepend_string_variant":
		prepended := lkllm.NewInstructions("audio", "text").AsModality("text").PrependString("prefix ")
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":    "instructions_prepend_string_variant",
					"default": prepended.String(),
					"audio":   prepended.AsModality("audio").String(),
					"text":    prepended.AsModality("text").String(),
				},
			},
		}, nil
	case "instructions_roundtrip":
		ctx := lkllm.NewChatContext()
		ctx.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{
				ID:   "system",
				Role: lkllm.ChatRoleSystem,
				Content: []lkllm.ChatContent{{
					Instructions: lkllm.NewInstructions("audio instructions", "text instructions"),
				}},
			},
		}
		data := ctx.ToDict()
		items := data["items"].([]map[string]any)
		content := items[0]["content"].([]any)
		instructions := content[0].(map[string]any)
		roundTrip, err := lkllm.ChatContextFromDict(data)
		if err != nil {
			return nil, err
		}
		msg := roundTrip.Items[0].(*lkllm.ChatMessage)
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name":             "instructions_roundtrip",
					"serialized_type":  instructions["type"],
					"serialized_audio": instructions["audio"],
					"serialized_text":  instructions["text"],
					"roundtrip_text":   msg.Content[0].Instructions.AsModality("text").String(),
				},
			},
		}, nil
	case "dict_shape":
		ctx := lkllm.NewChatContext()
		ctx.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{
				ID:   "message",
				Role: lkllm.ChatRoleUser,
				Content: []lkllm.ChatContent{
					{Text: "hello"},
					{Image: &lkllm.ImageContent{ID: "image", Image: "https://example.test/image.png", InferenceDetail: "high"}},
					{Audio: &lkllm.AudioContent{Transcript: "audio text"}},
				},
				CreatedAt: time.Unix(10, 0),
			},
			&lkllm.FunctionCall{ID: "call", CallID: "call_lookup", Name: "lookup", Arguments: `{}`, CreatedAt: time.Unix(11, 0)},
			&lkllm.FunctionCallOutput{ID: "output", CallID: "call_lookup", Name: "lookup", Output: "ok", CreatedAt: time.Unix(12, 0)},
			&lkllm.AgentConfigUpdate{ID: "config", CreatedAt: time.Unix(13, 0)},
		}
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{
					"name": "dict_shape",
					"data": ctx.ToDict(lkllm.ChatContextDictOptions{
						ExcludeFunctionCall: true,
						ExcludeConfigUpdate: true,
					}),
				},
			},
		}, nil
	case "dict_empty_string":
		ctx := lkllm.NewChatContext()
		ctx.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{
				ID:   "message",
				Role: lkllm.ChatRoleSystem,
				Content: []lkllm.ChatContent{
					{Text: ""},
					{Text: "instructions"},
				},
			},
		}
		return map[string]any{
			"contract": "llm-chat-context",
			"events": []map[string]any{
				{"name": "dict_empty_string", "data": ctx.ToDict()},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported llm chat context action %q", payload.Action)
	}
}

type llmScenarioSpec struct {
	SpecVersion string                   `json:"spec_version"`
	Kind        string                   `json:"kind"`
	Contract    string                   `json:"contract"`
	Fixtures    []llmScenarioFixtureSpec `json:"fixtures"`
	Steps       []llmScenarioStepSpec    `json:"steps"`
}

type llmScenarioFixtureSpec struct {
	Name    string         `json:"name"`
	Factory string         `json:"factory"`
	Args    map[string]any `json:"args"`
}

type llmScenarioStepSpec struct {
	Kind   string                    `json:"kind"`
	Op     string                    `json:"op"`
	Target string                    `json:"target"`
	Args   map[string]any            `json:"args"`
	Assign string                    `json:"assign"`
	Name   string                    `json:"name"`
	Fields []llmScenarioFieldMapping `json:"fields"`
}

type llmScenarioFieldMapping struct {
	Name      string `json:"name"`
	From      string `json:"from"`
	Transform string `json:"transform"`
}

type llmScenarioState struct {
	objects map[string]any
	vars    map[string]any
	events  []map[string]any
}

func runLLMChatContextDeclarative(input json.RawMessage) (any, error) {
	var spec llmScenarioSpec
	if err := json.Unmarshal(input, &spec); err != nil {
		return nil, err
	}
	if err := spec.validate(); err != nil {
		return nil, err
	}
	state := &llmScenarioState{
		objects: map[string]any{},
		vars:    map[string]any{},
	}
	for _, fixture := range spec.Fixtures {
		object, err := buildLLMChatContextFixture(fixture)
		if err != nil {
			return nil, err
		}
		state.objects[fixture.Name] = object
	}
	for _, step := range spec.Steps {
		switch step.Kind {
		case "call":
			if err := runLLMChatContextCallStep(state, step); err != nil {
				return nil, err
			}
		case "emit":
			if err := runLLMChatContextEmitStep(state, step); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported llm chat context step kind %q", step.Kind)
		}
	}
	return map[string]any{
		"contract": spec.Contract,
		"events":   state.events,
	}, nil
}

func (s llmScenarioSpec) validate() error {
	switch {
	case s.SpecVersion != "1.0":
		return fmt.Errorf("spec_version = %q, want 1.0", s.SpecVersion)
	case s.Kind != "parity-scenario":
		return fmt.Errorf("kind = %q, want parity-scenario", s.Kind)
	case s.Contract != "llm-chat-context":
		return fmt.Errorf("contract = %q, want llm-chat-context", s.Contract)
	case len(s.Steps) == 0:
		return errors.New("steps are required")
	default:
		return nil
	}
}

func buildLLMChatContextFixture(fixture llmScenarioFixtureSpec) (any, error) {
	switch fixture.Factory {
	case "llm_chat_context.empty":
		var receiver lkllm.ChatContext
		return receiver.Empty(), nil
	case "llm_chat_context.lookup_fixture":
		ctx := lkllm.NewChatContext()
		ctx.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{ID: "first", Role: lkllm.ChatRoleUser},
			&lkllm.FunctionCall{ID: "call", CallID: "call_lookup", Name: "lookup"},
		}
		return ctx, nil
	case "llm_chat_context.single_late_message":
		ctx := lkllm.NewChatContext()
		ctx.Items = []lkllm.ChatItem{
			&lkllm.ChatMessage{ID: "late", Role: lkllm.ChatRoleUser, Content: []lkllm.ChatContent{{Text: "late"}}, CreatedAt: time.Unix(30, 0)},
		}
		return ctx, nil
	case "llm_chat_context.messages":
		ctx := lkllm.NewChatContext()
		items, err := buildLLMChatContextMessagesFixture(fixture.Args)
		if err != nil {
			return nil, err
		}
		ctx.Items = items
		return ctx, nil
	case "llm_chat_context.items":
		ctx := lkllm.NewChatContext()
		items, err := buildLLMChatContextItemsFixture(fixture.Args)
		if err != nil {
			return nil, err
		}
		ctx.Items = items
		return ctx, nil
	default:
		return nil, fmt.Errorf("unsupported llm chat context fixture factory %q", fixture.Factory)
	}
}

func buildLLMChatContextMessagesFixture(args map[string]any) ([]lkllm.ChatItem, error) {
	rawItems, ok := args["items"].([]any)
	if !ok {
		return nil, errors.New("llm_chat_context.messages fixture requires items")
	}
	return buildLLMScenarioMessages(rawItems)
}

func buildLLMChatContextItemsFixture(args map[string]any) ([]lkllm.ChatItem, error) {
	rawItems, ok := args["items"].([]any)
	if !ok {
		return nil, errors.New("llm_chat_context.items fixture requires items")
	}
	items := make([]lkllm.ChatItem, 0, len(rawItems))
	for index, raw := range rawItems {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("fixture item %d is %T, want object", index, raw)
		}
		built, err := buildLLMScenarioItem(item)
		if err != nil {
			return nil, fmt.Errorf("fixture item %d: %w", index, err)
		}
		items = append(items, built)
	}
	return items, nil
}

func buildLLMScenarioMessages(rawItems []any) ([]lkllm.ChatItem, error) {
	items := make([]lkllm.ChatItem, 0, len(rawItems))
	for index, raw := range rawItems {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("fixture item %d is %T, want object", index, raw)
		}
		message, err := buildLLMScenarioMessage(item)
		if err != nil {
			return nil, fmt.Errorf("fixture item %d: %w", index, err)
		}
		items = append(items, message)
	}
	return items, nil
}

func buildLLMScenarioItem(item map[string]any) (lkllm.ChatItem, error) {
	itemType, _ := item["type"].(string)
	switch itemType {
	case "", "message":
		return buildLLMScenarioMessage(item)
	case "function_call":
		functionCall := &lkllm.FunctionCall{
			ID:        stringArg(item, "id"),
			CallID:    stringArg(item, "call_id"),
			Name:      stringArg(item, "name"),
			Arguments: stringArg(item, "arguments"),
		}
		if extra, ok := item["extra"].(map[string]any); ok {
			functionCall.Extra = extra
		}
		if createdAt, ok := scenarioIntArg(item, "created_at_unix"); ok {
			functionCall.CreatedAt = time.Unix(int64(createdAt), 0)
		}
		return functionCall, nil
	case "function_call_output":
		output := &lkllm.FunctionCallOutput{
			ID:      stringArg(item, "id"),
			CallID:  stringArg(item, "call_id"),
			Name:    stringArg(item, "name"),
			Output:  stringArg(item, "output"),
			IsError: scenarioBoolArg(item, "is_error"),
		}
		if createdAt, ok := scenarioIntArg(item, "created_at_unix"); ok {
			output.CreatedAt = time.Unix(int64(createdAt), 0)
		}
		return output, nil
	case "agent_handoff":
		handoff := &lkllm.AgentHandoff{
			ID:         stringArg(item, "id"),
			NewAgentID: stringArg(item, "new_agent_id"),
		}
		if createdAt, ok := scenarioIntArg(item, "created_at_unix"); ok {
			handoff.CreatedAt = time.Unix(int64(createdAt), 0)
		}
		return handoff, nil
	case "agent_config_update":
		configUpdate := &lkllm.AgentConfigUpdate{ID: stringArg(item, "id")}
		if createdAt, ok := scenarioIntArg(item, "created_at_unix"); ok {
			configUpdate.CreatedAt = time.Unix(int64(createdAt), 0)
		}
		return configUpdate, nil
	default:
		return nil, fmt.Errorf("unsupported item type %q", itemType)
	}
}

func buildLLMScenarioMessage(item map[string]any) (*lkllm.ChatMessage, error) {
	content, hasContent, err := buildLLMScenarioContent(item)
	if err != nil {
		return nil, err
	}
	message := &lkllm.ChatMessage{
		ID:          stringArg(item, "id"),
		Role:        lkllm.ChatRole(stringArg(item, "role")),
		Content:     []lkllm.ChatContent{{Text: stringArg(item, "text")}},
		Interrupted: scenarioBoolArg(item, "interrupted"),
	}
	if extra, ok := item["extra"].(map[string]any); ok {
		message.Extra = extra
	}
	if metrics, ok := item["metrics"].(map[string]any); ok {
		message.Metrics = metrics
	}
	if _, ok := item["text"]; !ok {
		message.Content = nil
	}
	if hasContent {
		message.Content = content
	}
	if createdAt, ok := scenarioIntArg(item, "created_at_unix"); ok {
		message.CreatedAt = time.Unix(int64(createdAt), 0)
	}
	return message, nil
}

func buildLLMScenarioContent(item map[string]any) ([]lkllm.ChatContent, bool, error) {
	rawContent, ok := item["content"]
	if !ok {
		return nil, false, nil
	}
	parts, ok := rawContent.([]any)
	if !ok {
		return nil, true, fmt.Errorf("message content is %T, want array", rawContent)
	}
	content := make([]lkllm.ChatContent, 0, len(parts))
	for index, rawPart := range parts {
		switch part := rawPart.(type) {
		case string:
			content = append(content, lkllm.ChatContent{Text: part})
		case map[string]any:
			built, err := buildLLMScenarioContentPart(part)
			if err != nil {
				return nil, true, fmt.Errorf("content part %d: %w", index, err)
			}
			content = append(content, built)
		default:
			return nil, true, fmt.Errorf("content part %d is %T, want string or object", index, rawPart)
		}
	}
	return content, true, nil
}

func buildLLMScenarioContentPart(part map[string]any) (lkllm.ChatContent, error) {
	partType, _ := part["type"].(string)
	switch partType {
	case "instructions":
		instructions := lkllm.NewInstructions(stringArg(part, "audio"))
		if _, ok := part["text"]; ok {
			instructions = lkllm.NewInstructions(stringArg(part, "audio"), stringArg(part, "text"))
		}
		if active := stringArg(part, "active"); active != "" {
			instructions = instructions.AsModality(active)
		}
		return lkllm.ChatContent{
			Instructions: instructions,
		}, nil
	case "image_content":
		return lkllm.ChatContent{
			Image: &lkllm.ImageContent{
				ID:              stringArg(part, "id"),
				Image:           part["image"],
				InferenceDetail: stringArg(part, "inference_detail"),
				MimeType:        stringArg(part, "mime_type"),
			},
		}, nil
	case "audio_content":
		return lkllm.ChatContent{
			Audio: &lkllm.AudioContent{Transcript: stringArg(part, "transcript")},
		}, nil
	default:
		return lkllm.ChatContent{}, fmt.Errorf("unsupported content type %q", partType)
	}
}

func runLLMChatContextCallStep(state *llmScenarioState, step llmScenarioStepSpec) error {
	ctx, ok := state.objects[step.Target].(*lkllm.ChatContext)
	if !ok {
		ctx, ok = state.vars[step.Target].(*lkllm.ChatContext)
	}
	if !ok {
		return fmt.Errorf("target %q is %T, want *llm.ChatContext", step.Target, state.objects[step.Target])
	}
	id, _ := step.Args["id"].(string)
	switch step.Op {
	case "count_items":
		state.vars[step.Assign] = len(ctx.Items)
	case "clear_items":
		ctx.Items = nil
	case "append_item":
		rawItem, ok := step.Args["item"].(map[string]any)
		if !ok {
			return errors.New("append_item requires item")
		}
		item, err := buildLLMScenarioItem(rawItem)
		if err != nil {
			return err
		}
		ctx.Append(item)
		state.vars[step.Assign] = item
	case "copy":
		tools, err := buildLLMScenarioToolsArg(step.Args)
		if err != nil {
			return err
		}
		state.vars[step.Assign] = ctx.Copy(lkllm.ChatContextCopyOptions{
			ExcludeFunctionCall: scenarioBoolArg(step.Args, "exclude_function_call"),
			ExcludeInstructions: scenarioBoolArg(step.Args, "exclude_instructions"),
			ExcludeEmptyMessage: scenarioBoolArg(step.Args, "exclude_empty_message"),
			ExcludeHandoff:      scenarioBoolArg(step.Args, "exclude_handoff"),
			ExcludeConfigUpdate: scenarioBoolArg(step.Args, "exclude_config_update"),
			Tools:               tools,
		})
	case "merge":
		otherName := stringArg(step.Args, "other")
		other, ok := state.objects[otherName].(*lkllm.ChatContext)
		if !ok {
			return fmt.Errorf("merge other %q is %T, want *llm.ChatContext", otherName, state.objects[otherName])
		}
		state.vars[step.Assign] = ctx.Merge(other, lkllm.ChatContextMergeOptions{
			ExcludeFunctionCall: scenarioBoolArg(step.Args, "exclude_function_call"),
			ExcludeInstructions: scenarioBoolArg(step.Args, "exclude_instructions"),
			ExcludeConfigUpdate: scenarioBoolArg(step.Args, "exclude_config_update"),
		})
	case "is_equivalent":
		otherName := stringArg(step.Args, "other")
		other, ok := state.objects[otherName].(*lkllm.ChatContext)
		if !ok {
			return fmt.Errorf("is_equivalent other %q is %T, want *llm.ChatContext", otherName, state.objects[otherName])
		}
		state.vars[step.Assign] = ctx.IsEquivalent(other)
	case "function_call_item_to_message":
		item := ctx.GetByID(id)
		if item == nil {
			state.vars[step.Assign] = nil
			return nil
		}
		state.vars[step.Assign] = lkllm.FunctionCallItemToMessage(item)
	case "tool_names":
		tools, err := buildLLMScenarioTools(step.Args)
		if err != nil {
			return err
		}
		state.vars[step.Assign] = ctx.GetToolNames(tools)
	case "lookup_by_id":
		state.vars[step.Assign] = ctx.GetByID(id)
	case "messages":
		state.vars[step.Assign] = ctx.Messages()
	case "truncate":
		maxItems, ok := scenarioIntArg(step.Args, "max_items")
		if !ok {
			return errors.New("truncate requires max_items")
		}
		state.vars[step.Assign] = ctx.Truncate(maxItems)
	case "index":
		index := ctx.IndexByID(id)
		if index == nil {
			state.vars[step.Assign] = nil
			return nil
		}
		state.vars[step.Assign] = *index
	case "read_only":
		state.vars[step.Assign] = ctx.ReadOnly()
	case "to_dict":
		state.vars[step.Assign] = ctx.ToDict(lkllm.ChatContextDictOptions{
			ExcludeFunctionCall: scenarioBoolArg(step.Args, "exclude_function_call"),
			ExcludeConfigUpdate: scenarioBoolArg(step.Args, "exclude_config_update"),
			ExcludeMetrics:      scenarioBoolArg(step.Args, "exclude_metrics"),
			IncludeImage:        scenarioBoolArg(step.Args, "include_image"),
			IncludeTimestamp:    scenarioBoolArg(step.Args, "include_timestamp"),
		})
	case "from_dict":
		sourceName := stringArg(step.Args, "source")
		source, ok := state.vars[sourceName].(map[string]any)
		if !ok {
			return fmt.Errorf("from_dict source %q is %T, want map[string]any", sourceName, state.vars[sourceName])
		}
		restored, err := lkllm.ChatContextFromDict(source)
		if err != nil {
			return err
		}
		state.vars[step.Assign] = restored
	case "from_dict_capture_error":
		data, ok := step.Args["data"].(map[string]any)
		if !ok {
			return errors.New("from_dict_capture_error requires data")
		}
		err := ctx.FromDict(data)
		_, hasItems := data["items"]
		switch {
		case err == nil:
			state.vars[step.Assign] = ""
		case !hasItems:
			state.vars[step.Assign] = "items_required"
		case data["items"] == nil:
			state.vars[step.Assign] = "items_list_required"
		default:
			state.vars[step.Assign] = "invalid_items"
		}
	case "to_provider_format":
		options := lkllm.ChatContextProviderFormatOptions{}
		if _, ok := step.Args["inject_dummy_user_message"]; ok {
			value := scenarioBoolArg(step.Args, "inject_dummy_user_message")
			options.InjectDummyUserMessage = &value
		}
		if _, ok := step.Args["inject_trailing_user_message"]; ok {
			value := scenarioBoolArg(step.Args, "inject_trailing_user_message")
			options.InjectTrailingUserMessage = &value
		}
		if raw, ok := step.Args["thought_signatures"].(map[string]any); ok {
			options.ThoughtSignatures = map[string][]byte{}
			for key, value := range raw {
				options.ThoughtSignatures[key] = []byte(fmt.Sprint(value))
			}
		}
		formatted, _, providerErr := ctx.ToProviderFormatE(stringArg(step.Args, "format"), options)
		if providerErr != nil {
			return providerErr
		}
		state.vars[step.Assign] = formatted
	case "to_provider_format_with_extra":
		options := lkllm.ChatContextProviderFormatOptions{}
		if _, ok := step.Args["inject_dummy_user_message"]; ok {
			value := scenarioBoolArg(step.Args, "inject_dummy_user_message")
			options.InjectDummyUserMessage = &value
		}
		if _, ok := step.Args["inject_trailing_user_message"]; ok {
			value := scenarioBoolArg(step.Args, "inject_trailing_user_message")
			options.InjectTrailingUserMessage = &value
		}
		if raw, ok := step.Args["thought_signatures"].(map[string]any); ok {
			options.ThoughtSignatures = map[string][]byte{}
			for key, value := range raw {
				options.ThoughtSignatures[key] = []byte(fmt.Sprint(value))
			}
		}
		formatted, extra, providerErr := ctx.ToProviderFormatE(stringArg(step.Args, "format"), options)
		if providerErr != nil {
			return providerErr
		}
		state.vars[step.Assign] = map[string]any{
			"messages": formatted,
			"extra":    extra,
		}
	case "to_provider_format_capture_error":
		_, _, providerErr := ctx.ToProviderFormatE(stringArg(step.Args, "format"))
		state.vars[step.Assign] = map[string]any{
			"error": providerErr != nil,
		}
	case "add_message":
		role, _ := step.Args["role"].(string)
		text, _ := step.Args["text"].(string)
		args := lkllm.ChatMessageArgs{
			ID:   id,
			Role: lkllm.ChatRole(role),
			Text: text,
		}
		if createdAt, ok := scenarioIntArg(step.Args, "created_at_unix"); ok {
			args.CreatedAt = time.Unix(int64(createdAt), 0)
		}
		state.vars[step.Assign] = ctx.AddMessage(args)
	case "add_message_capture_panic":
		role, _ := step.Args["role"].(string)
		text, _ := step.Args["text"].(string)
		state.vars[step.Assign] = capturePanicString(func() {
			ctx.AddMessage(lkllm.ChatMessageArgs{
				ID:   id,
				Role: lkllm.ChatRole(role),
				Text: text,
			})
		})
	case "insert_messages":
		rawItems, ok := step.Args["items"].([]any)
		if !ok {
			return errors.New("insert_messages requires items")
		}
		items, err := buildLLMScenarioMessages(rawItems)
		if err != nil {
			return err
		}
		ctx.Insert(items...)
		state.vars[step.Assign] = items
	case "insert_item":
		rawItem, ok := step.Args["item"].(map[string]any)
		if !ok {
			return errors.New("insert_item requires item")
		}
		item, err := buildLLMScenarioItem(rawItem)
		if err != nil {
			return err
		}
		ctx.Insert(item)
		state.vars[step.Assign] = item
	case "upsert_item":
		rawItem, ok := step.Args["item"].(map[string]any)
		if !ok {
			return errors.New("upsert_item requires item")
		}
		item, err := buildLLMScenarioItem(rawItem)
		if err != nil {
			return err
		}
		err = ctx.UpsertItem(item, lkllm.ChatContextUpsertOptions{
			AllowTypeMismatch: scenarioBoolArg(step.Args, "allow_type_mismatch"),
		})
		state.vars[step.Assign] = item
		if step.Assign != "" {
			state.vars[step.Assign+"_error"] = err
		}
	case "upsert_message":
		item := &lkllm.ChatMessage{
			ID:      id,
			Role:    lkllm.ChatRole(stringArg(step.Args, "role")),
			Content: []lkllm.ChatContent{{Text: stringArg(step.Args, "text")}},
		}
		if createdAt, ok := scenarioIntArg(step.Args, "created_at_unix"); ok {
			item.CreatedAt = time.Unix(int64(createdAt), 0)
		}
		err := ctx.UpsertItem(item, lkllm.ChatContextUpsertOptions{
			AllowTypeMismatch: scenarioBoolArg(step.Args, "allow_type_mismatch"),
		})
		state.vars[step.Assign] = item
		if step.Assign != "" {
			state.vars[step.Assign+"_error"] = err
		}
	case "upsert_function_call":
		item := &lkllm.FunctionCall{
			ID:        id,
			CallID:    stringArg(step.Args, "call_id"),
			Name:      stringArg(step.Args, "name"),
			Arguments: stringArg(step.Args, "arguments"),
		}
		if createdAt, ok := scenarioIntArg(step.Args, "created_at_unix"); ok {
			item.CreatedAt = time.Unix(int64(createdAt), 0)
		}
		err := ctx.UpsertItem(item, lkllm.ChatContextUpsertOptions{
			AllowTypeMismatch: scenarioBoolArg(step.Args, "allow_type_mismatch"),
		})
		state.vars[step.Assign] = item
		if step.Assign != "" {
			state.vars[step.Assign+"_error"] = err
		}
	default:
		return fmt.Errorf("unsupported llm chat context call op %q", step.Op)
	}
	return nil
}

func runLLMChatContextEmitStep(state *llmScenarioState, step llmScenarioStepSpec) error {
	event := map[string]any{"name": step.Name}
	for _, field := range step.Fields {
		value, ok := state.vars[field.From]
		if !ok {
			value = state.objects[field.From]
		}
		resolved, err := transformLLMScenarioField(state, value, field.Transform)
		if err != nil {
			return fmt.Errorf("emit field %q: %w", field.Name, err)
		}
		event[field.Name] = resolved
	}
	state.events = append(state.events, event)
	return nil
}

func transformLLMScenarioField(state *llmScenarioState, value any, transform string) (any, error) {
	if strings.HasPrefix(transform, "provider_first_extra_has:") {
		key := strings.TrimPrefix(transform, "provider_first_extra_has:")
		extra, err := firstProviderMessageExtra(value)
		if err != nil {
			return nil, err
		}
		_, ok := extra[key]
		return ok, nil
	}
	if strings.HasPrefix(transform, "provider_first_tool_call_extra_has:") {
		key := strings.TrimPrefix(transform, "provider_first_tool_call_extra_has:")
		extra, err := firstProviderToolCallExtra(value)
		if err != nil {
			return nil, err
		}
		_, ok := extra[key]
		return ok, nil
	}
	if strings.HasPrefix(transform, "context_first_id_matches:") {
		ctx, ok := value.(*lkllm.ChatContext)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use context_first_id_matches", value)
		}
		if len(ctx.Items) == 0 {
			return false, nil
		}
		varName := strings.TrimPrefix(transform, "context_first_id_matches:")
		item, ok := state.vars[varName].(lkllm.ChatItem)
		if !ok {
			return nil, fmt.Errorf("variable %q cannot use context_first_id_matches", varName)
		}
		return ctx.Items[0].GetID() == item.GetID(), nil
	}
	if strings.HasPrefix(transform, "context_first_identity_matches:") {
		ctx, ok := value.(*lkllm.ChatContext)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use context_first_identity_matches", value)
		}
		if len(ctx.Items) == 0 {
			return false, nil
		}
		varName := strings.TrimPrefix(transform, "context_first_identity_matches:")
		item, ok := state.vars[varName].(lkllm.ChatItem)
		if !ok {
			return nil, fmt.Errorf("variable %q cannot use context_first_identity_matches", varName)
		}
		return ctx.Items[0] == item, nil
	}
	if strings.HasPrefix(transform, "context_last_identity_matches:") {
		ctx, ok := value.(*lkllm.ChatContext)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use context_last_identity_matches", value)
		}
		if len(ctx.Items) == 0 {
			return false, nil
		}
		varName := strings.TrimPrefix(transform, "context_last_identity_matches:")
		item, ok := state.vars[varName].(lkllm.ChatItem)
		if !ok {
			return nil, fmt.Errorf("variable %q cannot use context_last_identity_matches", varName)
		}
		return ctx.Items[len(ctx.Items)-1] == item, nil
	}
	switch transform {
	case "", "identity":
		return value, nil
	case "exists":
		return value != nil, nil
	case "error_message":
		if value == nil {
			return "", nil
		}
		err, ok := value.(error)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use error_message", value)
		}
		return err.Error(), nil
	case "null_if_missing":
		return value, nil
	case "int_or_null":
		switch typed := value.(type) {
		case nil:
			return nil, nil
		case int:
			return typed, nil
		default:
			return nil, fmt.Errorf("value %T cannot use int_or_null", value)
		}
	case "item_id":
		item, ok := value.(lkllm.ChatItem)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use item_id", value)
		}
		return item.GetID(), nil
	case "item_id_has_prefix":
		item, ok := value.(lkllm.ChatItem)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use item_id_has_prefix", value)
		}
		return hasItemPrefix(item.GetID()), nil
	case "message_role":
		message, ok := value.(*lkllm.ChatMessage)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use message_role", value)
		}
		return string(message.Role), nil
	case "message_text_content":
		message, ok := value.(*lkllm.ChatMessage)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use message_text_content", value)
		}
		return message.TextContent(), nil
	case "message_extra":
		message, ok := value.(*lkllm.ChatMessage)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use message_extra", value)
		}
		return message.Extra, nil
	case "item_created_at_set":
		item, ok := value.(lkllm.ChatItem)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use item_created_at_set", value)
		}
		return !item.GetCreatedAt().IsZero(), nil
	case "dict_item_created_at_values":
		data, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use dict_item_created_at_values", value)
		}
		items, ok := data["items"].([]map[string]any)
		if !ok {
			return nil, fmt.Errorf("dict items are %T, want []map[string]any", data["items"])
		}
		values := make([]any, 0, len(items))
		for _, item := range items {
			values = append(values, item["created_at"])
		}
		return values, nil
	case "context_item_ids":
		ctx, ok := value.(*lkllm.ChatContext)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use context_item_ids", value)
		}
		return chatItemIDs(ctx.Items), nil
	case "context_item_types":
		ctx, ok := value.(*lkllm.ChatContext)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use context_item_types", value)
		}
		return chatItemTypes(ctx.Items), nil
	case "context_item_id_prefixes":
		ctx, ok := value.(*lkllm.ChatContext)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use context_item_id_prefixes", value)
		}
		return chatItemIDPrefixes(ctx.Items), nil
	case "context_item_created_at_set":
		ctx, ok := value.(*lkllm.ChatContext)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use context_item_created_at_set", value)
		}
		return chatItemCreatedAtSet(ctx.Items), nil
	case "context_items_found":
		ctx, ok := value.(*lkllm.ChatContext)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use context_items_found", value)
		}
		return chatItemsFound(ctx, ctx.Items), nil
	case "context_readonly":
		ctx, ok := value.(*lkllm.ChatContext)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use context_readonly", value)
		}
		return ctx.Readonly(), nil
	case "context_item_count":
		ctx, ok := value.(*lkllm.ChatContext)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use context_item_count", value)
		}
		return len(ctx.Items), nil
	case "context_items_is_list":
		ctx, ok := value.(*lkllm.ChatContext)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use context_items_is_list", value)
		}
		return ctx.Items != nil, nil
	case "message_list_is_list":
		_, ok := value.([]*lkllm.ChatMessage)
		return ok, nil
	case "message_list_count":
		messages, ok := value.([]*lkllm.ChatMessage)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use message_list_count", value)
		}
		return len(messages), nil
	case "message_list_ids":
		messages, ok := value.([]*lkllm.ChatMessage)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use message_list_ids", value)
		}
		ids := make([]string, 0, len(messages))
		for _, message := range messages {
			ids = append(ids, message.ID)
		}
		return ids, nil
	case "context_first_message_text_content":
		ctx, ok := value.(*lkllm.ChatContext)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use context_first_message_text_content", value)
		}
		if len(ctx.Items) == 0 {
			return nil, nil
		}
		message, ok := ctx.Items[0].(*lkllm.ChatMessage)
		if !ok {
			return nil, fmt.Errorf("context first item is %T, want *llm.ChatMessage", ctx.Items[0])
		}
		return message.TextContent(), nil
	case "context_first_item_type":
		ctx, ok := value.(*lkllm.ChatContext)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use context_first_item_type", value)
		}
		if len(ctx.Items) == 0 {
			return nil, nil
		}
		return ctx.Items[0].GetType(), nil
	case "dict_first_instruction_type":
		instructions, err := firstSerializedInstruction(value)
		if err != nil {
			return nil, err
		}
		return instructions["type"], nil
	case "dict_first_instruction_audio":
		instructions, err := firstSerializedInstruction(value)
		if err != nil {
			return nil, err
		}
		return instructions["audio"], nil
	case "dict_first_instruction_text":
		instructions, err := firstSerializedInstruction(value)
		if err != nil {
			return nil, err
		}
		return instructions["text"], nil
	case "dict_first_instruction_text_present":
		instructions, err := firstSerializedInstruction(value)
		if err != nil {
			return nil, err
		}
		_, ok := instructions["text"]
		return ok, nil
	case "dict_first_image_id_has_prefix":
		image, err := firstSerializedImage(value)
		if err != nil {
			return nil, err
		}
		id, _ := image["id"].(string)
		return strings.HasPrefix(id, "img_"), nil
	case "dict_first_image_inference_detail":
		image, err := firstSerializedImage(value)
		if err != nil {
			return nil, err
		}
		return image["inference_detail"], nil
	case "provider_first_content":
		messages, ok := value.([]map[string]any)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use provider_first_content", value)
		}
		if len(messages) == 0 {
			return nil, nil
		}
		return messages[0]["content"], nil
	case "provider_first_role":
		messages, ok := value.([]map[string]any)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use provider_first_role", value)
		}
		if len(messages) == 0 {
			return nil, nil
		}
		return messages[0]["role"], nil
	case "provider_first_image_url":
		image, err := firstProviderImagePart(value)
		if err != nil {
			return nil, err
		}
		imageURL, _ := image["image_url"].(map[string]any)
		return imageURL["url"], nil
	case "provider_first_image_detail":
		image, err := firstProviderImagePart(value)
		if err != nil {
			return nil, err
		}
		imageURL, _ := image["image_url"].(map[string]any)
		return imageURL["detail"], nil
	case "provider_first_text_part":
		messages, ok := value.([]map[string]any)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use provider_first_text_part", value)
		}
		if len(messages) == 0 {
			return nil, nil
		}
		content, ok := messages[0]["content"].([]map[string]any)
		if !ok {
			return nil, fmt.Errorf("provider first content is %T, want []map[string]any", messages[0]["content"])
		}
		for _, part := range content {
			if part["type"] == "text" {
				return part["text"], nil
			}
		}
		return nil, nil
	case "provider_first_extra_exists":
		extra, err := firstProviderMessageExtra(value)
		if err != nil {
			return nil, err
		}
		return len(extra) > 0, nil
	case "provider_first_tool_call_extra_exists":
		extra, err := firstProviderToolCallExtra(value)
		if err != nil {
			return nil, err
		}
		return len(extra) > 0, nil
	case "provider_first_tool_call_ids":
		messages, ok := value.([]map[string]any)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use provider_first_tool_call_ids", value)
		}
		if len(messages) == 0 {
			return nil, nil
		}
		toolCalls, ok := messages[0]["tool_calls"].([]map[string]any)
		if !ok {
			return []string{}, nil
		}
		ids := make([]string, 0, len(toolCalls))
		for _, toolCall := range toolCalls {
			id, _ := toolCall["id"].(string)
			ids = append(ids, id)
		}
		return ids, nil
	case "provider_tool_output_contents":
		messages, ok := value.([]map[string]any)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use provider_tool_output_contents", value)
		}
		contents := make([]string, 0)
		for _, message := range messages {
			if message["role"] != "tool" {
				continue
			}
			content, _ := message["content"].(string)
			contents = append(contents, content)
		}
		return contents, nil
	case "provider_message_count":
		messages, ok := value.([]map[string]any)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use provider_message_count", value)
		}
		return len(messages), nil
	case "provider_last_role":
		messages, ok := value.([]map[string]any)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use provider_last_role", value)
		}
		if len(messages) == 0 {
			return nil, nil
		}
		return messages[len(messages)-1]["role"], nil
	case "provider_last_text":
		messages, ok := value.([]map[string]any)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use provider_last_text", value)
		}
		if len(messages) == 0 {
			return nil, nil
		}
		content, ok := messages[len(messages)-1]["content"].([]map[string]any)
		if ok && len(content) > 0 {
			return content[0]["text"], nil
		}
		return messages[len(messages)-1]["content"], nil
	case "provider_error_exists":
		data, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use provider_error_exists", value)
		}
		exists, _ := data["error"].(bool)
		return exists, nil
	case "google_first_function_call_thought_signature":
		messages, ok := value.([]map[string]any)
		if !ok {
			return nil, fmt.Errorf("value %T cannot use google_first_function_call_thought_signature", value)
		}
		for _, message := range messages {
			parts, ok := message["parts"].([]map[string]any)
			if !ok {
				continue
			}
			for _, part := range parts {
				if _, ok := part["function_call"]; !ok {
					continue
				}
				signature, ok := part["thought_signature"].([]byte)
				if ok {
					return string(signature), nil
				}
				return part["thought_signature"], nil
			}
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported transform %q", transform)
	}
}

func firstSerializedInstruction(value any) (map[string]any, error) {
	data, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("value %T cannot use dict_first_instruction transform", value)
	}
	rawItems, ok := data["items"].([]map[string]any)
	if !ok || len(rawItems) == 0 {
		return nil, errors.New("dict_first_instruction requires non-empty items")
	}
	rawContent, ok := rawItems[0]["content"].([]any)
	if !ok || len(rawContent) == 0 {
		return nil, errors.New("dict_first_instruction requires non-empty content")
	}
	instructions, ok := rawContent[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("first content is %T, want object", rawContent[0])
	}
	return instructions, nil
}

func firstSerializedImage(value any) (map[string]any, error) {
	data, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("value %T cannot use dict_first_image transform", value)
	}
	rawItems, ok := data["items"].([]map[string]any)
	if !ok || len(rawItems) == 0 {
		return nil, errors.New("dict_first_image requires non-empty items")
	}
	rawContent, ok := rawItems[0]["content"].([]any)
	if !ok || len(rawContent) == 0 {
		return nil, errors.New("dict_first_image requires non-empty content")
	}
	for _, part := range rawContent {
		image, ok := part.(map[string]any)
		if ok && image["type"] == "image_content" {
			return image, nil
		}
	}
	return nil, errors.New("dict_first_image requires image content")
}

func firstProviderImagePart(value any) (map[string]any, error) {
	messages, ok := value.([]map[string]any)
	if !ok {
		return nil, fmt.Errorf("value %T cannot use provider_first_image transform", value)
	}
	if len(messages) == 0 {
		return nil, errors.New("provider_first_image requires non-empty messages")
	}
	content, ok := messages[0]["content"].([]map[string]any)
	if !ok {
		return nil, fmt.Errorf("provider first content is %T, want []map[string]any", messages[0]["content"])
	}
	for _, part := range content {
		if part["type"] == "image_url" {
			return part, nil
		}
	}
	return nil, errors.New("provider_first_image requires image_url content")
}

func firstProviderMessageExtra(value any) (map[string]any, error) {
	messages, ok := value.([]map[string]any)
	if !ok {
		return nil, fmt.Errorf("value %T cannot use provider_first_extra transform", value)
	}
	if len(messages) == 0 {
		return nil, errors.New("provider_first_extra requires non-empty messages")
	}
	extra, ok := messages[0]["extra_content"].(map[string]any)
	if !ok {
		return map[string]any{}, nil
	}
	return extra, nil
}

func firstProviderToolCallExtra(value any) (map[string]any, error) {
	messages, ok := value.([]map[string]any)
	if !ok {
		return nil, fmt.Errorf("value %T cannot use provider_first_tool_call_extra transform", value)
	}
	for _, message := range messages {
		toolCalls, ok := message["tool_calls"].([]map[string]any)
		if !ok {
			continue
		}
		for _, toolCall := range toolCalls {
			extra, ok := toolCall["extra_content"].(map[string]any)
			if !ok {
				return map[string]any{}, nil
			}
			return extra, nil
		}
	}
	return map[string]any{}, nil
}

func stringArg(args map[string]any, name string) string {
	value, _ := args[name].(string)
	return value
}

func scenarioBoolArg(args map[string]any, name string) bool {
	value, _ := args[name].(bool)
	return value
}

func buildLLMScenarioToolsArg(args map[string]any) ([]interface{}, error) {
	raw, ok := args["tools"].([]any)
	if !ok {
		return nil, nil
	}
	tools := make([]interface{}, 0, len(raw))
	for index, item := range raw {
		switch tool := item.(type) {
		case string:
			tools = append(tools, tool)
		case map[string]any:
			built, err := buildLLMScenarioTool(tool)
			if err != nil {
				return nil, fmt.Errorf("tool spec %d: %w", index, err)
			}
			tools = append(tools, built)
		default:
			return nil, fmt.Errorf("tool spec %d is %T, want string or object", index, item)
		}
	}
	return tools, nil
}

func buildLLMScenarioTools(args map[string]any) ([]interface{}, error) {
	raw, ok := args["tools"].([]any)
	if !ok {
		return nil, errors.New("tool_names requires tools")
	}
	tools := make([]interface{}, 0, len(raw))
	for index, item := range raw {
		spec, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("tool spec %d is %T, want object", index, item)
		}
		tool, err := buildLLMScenarioTool(spec)
		if err != nil {
			return nil, fmt.Errorf("tool spec %d: %w", index, err)
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

func buildLLMScenarioTool(spec map[string]any) (interface{}, error) {
	toolType := stringArg(spec, "type")
	if toolType == "" {
		if _, ok := spec["tools"]; ok {
			toolType = "toolset"
		} else {
			toolType = "tool"
		}
	}
	switch toolType {
	case "name":
		return stringArg(spec, "name"), nil
	case "tool":
		return &scenarioTool{id: stringArg(spec, "id"), name: stringArg(spec, "name")}, nil
	case "toolset":
		rawTools, ok := spec["tools"].([]any)
		if !ok {
			return nil, errors.New("toolset requires tools")
		}
		tools := make([]lkllm.Tool, 0, len(rawTools))
		for index, rawTool := range rawTools {
			childSpec, ok := rawTool.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("toolset child %d is %T, want object", index, rawTool)
			}
			child, err := buildLLMScenarioTool(childSpec)
			if err != nil {
				return nil, fmt.Errorf("toolset child %d: %w", index, err)
			}
			tool, ok := child.(lkllm.Tool)
			if !ok {
				return nil, fmt.Errorf("toolset child %d is %T, want tool", index, child)
			}
			tools = append(tools, tool)
		}
		return &scenarioToolset{id: stringArg(spec, "id"), tools: tools}, nil
	case "ignored":
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported tool type %q", toolType)
	}
}

func scenarioIntArg(args map[string]any, name string) (int, bool) {
	value, ok := args[name]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	default:
		return 0, false
	}
}

func chatItemIDs(items []lkllm.ChatItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.GetID())
	}
	return ids
}

func chatItemTypes(items []lkllm.ChatItem) []string {
	types := make([]string, 0, len(items))
	for _, item := range items {
		types = append(types, item.GetType())
	}
	return types
}

func hasItemPrefix(id string) bool {
	return strings.HasPrefix(id, "item_")
}

func chatItemIDPrefixes(items []lkllm.ChatItem) []bool {
	prefixes := make([]bool, 0, len(items))
	for _, item := range items {
		prefixes = append(prefixes, hasItemPrefix(item.GetID()))
	}
	return prefixes
}

func chatItemCreatedAtSet(items []lkllm.ChatItem) []bool {
	set := make([]bool, 0, len(items))
	for _, item := range items {
		set = append(set, !item.GetCreatedAt().IsZero())
	}
	return set
}

func chatItemsFound(ctx *lkllm.ChatContext, items []lkllm.ChatItem) []bool {
	found := make([]bool, 0, len(items))
	for _, item := range items {
		found = append(found, ctx.GetByID(item.GetID()) == item)
	}
	return found
}

func capturePanicString(fn func()) (message string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			message = fmt.Sprint(recovered)
		}
	}()
	fn()
	return ""
}

type scenarioTool struct {
	id   string
	name string
}

func (t *scenarioTool) ID() string                                      { return t.id }
func (t *scenarioTool) Name() string                                    { return t.name }
func (t *scenarioTool) Description() string                             { return "" }
func (t *scenarioTool) Parameters() map[string]any                      { return nil }
func (t *scenarioTool) Execute(context.Context, string) (string, error) { return "", nil }

type scenarioToolset struct {
	id    string
	tools []lkllm.Tool
}

func (s *scenarioToolset) ID() string                                      { return s.id }
func (s *scenarioToolset) Name() string                                    { return s.id }
func (s *scenarioToolset) Description() string                             { return "" }
func (s *scenarioToolset) Parameters() map[string]any                      { return nil }
func (s *scenarioToolset) Execute(context.Context, string) (string, error) { return "", nil }
func (s *scenarioToolset) Tools() []lkllm.Tool                             { return s.tools }

func runLLMValueObjects(input json.RawMessage) (any, error) {
	var payload struct {
		Action string `json:"action"`
		Mode   string `json:"mode"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Action == "" {
		payload.Action = "metadata_defaults"
	}
	switch payload.Action {
	case "metadata_defaults":
		provider := &fakeScenarioLLM{}
		lkllm.Prewarm(provider)
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":          "metadata_defaults",
					"model":         lkllm.Model(provider),
					"provider":      lkllm.Provider(provider),
					"prewarm_calls": provider.prewarmCalls,
				},
			},
		}, nil
	case "metadata_overrides":
		provider := &fakeScenarioLLM{label: "test.LLM", model: "model-a", provider: "provider-a"}
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":     "metadata_overrides",
					"label":    lkllm.Label(provider),
					"model":    lkllm.Model(provider),
					"provider": lkllm.Provider(provider),
				},
			},
		}, nil
	case "prewarm":
		provider := &fakeScenarioLLM{}
		lkllm.Prewarm(provider)
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":          "prewarm",
					"prewarm_calls": provider.prewarmCalls,
				},
			},
		}, nil
	case "llm_error_payload":
		underlying := errors.New("provider unavailable")
		err := lkllm.NewLLMError("openai.LLM", underlying, true)
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":               "llm_error_payload",
					"type":               err.Type,
					"label":              err.Label,
					"recoverable":        err.Recoverable,
					"timestamp_positive": err.Timestamp.UnixNano() > 0,
					"error_message":      err.Error(),
				},
			},
		}, nil
	case "completion_usage_payload":
		usage := lkllm.CompletionUsage{
			CompletionTokens:    7,
			PromptTokens:        11,
			PromptCachedTokens:  3,
			CacheCreationTokens: 2,
			CacheReadTokens:     5,
			TotalTokens:         18,
			ServiceTier:         "priority",
		}
		payload, err := completionUsagePayload(usage)
		if err != nil {
			return nil, err
		}
		minimal, err := completionUsagePayloadFromJSON([]byte(`{"completion_tokens":7,"prompt_tokens":11,"total_tokens":18,"service_tier":null}`))
		if err != nil {
			return nil, err
		}
		requiredCases := []struct {
			field   string
			payload string
		}{
			{
				field:   "completion_tokens",
				payload: `{"prompt_tokens":11,"total_tokens":18}`,
			},
			{
				field:   "prompt_tokens",
				payload: `{"completion_tokens":7,"total_tokens":18}`,
			},
			{
				field:   "total_tokens",
				payload: `{"completion_tokens":7,"prompt_tokens":11}`,
			},
		}
		missingFields := make([]string, 0, len(requiredCases))
		for _, test := range requiredCases {
			var decoded lkllm.CompletionUsage
			if err := json.Unmarshal([]byte(test.payload), &decoded); err != nil {
				missingFields = append(missingFields, test.field)
			}
		}
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":    "completion_usage_payload",
					"payload": payload,
				},
				{
					"name":            "completion_usage_required_fields",
					"missing_fields":  missingFields,
					"minimal_payload": minimal,
				},
			},
		}, nil
	case "function_tool_call_payload":
		toolCall := lkllm.FunctionToolCall{
			Type:      "function",
			Name:      "lookup_weather",
			Arguments: `{"city":"Paris"}`,
			CallID:    "call_123",
			Extra:     map[string]any{"provider": "openai"},
		}
		payload, err := functionToolCallPayload(toolCall)
		if err != nil {
			return nil, err
		}
		minimal, err := functionToolCallPayloadFromJSON([]byte(`{"name":"lookup_weather","arguments":"{}","call_id":"call_456"}`))
		if err != nil {
			return nil, err
		}
		requiredCases := []struct {
			field   string
			payload string
		}{
			{
				field:   "name",
				payload: `{"arguments":"{}","call_id":"call_123"}`,
			},
			{
				field:   "arguments",
				payload: `{"name":"lookup_weather","call_id":"call_123"}`,
			},
			{
				field:   "call_id",
				payload: `{"name":"lookup_weather","arguments":"{}"}`,
			},
		}
		missingFields := make([]string, 0, len(requiredCases))
		for _, test := range requiredCases {
			var decoded lkllm.FunctionToolCall
			if err := json.Unmarshal([]byte(test.payload), &decoded); err != nil {
				missingFields = append(missingFields, test.field)
			}
		}
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":    "function_tool_call_payload",
					"payload": payload,
				},
				{
					"name":            "function_tool_call_required_fields",
					"missing_fields":  missingFields,
					"minimal_payload": minimal,
				},
			},
		}, nil
	case "choice_delta_payload":
		delta := lkllm.ChoiceDelta{
			Role:    lkllm.ChatRoleAssistant,
			Content: "hello",
			Extra:   map[string]any{"reasoning": "visible"},
		}
		payload, err := choiceDeltaPayload(delta)
		if err != nil {
			return nil, err
		}
		minimal, err := choiceDeltaPayloadFromJSON([]byte(`{}`))
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":    "choice_delta_payload",
					"payload": payload,
				},
				{
					"name":            "choice_delta_defaults",
					"minimal_payload": minimal,
				},
			},
		}, nil
	case "chat_chunk_payload":
		chunk := lkllm.ChatChunk{
			ID: "chunk_123",
			Delta: &lkllm.ChoiceDelta{
				Role:    lkllm.ChatRoleAssistant,
				Content: "hello",
			},
		}
		payload, err := chatChunkPayload(chunk)
		if err != nil {
			return nil, err
		}
		minimal, err := chatChunkPayloadFromJSON([]byte(`{"id":"chunk_empty"}`))
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":    "chat_chunk_payload",
					"payload": payload,
				},
				{
					"name":            "chat_chunk_defaults",
					"minimal_payload": minimal,
				},
			},
		}, nil
	case "collected_response_payload":
		response := lkllm.CollectedResponse{
			Text: "hello",
			ToolCalls: []lkllm.FunctionToolCall{
				{
					Name:      "lookup_weather",
					Arguments: `{"city":"Paris"}`,
					CallID:    "call_123",
					Extra:     map[string]any{"provider": "openai"},
				},
			},
			Usage: &lkllm.CompletionUsage{
				CompletionTokens: 3,
				PromptTokens:     4,
				TotalTokens:      7,
				ServiceTier:      "priority",
			},
			Extra: map[string]any{"reasoning": "visible"},
		}
		payload, err := collectedResponsePayload(response)
		if err != nil {
			return nil, err
		}
		minimal, err := collectedResponsePayloadFromJSON([]byte(`{}`))
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":    "collected_response_payload",
					"payload": payload,
				},
				{
					"name":            "collected_response_defaults",
					"minimal_payload": minimal,
				},
			},
		}, nil
	case "realtime_error_payload":
		underlying := errors.New("session disconnected")
		err := lkllm.NewRealtimeModelError("openai.RealtimeModel", underlying, false)
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":               "realtime_error_payload",
					"type":               err.Type,
					"label":              err.Label,
					"recoverable":        err.Recoverable,
					"timestamp_positive": err.Timestamp.UnixNano() > 0,
					"error_message":      err.Error(),
				},
			},
		}, nil
	case "realtime_error_message":
		cause := errors.New("timeout")
		messageOnly := lkllm.NewRealtimeError("generation timed out", nil)
		messageCause := lkllm.NewRealtimeError("update chat context failed", cause)
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":      "realtime_error_message",
					"case":      "message_only",
					"error":     messageOnly.Error(),
					"has_cause": errors.Unwrap(messageOnly) != nil,
				},
				{
					"name":      "realtime_error_message",
					"case":      "message_cause",
					"error":     messageCause.Error(),
					"has_cause": errors.Unwrap(messageCause) != nil,
				},
			},
		}, nil
	case "realtime_capabilities":
		caps := lkllm.RealtimeCapabilities{
			MessageTruncation:       true,
			TurnDetection:           true,
			UserTranscription:       true,
			AutoToolReplyGeneration: true,
			AudioOutput:             true,
			ManualFunctionCalls:     true,
			MutableChatContext:      true,
			MutableInstructions:     true,
			MutableTools:            true,
			PerResponseToolChoice:   true,
			SupportsSay:             true,
		}
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":                       "realtime_capabilities",
					"message_truncation":         caps.MessageTruncation,
					"turn_detection":             caps.TurnDetection,
					"user_transcription":         caps.UserTranscription,
					"auto_tool_reply_generation": caps.AutoToolReplyGeneration,
					"audio_output":               caps.AudioOutput,
					"manual_function_calls":      caps.ManualFunctionCalls,
					"mutable_chat_context":       caps.MutableChatContext,
					"mutable_instructions":       caps.MutableInstructions,
					"mutable_tools":              caps.MutableTools,
					"per_response_tool_choice":   caps.PerResponseToolChoice,
					"supports_say":               caps.SupportsSay,
				},
			},
		}, nil
	case "realtime_capabilities_payload":
		caps := lkllm.RealtimeCapabilities{
			MessageTruncation:       true,
			TurnDetection:           true,
			UserTranscription:       true,
			AutoToolReplyGeneration: true,
			AudioOutput:             true,
			ManualFunctionCalls:     true,
			MutableChatContext:      true,
			MutableInstructions:     true,
			MutableTools:            true,
			PerResponseToolChoice:   true,
			SupportsSay:             true,
		}
		data, err := json.Marshal(caps)
		if err != nil {
			return nil, err
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, err
		}
		_, goFieldPresent := payload["MessageTruncation"]
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":             "realtime_capabilities_payload",
					"payload":          payload,
					"go_field_present": goFieldPresent,
				},
			},
		}, nil
	case "realtime_capabilities_required_fields":
		requiredFields := []string{
			"message_truncation",
			"turn_detection",
			"user_transcription",
			"auto_tool_reply_generation",
			"audio_output",
			"manual_function_calls",
		}
		base := map[string]bool{
			"message_truncation":         false,
			"turn_detection":             false,
			"user_transcription":         false,
			"auto_tool_reply_generation": false,
			"audio_output":               false,
			"manual_function_calls":      false,
		}
		missingFields := make([]string, 0, len(requiredFields))
		for _, field := range requiredFields {
			testPayload := make(map[string]bool, len(base)-1)
			for key, value := range base {
				if key != field {
					testPayload[key] = value
				}
			}
			data, err := json.Marshal(testPayload)
			if err != nil {
				return nil, err
			}
			var decoded lkllm.RealtimeCapabilities
			if err := json.Unmarshal(data, &decoded); err != nil {
				missingFields = append(missingFields, field)
			}
		}
		baseData, err := json.Marshal(base)
		if err != nil {
			return nil, err
		}
		var minimal lkllm.RealtimeCapabilities
		if err := json.Unmarshal(baseData, &minimal); err != nil {
			return nil, err
		}
		minimalData, err := json.Marshal(minimal)
		if err != nil {
			return nil, err
		}
		var minimalPayload map[string]any
		if err := json.Unmarshal(minimalData, &minimalPayload); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":            "realtime_capabilities_required_fields",
					"missing_fields":  missingFields,
					"minimal_payload": minimalPayload,
				},
			},
		}, nil
	case "realtime_metadata_defaults":
		model := &fakeScenarioRealtimeModel{}
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":     "realtime_metadata_defaults",
					"model":    lkllm.RealtimeModelName(model),
					"provider": lkllm.RealtimeProvider(model),
				},
			},
		}, nil
	case "realtime_metadata_overrides":
		model := &fakeScenarioRealtimeModel{label: "test.RealtimeModel", model: "realtime-a", provider: "provider-a"}
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":     "realtime_metadata_overrides",
					"label":    lkllm.RealtimeLabel(model),
					"model":    lkllm.RealtimeModelName(model),
					"provider": lkllm.RealtimeProvider(model),
				},
			},
		}, nil
	case "realtime_session_options":
		options := lkllm.RealtimeSessionOptions{
			ToolChoice: map[string]any{
				"type":     "function",
				"function": map[string]any{"name": "lookup"},
			},
		}
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":        "realtime_session_options",
					"tool_choice": options.ToolChoice,
				},
			},
		}, nil
	case "realtime_generate_reply_options":
		options := lkllm.RealtimeGenerateReplyOptions{
			Instructions: "answer briefly",
			ToolChoice:   "none",
			Tools:        []lkllm.Tool{},
		}
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":          "realtime_generate_reply_options",
					"instructions":  options.Instructions,
					"tool_choice":   options.ToolChoice,
					"tools_length":  len(options.Tools),
					"tools_is_list": options.Tools != nil,
				},
			},
		}, nil
	case "realtime_truncate_options":
		transcript := "spoken text"
		options := lkllm.RealtimeTruncateOptions{
			MessageID:       "msg_123",
			Modalities:      []string{"audio"},
			AudioEndMillis:  1500,
			AudioTranscript: &transcript,
		}
		audioTranscript := ""
		if options.AudioTranscript != nil {
			audioTranscript = *options.AudioTranscript
		}
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":             "realtime_truncate_options",
					"message_id":       options.MessageID,
					"modalities":       options.Modalities,
					"audio_end_ms":     options.AudioEndMillis,
					"audio_transcript": audioTranscript,
				},
			},
		}, nil
	case "realtime_video_frame_surface":
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":       "realtime_video_frame_surface",
					"push_video": true,
					"frame_type": "rtc.VideoFrame",
				},
			},
		}, nil
	case "realtime_event_payloads":
		return runLLMRealtimeEventPayloads(payload.Mode)
	default:
		return nil, fmt.Errorf("unsupported LLM value object action %q", payload.Action)
	}
}

func runLLMRealtimeEventPayloads(mode string) (any, error) {
	switch mode {
	case "generation_created":
		messageCh := make(chan lkllm.MessageGeneration, 1)
		functionCh := make(chan *lkllm.FunctionCall, 1)
		textCh := make(chan string, 1)
		audioCh := make(chan *audiomodel.AudioFrame, 1)
		modalitiesCh := make(chan []string, 1)
		messageCh <- lkllm.MessageGeneration{
			MessageID:    "msg_123",
			TextCh:       textCh,
			AudioCh:      audioCh,
			ModalitiesCh: modalitiesCh,
		}
		event := lkllm.RealtimeEvent{
			Type: lkllm.RealtimeEventTypeGenerationCreated,
			Generation: &lkllm.GenerationCreatedEvent{
				MessageCh:     messageCh,
				FunctionCh:    functionCh,
				ResponseID:    "resp_123",
				UserInitiated: true,
			},
		}
		message := <-event.Generation.MessageCh
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":                "realtime_event_payloads",
					"mode":                mode,
					"type":                event.Type,
					"message_id":          message.MessageID,
					"response_id":         event.Generation.ResponseID,
					"user_initiated":      event.Generation.UserInitiated,
					"has_text_stream":     message.TextCh != nil,
					"has_audio_stream":    message.AudioCh != nil,
					"has_modalities":      message.ModalitiesCh != nil,
					"has_function_stream": event.Generation.FunctionCh != nil,
				},
			},
		}, nil
	case "input_transcription":
		confidence := 0.91
		event := lkllm.RealtimeEvent{
			Type: lkllm.RealtimeEventTypeInputAudioTranscriptionCompleted,
			InputTranscription: &lkllm.InputTranscriptionCompleted{
				ItemID:       "item_123",
				ContentIndex: 2,
				Transcript:   "hello",
				IsFinal:      true,
				Confidence:   &confidence,
			},
		}
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":          "realtime_event_payloads",
					"mode":          mode,
					"type":          event.Type,
					"item_id":       event.InputTranscription.ItemID,
					"content_index": event.InputTranscription.ContentIndex,
					"transcript":    event.InputTranscription.Transcript,
					"is_final":      event.InputTranscription.IsFinal,
					"confidence":    *event.InputTranscription.Confidence,
				},
			},
		}, nil
	case "speech_stopped":
		event := lkllm.RealtimeEvent{
			Type: lkllm.RealtimeEventTypeSpeechStopped,
			SpeechStopped: &lkllm.InputSpeechStoppedEvent{
				UserTranscriptionEnabled: true,
			},
		}
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":                       "realtime_event_payloads",
					"mode":                       mode,
					"type":                       event.Type,
					"user_transcription_enabled": event.SpeechStopped.UserTranscriptionEnabled,
				},
			},
		}, nil
	case "output_item_metadata":
		event := lkllm.RealtimeEvent{
			Type:         lkllm.RealtimeEventTypeText,
			ItemID:       "msg_123",
			ContentIndex: 2,
			Text:         "hello",
		}
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":          "realtime_event_payloads",
					"mode":          mode,
					"type":          event.Type,
					"item_id":       event.ItemID,
					"content_index": event.ContentIndex,
					"text":          event.Text,
				},
			},
		}, nil
	case "remote_item_added":
		item := &lkllm.ChatMessage{
			ID:      "msg_123",
			Role:    lkllm.ChatRoleUser,
			Content: []lkllm.ChatContent{{Text: "hello"}},
		}
		event := lkllm.RealtimeEvent{
			Type: lkllm.RealtimeEventTypeRemoteItemAdded,
			RemoteItem: &lkllm.RemoteItemAddedEvent{
				PreviousItemID: "prev_123",
				Item:           item,
			},
		}
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":             "realtime_event_payloads",
					"mode":             mode,
					"type":             event.Type,
					"previous_item_id": event.RemoteItem.PreviousItemID,
					"item_id":          event.RemoteItem.Item.GetID(),
				},
			},
		}, nil
	case "session_reconnected":
		event := lkllm.RealtimeEvent{
			Type:      lkllm.RealtimeEventTypeSessionReconnected,
			Reconnect: &lkllm.RealtimeSessionReconnectedEvent{},
		}
		return map[string]any{
			"contract": "llm-value-objects",
			"events": []map[string]any{
				{
					"name":        "realtime_event_payloads",
					"mode":        mode,
					"type":        event.Type,
					"has_payload": event.Reconnect != nil,
				},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported realtime event payload mode %q", mode)
	}
}

func runLLMFunctionArguments(input json.RawMessage) (any, error) {
	var payload struct {
		Raw string `json:"raw"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Raw == "" {
		payload.Raw = `{"city":"Paris","limit":3}`
	}
	parsed, err := lkllm.ParseFunctionArguments(payload.Raw)
	if err != nil {
		event := map[string]any{
			"name":          "parse_function_arguments",
			"raw":           payload.Raw,
			"error":         true,
			"error_message": err.Error(),
			"error_class":   "ValueError",
		}
		if payload.Raw == "<|im_end|>" {
			event["error_message"] = ""
			event["error_prefix"] = strings.HasPrefix(err.Error(), "could not parse function arguments as JSON: ")
			event["error_suffix"] = strings.HasSuffix(err.Error(), ": "+payload.Raw)
		}
		return map[string]any{
			"contract": "llm-function-arguments",
			"events":   []map[string]any{event},
		}, nil
	}
	return map[string]any{
		"contract": "llm-function-arguments",
		"events": []map[string]any{
			{
				"name":   "parse_function_arguments",
				"raw":    payload.Raw,
				"error":  false,
				"parsed": parsed,
			},
		},
	}, nil
}

func runLLMImageSerialization(input json.RawMessage) (any, error) {
	var payload struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Action == "" {
		payload.Action = "unsupported_type"
	}
	var image *lkllm.ImageContent
	switch payload.Action {
	case "unsupported_type":
		image = &lkllm.ImageContent{Image: 42}
	case "unsupported_mime":
		image = &lkllm.ImageContent{Image: "data:image/bmp;base64,AA=="}
	default:
		return nil, fmt.Errorf("unsupported LLM image serialization action %q", payload.Action)
	}
	_, err := lkllm.SerializeImage(image)
	if err != nil {
		return map[string]any{
			"contract": "llm-image-serialization",
			"events": []map[string]any{
				{
					"name":          "serialize_image",
					"action":        payload.Action,
					"error":         true,
					"error_message": err.Error(),
					"error_class":   "ValueError",
				},
			},
		}, nil
	}
	return map[string]any{
		"contract": "llm-image-serialization",
		"events": []map[string]any{
			{"name": "serialize_image", "action": payload.Action, "error": false},
		},
	}, nil
}

func runLLMFunctionOutput(input json.RawMessage) (any, error) {
	var payload struct {
		Action    string          `json:"action"`
		Arguments string          `json:"arguments"`
		Output    json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Action == "" {
		payload.Action = "tool_error"
	}
	if payload.Arguments == "" {
		payload.Arguments = "{}"
	}
	call := lkllm.FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: payload.Arguments}
	if payload.Action == "stringifies_valid_outputs" {
		cases := []struct {
			name   string
			output any
		}{
			{name: "integer", output: 7},
			{name: "positive infinity", output: math.Inf(1)},
			{name: "negative infinity", output: math.Inf(-1)},
			{name: "exponent float", output: 1e20},
			{name: "true", output: true},
			{name: "complex", output: complex(1, 2)},
			{name: "complex positive infinity", output: complex(math.Inf(1), 2)},
			{name: "complex imaginary infinity", output: complex(1, math.Inf(1))},
			{name: "complex nan", output: complex(math.NaN(), 2)},
			{name: "complex negative zero imaginary", output: complex(1, math.Copysign(0, -1))},
			{name: "complex negative zero real", output: complex(math.Copysign(0, -1), 2)},
			{name: "list", output: []any{1, "x", true}},
			{name: "list floats", output: []any{0.0, math.Copysign(0, -1), 1.0, 1.5}},
			{name: "list exponent floats", output: []any{1e20, 1e-7, 1e-5}},
			{name: "list string newline", output: []any{"line\nnext"}},
			{name: "list string apostrophe", output: []any{"can't"}},
			{name: "list string nul", output: []any{"\x00"}},
			{name: "list string backspace", output: []any{"\b"}},
			{name: "list string escape", output: []any{"\x1b"}},
			{name: "list string next line", output: []any{"\u0085"}},
			{name: "list string line separator", output: []any{"\u2028"}},
			{name: "list string non-ascii printable", output: []any{"é"}},
			{name: "tuple", output: [3]any{1, "x", true}},
			{name: "singleton tuple", output: [1]any{1}},
			{name: "dict", output: map[string]any{"ok": true}},
			{name: "dict float", output: map[string]any{"score": 1.0}},
		}
		events := make([]map[string]any, 0, len(cases))
		for _, tc := range cases {
			result := lkllm.MakeFunctionCallOutput(call, tc.output, nil)
			event := map[string]any{
				"name":               "function_output_stringify",
				"case":               tc.name,
				"has_output":         result.FncCallOut != nil,
				"output":             "",
				"is_error":           false,
				"raw_output_present": result.RawOutput != nil,
			}
			if result.FncCallOut != nil {
				event["output"] = result.FncCallOut.Output
				event["is_error"] = result.FncCallOut.IsError
			}
			events = append(events, event)
		}
		return map[string]any{
			"contract": "llm-function-output",
			"events":   events,
		}, nil
	}
	var output any
	var exception error
	switch payload.Action {
	case "tool_error":
		exception = lkllm.NewToolError("visible failure")
	case "visible_output":
		output = "Paris"
	case "visible_tool_output":
		output = "Paris"
	case "stop_response":
		exception = lkllm.StopResponse{}
	case "internal_error":
		exception = errors.New("database password leaked")
	case "falsy_output":
		if len(payload.Output) == 0 {
			output = false
		} else if err := json.Unmarshal(payload.Output, &output); err != nil {
			return nil, err
		}
	case "timestamp_output":
		output = "Paris"
	case "invalid_structured":
		output = map[string]any{"bad": func() {}}
	default:
		return nil, fmt.Errorf("unsupported LLM function output action %q", payload.Action)
	}
	result := lkllm.MakeFunctionCallOutput(call, output, exception)
	if payload.Action == "visible_tool_output" {
		result = lkllm.MakeToolOutput(call, output, exception)
	}
	event := map[string]any{
		"name":               "function_output",
		"action":             payload.Action,
		"call_id":            result.FncCall.CallID,
		"function_name":      result.FncCall.Name,
		"has_output":         result.FncCallOut != nil,
		"raw_output_present": result.RawOutput != nil,
		"raw_error_present":  result.RawError != nil,
	}
	if result.FncCallOut != nil {
		event["output_call_id"] = result.FncCallOut.CallID
		event["output_name"] = result.FncCallOut.Name
		event["output"] = result.FncCallOut.Output
		event["is_error"] = result.FncCallOut.IsError
		event["created_at_present"] = !result.FncCallOut.CreatedAt.IsZero()
	}
	return map[string]any{
		"contract": "llm-function-output",
		"events":   []map[string]any{event},
	}, nil
}

func runLLMThinkingTokens(input json.RawMessage) (any, error) {
	var payload struct {
		Values []string `json:"values"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Values == nil {
		payload.Values = []string{"hello", "<think>", "hidden reasoning", "</think>visible"}
	}
	thinking := false
	events := make([]map[string]any, 0, len(payload.Values))
	for _, value := range payload.Values {
		output, ok := lkllm.StripThinkingTokens(value, &thinking)
		events = append(events, map[string]any{
			"name":           "strip_thinking_tokens",
			"input":          value,
			"output_present": ok,
			"output":         output,
			"thinking":       thinking,
		})
	}
	return map[string]any{
		"contract": "llm-thinking-tokens",
		"events":   events,
	}, nil
}

func runLLMToolContext(input json.RawMessage) (any, error) {
	var payload struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Action == "" {
		payload.Action = "empty"
	}
	summary := func(ctx *lkllm.ToolContext, name string, extra map[string]any) map[string]any {
		functionNames := make([]string, 0, len(ctx.FunctionTools()))
		providerNames := make([]string, 0, len(ctx.ProviderTools()))
		for _, tool := range ctx.ProviderTools() {
			providerNames = append(providerNames, tool.ID())
		}
		flattenNames := make([]string, 0, len(ctx.Flatten()))
		for _, tool := range ctx.Flatten() {
			flattenNames = append(flattenNames, tool.ID())
			if _, ok := tool.(lkllm.ProviderTool); !ok {
				functionNames = append(functionNames, tool.ID())
			}
		}
		event := map[string]any{
			"name":           name,
			"function_count": len(ctx.FunctionTools()),
			"provider_count": len(ctx.ProviderTools()),
			"toolset_count":  len(ctx.Toolsets()),
			"function_names": functionNames,
			"provider_names": providerNames,
			"flatten_names":  flattenNames,
		}
		for key, value := range extra {
			event[key] = value
		}
		return event
	}
	newTool := func(id, name string) *scenarioLLMTool {
		return &scenarioLLMTool{id: id, name: name}
	}
	switch payload.Action {
	case "empty":
		var receiver lkllm.ToolContext
		ctx := receiver.Empty()
		tool := newTool("lookup", "lookup")
		if err := ctx.AddTool(tool); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "llm-tool-context",
			"events": []map[string]any{
				summary(ctx, "empty", map[string]any{"lookup_found": ctx.GetFunctionTool("lookup") == tool}),
			},
		}, nil
	case "duplicate_constructor":
		var errMsg string
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					if err, ok := recovered.(error); ok {
						errMsg = err.Error()
					} else {
						errMsg = fmt.Sprint(recovered)
					}
				}
			}()
			lkllm.NewToolContext([]interface{}{newTool("lookup-a", "lookup"), newTool("lookup-b", "lookup")})
		}()
		return map[string]any{
			"contract": "llm-tool-context",
			"events": []map[string]any{
				{"name": "duplicate_constructor", "error": errMsg != "", "error_message": errMsg},
			},
		}, nil
	case "unknown_tool_type":
		var errMsg string
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					if err, ok := recovered.(error); ok {
						errMsg = err.Error()
					} else {
						errMsg = fmt.Sprint(recovered)
					}
				}
			}()
			lkllm.NewToolContext([]interface{}{"not-a-tool"})
		}()
		return map[string]any{
			"contract": "llm-tool-context",
			"events": []map[string]any{
				{"name": "unknown_tool_type", "error": errMsg != "", "error_contains_unknown": strings.Contains(errMsg, "unknown tool type")},
			},
		}, nil
	case "update_same_instance":
		tool := newTool("lookup", "lookup")
		toolset := &scenarioLLMToolset{id: "tools", tools: []lkllm.Tool{tool}}
		ctx := lkllm.EmptyToolContext()
		if err := ctx.UpdateTools([]interface{}{tool, toolset}); err != nil {
			return nil, err
		}
		return map[string]any{"contract": "llm-tool-context", "events": []map[string]any{summary(ctx, "update_same_instance", nil)}}, nil
	case "update_duplicate":
		ctx := lkllm.EmptyToolContext()
		err := ctx.UpdateTools([]interface{}{newTool("lookup-a", "lookup"), newTool("lookup-b", "lookup")})
		message := ""
		if err != nil {
			message = err.Error()
		}
		return map[string]any{"contract": "llm-tool-context", "events": []map[string]any{{"name": "update_duplicate", "error": err != nil, "error_message": message}}}, nil
	case "add_duplicate":
		ctx := lkllm.NewToolContext([]interface{}{newTool("lookup-a", "lookup")})
		err := ctx.AddTool(newTool("lookup-b", "lookup"))
		message := ""
		if err != nil {
			message = err.Error()
		}
		return map[string]any{"contract": "llm-tool-context", "events": []map[string]any{{"name": "add_duplicate", "error": err != nil, "error_message": message}}}, nil
	case "equal_identity":
		lookup := newTool("lookup", "lookup")
		provider := &scenarioLLMProviderTool{scenarioLLMTool: scenarioLLMTool{id: "provider", name: "provider"}}
		left := lkllm.NewToolContext([]interface{}{lookup, provider})
		right := lkllm.NewToolContext([]interface{}{provider, lookup})
		other := lkllm.NewToolContext([]interface{}{newTool("lookup-other", "lookup"), provider})
		return map[string]any{
			"contract": "llm-tool-context",
			"events": []map[string]any{
				{"name": "equal_identity", "same_identity_equal": left.Equal(right), "different_function_equal": left.Equal(other)},
			},
		}, nil
	case "flatten_function_order":
		ctx := lkllm.NewToolContext([]interface{}{
			newTool("zeta", "zeta"),
			newTool("alpha", "alpha"),
			newTool("middle", "middle"),
		})
		return map[string]any{
			"contract": "llm-tool-context",
			"events":   []map[string]any{summary(ctx, "flatten_function_order", nil)},
		}, nil
	case "flatten_provider_order":
		ctx := lkllm.NewToolContext([]interface{}{
			&scenarioLLMProviderTool{scenarioLLMTool: scenarioLLMTool{id: "zeta-provider", name: "zeta-provider"}},
			newTool("lookup", "lookup"),
			&scenarioLLMProviderTool{scenarioLLMTool: scenarioLLMTool{id: "alpha-provider", name: "alpha-provider"}},
		})
		return map[string]any{
			"contract": "llm-tool-context",
			"events":   []map[string]any{summary(ctx, "flatten_provider_order", nil)},
		}, nil
	case "sync_flattened":
		lookup := newTool("lookup", "lookup")
		weather := newTool("weather", "weather")
		replacement := newTool("replacement", "replacement")
		toolset := &scenarioLLMToolset{id: "tools", tools: []lkllm.Tool{lookup, weather}}
		ctx := lkllm.NewToolContext([]interface{}{toolset})
		if err := ctx.SyncFlattened([]lkllm.Tool{weather, replacement}); err != nil {
			return nil, err
		}
		toolsets := ctx.Toolsets()
		return map[string]any{
			"contract": "llm-tool-context",
			"events": []map[string]any{
				summary(ctx, "sync_flattened", map[string]any{
					"lookup_found":      ctx.GetFunctionTool("lookup") != nil,
					"weather_found":     ctx.GetFunctionTool("weather") == weather,
					"replacement_found": ctx.GetFunctionTool("replacement") == replacement,
					"toolset_preserved": len(toolsets) == 1 && toolsets[0] == toolset,
				}),
			},
		}, nil
	case "close_toolsets":
		lookup := newTool("lookup", "lookup")
		toolset := &scenarioLLMToolset{id: "tools", tools: []lkllm.Tool{lookup}}
		ctx := lkllm.NewToolContext([]interface{}{toolset})
		if err := ctx.Close(); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "llm-tool-context",
			"events": []map[string]any{
				summary(ctx, "close_toolsets", map[string]any{"close_calls": toolset.closeCalls}),
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported LLM tool context action %q", payload.Action)
	}
}

type scenarioLLMTool struct {
	id   string
	name string
}

func (t *scenarioLLMTool) ID() string                 { return t.id }
func (t *scenarioLLMTool) Name() string               { return t.name }
func (t *scenarioLLMTool) Description() string        { return "" }
func (t *scenarioLLMTool) Parameters() map[string]any { return nil }
func (t *scenarioLLMTool) Execute(context.Context, string) (string, error) {
	return "", nil
}

type scenarioLLMProviderTool struct {
	scenarioLLMTool
}

func (t *scenarioLLMProviderTool) IsProviderTool() bool { return true }

func completionUsagePayload(usage lkllm.CompletionUsage) (map[string]any, error) {
	data, err := json.Marshal(usage)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func completionUsagePayloadFromJSON(data []byte) (map[string]any, error) {
	var usage lkllm.CompletionUsage
	if err := json.Unmarshal(data, &usage); err != nil {
		return nil, err
	}
	return completionUsagePayload(usage)
}

func functionToolCallPayload(toolCall lkllm.FunctionToolCall) (map[string]any, error) {
	data, err := json.Marshal(toolCall)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func functionToolCallPayloadFromJSON(data []byte) (map[string]any, error) {
	var toolCall lkllm.FunctionToolCall
	if err := json.Unmarshal(data, &toolCall); err != nil {
		return nil, err
	}
	return functionToolCallPayload(toolCall)
}

func choiceDeltaPayload(delta lkllm.ChoiceDelta) (map[string]any, error) {
	data, err := json.Marshal(delta)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func choiceDeltaPayloadFromJSON(data []byte) (map[string]any, error) {
	var delta lkllm.ChoiceDelta
	if err := json.Unmarshal(data, &delta); err != nil {
		return nil, err
	}
	return choiceDeltaPayload(delta)
}

func chatChunkPayload(chunk lkllm.ChatChunk) (map[string]any, error) {
	data, err := json.Marshal(chunk)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func chatChunkPayloadFromJSON(data []byte) (map[string]any, error) {
	var chunk lkllm.ChatChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return nil, err
	}
	return chatChunkPayload(chunk)
}

func collectedResponsePayload(response lkllm.CollectedResponse) (map[string]any, error) {
	data, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func collectedResponsePayloadFromJSON(data []byte) (map[string]any, error) {
	var response lkllm.CollectedResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	return collectedResponsePayload(response)
}

type scenarioLLMToolset struct {
	id         string
	tools      []lkllm.Tool
	closeCalls int
}

func (s *scenarioLLMToolset) ID() string          { return s.id }
func (s *scenarioLLMToolset) Tools() []lkllm.Tool { return s.tools }
func (s *scenarioLLMToolset) Close() error {
	s.closeCalls++
	return nil
}

type fakeScenarioLLM struct {
	label        string
	model        string
	provider     string
	prewarmCalls int
}

func (f *fakeScenarioLLM) Chat(context.Context, *lkllm.ChatContext, ...lkllm.ChatOption) (lkllm.LLMStream, error) {
	return nil, errors.New("chat unsupported")
}

func (f *fakeScenarioLLM) Label() string {
	return f.label
}

func (f *fakeScenarioLLM) Model() string {
	return f.model
}

func (f *fakeScenarioLLM) Provider() string {
	return f.provider
}

func (f *fakeScenarioLLM) Prewarm() {
	f.prewarmCalls++
}

type fakeScenarioRealtimeModel struct {
	label    string
	model    string
	provider string
}

func (f *fakeScenarioRealtimeModel) Capabilities() lkllm.RealtimeCapabilities {
	return lkllm.RealtimeCapabilities{}
}

func (f *fakeScenarioRealtimeModel) Label() string {
	return f.label
}

func (f *fakeScenarioRealtimeModel) Model() string {
	return f.model
}

func (f *fakeScenarioRealtimeModel) Provider() string {
	return f.provider
}

func (f *fakeScenarioRealtimeModel) Session() (lkllm.RealtimeSession, error) {
	return nil, errors.New("session unsupported")
}

func (f *fakeScenarioRealtimeModel) Close() error {
	return nil
}
