package aistudio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// interactionThinkingLevels maps GenerateContent thinking level to Interaction enum
var interactionThinkingLevels = map[int64]int64{4: 1, 1: 2, 2: 3, 3: 4}

// interactionVideoMIME represents Interaction video MIME enum
var interactionVideoMIME = map[int64]string{1: "video/mp4"}

// interactionStatusHTTP maps trailing google.rpc status code to HTTP status
var interactionStatusHTTP = map[int64]int{
	3: http.StatusBadRequest, 4: http.StatusGatewayTimeout, 5: http.StatusNotFound, 7: http.StatusForbidden,
	8: http.StatusTooManyRequests, 9: http.StatusBadRequest, 13: http.StatusInternalServerError,
	14: http.StatusServiceUnavailable, 16: http.StatusUnauthorized,
}

// EncodeCreateInteractionStreamRequest encodes official CreateInteractionStream request and returns text content participating in WAA binding
func EncodeCreateInteractionStreamRequest(request GenerateRequest, defaults GenerationDefaults) ([]byte, []Content, error) {
	if len(request.Config.StopSequences) > 0 {
		return nil, nil, fmt.Errorf("Interaction models do not support stop sequences")
	}
	steps, binding, hasModelTurn, err := encodeInteractionSteps(request.Contents)
	if err != nil {
		return nil, nil, err
	}
	level, err := interactionThinkingLevel(request.Config, defaults)
	if err != nil {
		return nil, nil, err
	}
	maxOutput := defaults.MaxOutputTokens
	if request.Config.MaxOutputTokens != nil {
		maxOutput = *request.Config.MaxOutputTokens
	}
	if maxOutput <= 0 || maxOutput > defaults.MaxOutputTokens {
		return nil, nil, fmt.Errorf("max output tokens %d exceeds model range 1-%d", maxOutput, defaults.MaxOutputTokens)
	}
	// GenerationConfig: field 6 thinking level, field 7 thinking summaries, field 8 max output tokens
	config := make([]any, 8)
	config[5] = level
	config[6] = int64(1)
	config[7] = maxOutput
	if !hasModelTurn {
		config = append(config, make([]any, 17)...)
		config[24] = []any{}
	}
	interaction := make([]any, 54)
	if system := strings.TrimSpace(request.System); system != "" {
		interaction[6] = system
	}
	interaction[17] = []any{wireModelName(request.Model), config}
	interaction[26] = []any{steps}
	// field 54 video output config: official site default output resolution enum 1
	interaction[53] = []any{[]any{[]any{nil, nil, nil, []any{nil, nil, nil, nil, nil, int64(1)}}}}
	body, err := json.Marshal([]any{int64(1), int64(1), nil, interaction, nil, int64(1)})
	if err != nil {
		return nil, nil, fmt.Errorf("encode CreateInteractionStream: %w", err)
	}
	return body, binding, nil
}

// encodeInteractionSteps encodes canonical messages into Interaction input steps; user is step field 1, model is field 2
func encodeInteractionSteps(contents []Content) ([]any, []Content, bool, error) {
	steps := make([]any, 0, len(contents))
	binding := make([]Content, 0, len(contents))
	hasModelTurn := false
	for index, content := range contents {
		parts := make([]any, 0, len(content.Parts))
		texts := make([]Part, 0, len(content.Parts))
		for _, part := range content.Parts {
			switch {
			case part.Thought || part.Text == "" && part.ThoughtSignature != "" && part.InlineData == nil && part.File == nil:
				continue
			case part.Text != "" && part.InlineData == nil && part.File == nil && part.FunctionCall == nil && part.FunctionResult == nil:
				parts = append(parts, []any{[]any{part.Text}})
				texts = append(texts, Part{Text: part.Text})
			case part.File != nil && strings.TrimSpace(part.File.ID) != "" && part.Text == "" && content.Role == RoleUser:
				// Content field 9 is Drive file reference
				parts = append(parts, []any{nil, nil, nil, nil, nil, nil, nil, nil, []any{strings.TrimSpace(part.File.ID)}})
			case part.InlineData != nil:
				return nil, nil, false, fmt.Errorf("Interaction model contents[%d] attachment requires account Drive authorization", index)
			default:
				return nil, nil, false, fmt.Errorf("Interaction model contents[%d] only accepts text and user attachment parts", index)
			}
		}
		if len(parts) == 0 {
			continue
		}
		switch content.Role {
		case RoleUser:
			steps = append(steps, []any{[]any{parts}})
		case RoleAssistant:
			steps = append(steps, []any{nil, []any{parts}})
			hasModelTurn = true
		default:
			return nil, nil, false, fmt.Errorf("Interaction model does not accept %s messages", content.Role)
		}
		binding = append(binding, Content{Role: content.Role, Parts: texts})
	}
	if len(steps) == 0 {
		return nil, nil, false, fmt.Errorf("CreateInteractionStream contents cannot be empty")
	}
	return steps, binding, hasModelTurn, nil
}

// interactionThinkingLevel selects supported Interaction thinking level based on requested effort or budget
func interactionThinkingLevel(config GenerationConfig, defaults GenerationDefaults) (int64, error) {
	level := defaults.DefaultThinkingLevel
	switch strings.ToLower(strings.TrimSpace(config.ReasoningEffort)) {
	case "":
		if config.ThinkingBudget != nil {
			level = thinkingLevelForBudget(*config.ThinkingBudget)
		}
	case "minimal", "none":
		level = 4
	case "low":
		level = 1
	case "medium":
		level = 2
	case "high":
		level = 3
	default:
		return 0, fmt.Errorf("reasoning effort must be none, minimal, low, medium, or high")
	}
	level = closestSupportedThinkingLevel(level, defaults.ThinkingLevels)
	wire, ok := interactionThinkingLevels[level]
	if !ok {
		return 0, fmt.Errorf("unrecognized thinking level %d", level)
	}
	return wire, nil
}

// generateInteraction generates via CreateInteractionStream and maps to canonical events
func (c *Client) generateInteraction(ctx context.Context, request GenerateRequest, entry modelEntry) (<-chan Event, error) {
	body, binding, err := EncodeCreateInteractionStreamRequest(request, entry.defaults)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	rpc := newRPCRequest("CreateInteractionStream", request.AccountID, request.ID, body, true)
	c.applyBenefitTier(rpc.Method, request.AccountID, rpc.Header)
	bindingRequest := request
	bindingRequest.Contents = binding
	response, err := c.protected.DoProtected(ctx, bindingRequest, rpc)
	if err != nil {
		return nil, fmt.Errorf("send AI Studio CreateInteractionStream: %w", err)
	}
	response, err = validateRPCResponse("CreateInteractionStream", response)
	if err != nil {
		return nil, err
	}
	events := make(chan Event, 8)
	ready := make(chan error, 1)
	go func() {
		defer close(events)
		stopClose := context.AfterFunc(ctx, func() {
			_ = response.Body.Close()
		})
		defer stopClose()
		committed := false
		send := func(event Event) error {
			if !committed {
				committed = true
				ready <- nil
			}
			event.ProviderModel = entry.model.ID
			select {
			case events <- event:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		err := DecodeInteractionStream(observeStreamActivity(ctx, response.Body), send)
		if closeErr := response.Body.Close(); err == nil && ctx.Err() == nil {
			err = closeErr
		}
		if err == nil || ctx.Err() != nil {
			if !committed {
				ready <- ctx.Err()
			}
			return
		}
		if !committed {
			ready <- err
			return
		}
		_ = send(Event{Kind: EventError, Err: err})
	}()
	select {
	case err := <-ready:
		if err != nil {
			for range events {
			}
			return nil, err
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return events, nil
}

// DecodeInteractionStream decodes CreateInteractionStream events and trailer status in network order
func DecodeInteractionStream(source io.Reader, emit func(Event) error) error {
	decoder := json.NewDecoder(newSparseJSONReader(source))
	decoder.UseNumber()
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return &ProtocolEvidenceError{Method: "CreateInteractionStream", Path: "$", Detail: "root value is not array"}
	}
	if !decoder.More() {
		return &ProtocolEvidenceError{Method: "CreateInteractionStream", Path: "$", Detail: "root array missing event list"}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return &ProtocolEvidenceError{Method: "CreateInteractionStream", Path: "$[0]", Detail: "event list is not array"}
	}
	finished := false
	for index := 0; decoder.More(); index++ {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return &ProtocolEvidenceError{Method: "CreateInteractionStream", Path: fmt.Sprintf("$[0][%d]", index), Detail: err.Error()}
		}
		done, err := decodeInteractionEvent(raw, emit)
		if err != nil {
			return err
		}
		finished = finished || done
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		return &ProtocolEvidenceError{Method: "CreateInteractionStream", Path: "$[0]", Detail: "event list did not end properly"}
	}
	trailing := make([]json.RawMessage, 0, 3)
	for decoder.More() {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return &ProtocolEvidenceError{Method: "CreateInteractionStream", Path: "$", Detail: err.Error()}
		}
		trailing = append(trailing, raw)
	}
	if len(trailing) > 0 {
		// Trailing field 2 is google.rpc.Status: [code, message, details]
		status, err := rawArray(trailing[0], "$[1]", trailing[0])
		if err != nil || len(status) == 0 {
			return &ProtocolEvidenceError{Method: "CreateInteractionStream", Path: "$[1]", Detail: "trailer status invalid", Raw: cloneRaw(trailing[0])}
		}
		code, err := rawInt64(status[0], "$[1][0]", trailing[0])
		if err != nil {
			return withMethod(err, "CreateInteractionStream")
		}
		if code != 0 {
			rpcError := &RPCError{Method: "CreateInteractionStream", StatusCode: http.StatusBadGateway, Code: code}
			if status, ok := interactionStatusHTTP[code]; ok {
				rpcError.StatusCode = status
			}
			if len(status) > 1 {
				rpcError.Message, _ = rawString(status[1], "$[1][1]", trailing[0])
			}
			return rpcError
		}
	}
	if !finished {
		return &ProtocolEvidenceError{Method: "CreateInteractionStream", Path: "$", Detail: "stream ended without final interaction"}
	}
	return nil
}

// decodeInteractionEvent decodes a single stream event, returning whether it is the final interaction
func decodeInteractionEvent(raw json.RawMessage, emit func(Event) error) (bool, error) {
	event, err := rawArray(raw, "$event", raw)
	if err != nil {
		return false, withMethod(err, "CreateInteractionStream")
	}
	if delta := rawAt(event, 10); !isJSONNull(delta) {
		return false, decodeInteractionDelta(delta, emit)
	}
	final := rawAt(event, 19)
	if isJSONNull(final) {
		final = rawAt(event, 1)
	}
	if isJSONNull(final) {
		return false, nil
	}
	wrapper, err := rawArray(final, "$event[19]", raw)
	if err != nil || len(wrapper) == 0 {
		return false, &ProtocolEvidenceError{Method: "CreateInteractionStream", Path: "$event[19]", Detail: "final interaction is empty", Raw: cloneRaw(raw)}
	}
	interaction, err := rawArray(wrapper[0], "$event[19][0]", raw)
	if err != nil {
		return false, withMethod(err, "CreateInteractionStream")
	}
	status, err := optionalIntField(interaction, 1, "$event[19][0]", raw)
	if err != nil {
		return false, withMethod(err, "CreateInteractionStream")
	}
	switch status {
	case 3:
	case 4:
		return false, &RPCError{Method: "CreateInteractionStream", StatusCode: http.StatusBadGateway, Message: "interaction failed"}
	case 5:
		return false, &RPCError{Method: "CreateInteractionStream", StatusCode: http.StatusBadGateway, Message: "interaction cancelled"}
	default:
		return false, &ProtocolEvidenceError{Method: "CreateInteractionStream", Path: "$event[19][0][1]", Detail: fmt.Sprintf("unrecognized final status %d", status), Raw: cloneRaw(raw)}
	}
	if usage := rawAt(interaction, 12); !isJSONNull(usage) {
		decoded, err := decodeInteractionUsage(usage, raw)
		if err != nil {
			return false, err
		}
		if err := emit(Event{Kind: EventUsage, Usage: decoded}); err != nil {
			return false, err
		}
	}
	return true, emit(Event{Kind: EventFinish, FinishReason: "stop"})
}

// decodeInteractionDelta decodes content delta: field 1 text, field 5 video, field 6 thinking summaries, field 7 thought signature
func decodeInteractionDelta(raw json.RawMessage, emit func(Event) error) error {
	delta, err := rawArray(raw, "$delta", raw)
	if err != nil || len(delta) < 2 {
		return &ProtocolEvidenceError{Method: "CreateInteractionStream", Path: "$delta", Detail: "content delta fields insufficient", Raw: cloneRaw(raw)}
	}
	content, err := rawArray(delta[1], "$delta[1]", raw)
	if err != nil {
		return withMethod(err, "CreateInteractionStream")
	}
	for index, value := range content {
		if isJSONNull(value) {
			continue
		}
		switch index {
		case 0:
			text, err := rawArray(value, "$delta[1][0]", raw)
			if err != nil || len(text) == 0 {
				return &ProtocolEvidenceError{Method: "CreateInteractionStream", Path: "$delta[1][0]", Detail: "text delta invalid", Raw: cloneRaw(raw)}
			}
			decoded, err := rawString(text[0], "$delta[1][0][0]", raw)
			if err != nil {
				return withMethod(err, "CreateInteractionStream")
			}
			if err := emit(Event{Kind: EventText, Text: decoded}); err != nil {
				return err
			}
		case 4:
			video, err := rawArray(value, "$delta[1][4]", raw)
			if err != nil || len(video) < 2 {
				return &ProtocolEvidenceError{Method: "CreateInteractionStream", Path: "$delta[1][4]", Detail: "video delta invalid", Raw: cloneRaw(raw)}
			}
			code, err := rawInt64(video[0], "$delta[1][4][0]", raw)
			if err != nil {
				return withMethod(err, "CreateInteractionStream")
			}
			mimeType, ok := interactionVideoMIME[code]
			if !ok {
				return &ProtocolEvidenceError{Method: "CreateInteractionStream", Path: "$delta[1][4][0]", Detail: fmt.Sprintf("unrecognized video MIME enum %d", code), Raw: cloneRaw(raw)}
			}
			encoded, err := rawString(video[1], "$delta[1][4][1]", raw)
			if err != nil {
				return withMethod(err, "CreateInteractionStream")
			}
			data, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return &ProtocolEvidenceError{Method: "CreateInteractionStream", Path: "$delta[1][4][1]", Detail: "video data is not Base64", Raw: cloneRaw(raw)}
			}
			if err := emit(Event{Kind: EventMedia, Media: &Media{MIME: mimeType, Data: data}}); err != nil {
				return err
			}
		case 5:
			text := strings.Join(interactionStrings(value), "")
			if text != "" {
				if err := emit(Event{Kind: EventReasoning, Text: text}); err != nil {
					return err
				}
			}
		case 6:
		default:
			return &ProtocolEvidenceError{Method: "CreateInteractionStream", Path: fmt.Sprintf("$delta[1][%d]", index), Detail: "unrecognized content delta field", Raw: cloneRaw(raw)}
		}
	}
	return nil
}

// interactionStrings collects all strings in nested arrays in order
func interactionStrings(raw json.RawMessage) []string {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	var values []string
	var walk func(any)
	walk = func(item any) {
		switch typed := item.(type) {
		case string:
			values = append(values, typed)
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	return values
}

// decodeInteractionUsage decodes Usage: prompt, candidate, thoughts, and total tokens at indices 0, 4, 8, 9
func decodeInteractionUsage(raw json.RawMessage, evidence json.RawMessage) (*Usage, error) {
	values, err := rawArray(raw, "$usage", evidence)
	if err != nil {
		return nil, withMethod(err, "CreateInteractionStream")
	}
	usage := &Usage{}
	for _, field := range []struct {
		index  int
		target *int64
	}{{0, &usage.InputTokens}, {4, &usage.OutputTokens}, {8, &usage.ReasoningTokens}, {9, &usage.TotalTokens}} {
		value, err := optionalIntField(values, field.index, "$usage", evidence)
		if err != nil {
			return nil, withMethod(err, "CreateInteractionStream")
		}
		*field.target = value
	}
	if usage.TotalTokens == 0 {
		return nil, errors.New("CreateInteractionStream usage missing total tokens")
	}
	return usage, nil
}
