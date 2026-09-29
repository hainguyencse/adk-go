// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	anthropicapi "github.com/anthropics/anthropic-sdk-go"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/genai"
)

func toLLMResponse(message *anthropicapi.Message, cfg *genai.GenerateContentConfig) (*adkmodel.LLMResponse, error) {
	if message == nil {
		return nil, fmt.Errorf("empty Claude response")
	}
	content := &genai.Content{Role: "model"}
	var outputText strings.Builder
	hasToolCall := false
	for _, block := range message.Content {
		switch block.Type {
		case "text":
			content.Parts = append(content.Parts, genai.NewPartFromText(block.Text))
			outputText.WriteString(block.Text)
		case "thinking":
			content.Parts = append(content.Parts, &genai.Part{
				Text: block.Thinking, Thought: true, ThoughtSignature: []byte(block.Signature),
			})
		case "redacted_thinking":
			content.Parts = append(content.Parts, &genai.Part{
				Thought: true, ThoughtSignature: []byte(redactedSignaturePrefix + block.Data),
			})
		case "tool_use":
			if block.ID == "" {
				return nil, fmt.Errorf("Claude returned tool call %q without ID", block.Name)
			}
			var args map[string]any
			if err := json.Unmarshal(block.Input, &args); err != nil {
				return nil, fmt.Errorf("decode Claude tool %q arguments: %w", block.Name, err)
			}
			if args == nil {
				args = map[string]any{}
			}
			content.Parts = append(content.Parts, &genai.Part{FunctionCall: &genai.FunctionCall{
				ID: block.ID, Name: block.Name, Args: args,
			}})
			hasToolCall = true
		default:
			return nil, fmt.Errorf("unsupported Claude response block %q", block.Type)
		}
	}

	if cfg != nil && !hasToolCall &&
		(cfg.ResponseSchema != nil || cfg.ResponseJsonSchema != nil || cfg.ResponseMIMEType == "application/json") {
		cleaned := stripWholeJSONFence(strings.TrimSpace(outputText.String()))
		if !json.Valid([]byte(cleaned)) {
			return nil, fmt.Errorf("Claude returned invalid JSON for structured output")
		}
		var parts []*genai.Part
		for _, part := range content.Parts {
			if part.Thought {
				parts = append(parts, part)
			}
		}
		content.Parts = append(parts, genai.NewPartFromText(cleaned))
	}

	inputTokens := message.Usage.InputTokens +
		message.Usage.CacheCreationInputTokens +
		message.Usage.CacheReadInputTokens
	outputTokens := message.Usage.OutputTokens
	response := &adkmodel.LLMResponse{
		Content:      content,
		ModelVersion: string(message.Model),
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:        int32(inputTokens),
			CachedContentTokenCount: int32(message.Usage.CacheReadInputTokens),
			CandidatesTokenCount:    int32(outputTokens),
			TotalTokenCount:         int32(inputTokens + outputTokens),
		},
		TurnComplete: true,
		FinishReason: genai.FinishReasonStop,
	}
	switch string(message.StopReason) {
	case "max_tokens":
		response.FinishReason = genai.FinishReasonMaxTokens
	case "refusal":
		response.FinishReason = genai.FinishReasonSafety
	case "pause_turn":
		response.FinishReason = genai.FinishReasonOther
	case "end_turn", "stop_sequence", "tool_use":
		// A tool call is represented by FunctionCall parts and processed by ADK.
	default:
		return nil, fmt.Errorf("unknown Claude stop reason %q", message.StopReason)
	}
	return response, nil
}

// Only an entire fenced JSON response may be unwrapped. Extracting the first
// fenced object from arbitrary prose could silently replace the final answer.
func stripWholeJSONFence(value string) string {
	if !strings.HasPrefix(value, "```") || !strings.HasSuffix(value, "```") {
		return value
	}
	lines := strings.Split(value, "\n")
	if len(lines) < 3 || (lines[0] != "```" && !strings.EqualFold(lines[0], "```json")) ||
		lines[len(lines)-1] != "```" {
		return value
	}
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}
