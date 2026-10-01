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
	"strings"
	"testing"

	anthropicapi "github.com/anthropics/anthropic-sdk-go"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/genai"
)

func TestHighThinkingRaisesDefaultMaxTokens(t *testing.T) {
	req := &adkmodel.LLMRequest{Config: &genai.GenerateContentConfig{
		ThinkingConfig: &genai.ThinkingConfig{
			ThinkingLevel: genai.ThinkingLevelHigh,
		},
	}}
	params, err := buildRequest("claude-sonnet-4-5", req)
	if err != nil {
		t.Fatal(err)
	}
	if params.Thinking.OfEnabled == nil || params.Thinking.OfEnabled.BudgetTokens != 10000 {
		t.Fatalf("wrong thinking config: %+v", params.Thinking)
	}
	if params.MaxTokens <= params.Thinking.OfEnabled.BudgetTokens {
		t.Fatalf("max tokens %d must exceed thinking budget", params.MaxTokens)
	}
}

func TestToolModeNoneRemovesTools(t *testing.T) {
	req := &adkmodel.LLMRequest{Config: &genai.GenerateContentConfig{
		Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name: "search",
		}}}},
		ToolConfig: &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{
			Mode: genai.FunctionCallingConfigModeNone,
		}},
	}}
	params, err := buildRequest("claude-sonnet-4-5", req)
	if err != nil {
		t.Fatal(err)
	}
	if len(params.Tools) != 0 {
		t.Fatalf("tools must be omitted in mode NONE")
	}
}

func TestToolModeAnyHonorsAllowedNames(t *testing.T) {
	req := &adkmodel.LLMRequest{Config: &genai.GenerateContentConfig{
		Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{
			{Name: "allowed"}, {Name: "excluded"},
		}}},
		ToolConfig: &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{
			Mode:                 genai.FunctionCallingConfigModeAny,
			AllowedFunctionNames: []string{"allowed"},
		}},
	}}
	params, err := buildRequest("claude-sonnet-4-5", req)
	if err != nil {
		t.Fatal(err)
	}
	if len(params.Tools) != 1 || params.Tools[0].OfTool.Name != "allowed" ||
		params.ToolChoice.OfTool == nil || params.ToolChoice.OfTool.Name != "allowed" {
		t.Fatalf("allowed tool filter was not preserved: %+v", params)
	}
}

func TestToolSchemaKeepsConstraints(t *testing.T) {
	minimum := float64(0)
	decl := &genai.FunctionDeclaration{
		Name: "lookup",
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"code":  {Type: genai.TypeString, Pattern: "^[A-Z]{3}$"},
				"ratio": {Type: genai.TypeNumber, Minimum: &minimum},
			},
		},
	}
	schema, err := toolSchema(decl)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"\"pattern\":\"^[A-Z]{3}$\"", "\"minimum\":0", "\"type\":\"object\""} {
		if !strings.Contains(string(raw), expected) {
			t.Fatalf("schema lost %s: %s", expected, raw)
		}
	}
}

func TestToolResultRequiresID(t *testing.T) {
	_, _, err := convertPart(&genai.Part{FunctionResponse: &genai.FunctionResponse{Name: "lookup"}})
	if err == nil {
		t.Fatal("missing tool call ID must fail locally")
	}
}

func TestStructuredOutputNeverExtractsDraft(t *testing.T) {
	fence := strings.Repeat(string(rune(96)), 3)
	message := &anthropicapi.Message{
		Model:      "claude-sonnet-4-5",
		StopReason: anthropicapi.StopReason("end_turn"),
		Content: []anthropicapi.ContentBlockUnion{{
			Type: "text",
			Text: "draft: " + fence + "json\n{\"draft\":1}\n" + fence + "\nfinal: {\"answer\":2}",
		}},
	}
	_, err := toLLMResponse(message, &genai.GenerateContentConfig{ResponseMIMEType: "application/json"})
	if err == nil {
		t.Fatal("prose with an embedded draft JSON block must not be treated as final JSON")
	}
}

func TestStructuredOutputValidatesSchema(t *testing.T) {
	configs := []struct {
		name string
		cfg  *genai.GenerateContentConfig
	}{
		{
			name: "GenAI response schema",
			cfg: &genai.GenerateContentConfig{ResponseSchema: &genai.Schema{
				Type:     genai.TypeObject,
				Required: []string{"answer"},
				Properties: map[string]*genai.Schema{
					"answer": {Type: genai.TypeString},
				},
			}},
		},
		{
			name: "JSON response schema",
			cfg: &genai.GenerateContentConfig{ResponseJsonSchema: map[string]any{
				"type":     "object",
				"required": []string{"answer"},
				"properties": map[string]any{
					"answer": map[string]any{"type": "string"},
				},
			}},
		},
	}
	for _, config := range configs {
		for _, test := range []struct {
			name    string
			text    string
			wantErr bool
		}{
			{"required field missing", `{}`, true},
			{"wrong field type", `{"answer":42}`, true},
			{"matching schema", `{"answer":"ready"}`, false},
		} {
			t.Run(config.name+"/"+test.name, func(t *testing.T) {
				message := &anthropicapi.Message{
					Model:      "claude-sonnet-4-5",
					StopReason: anthropicapi.StopReason("end_turn"),
					Content: []anthropicapi.ContentBlockUnion{{
						Type: "text", Text: test.text,
					}},
				}
				response, err := toLLMResponse(message, config.cfg)
				if (err != nil) != test.wantErr {
					t.Fatalf("toLLMResponse() = (%+v, %v), want error %t", response, err, test.wantErr)
				}
				if test.wantErr && !strings.Contains(err.Error(), "does not match response schema") {
					t.Fatalf("toLLMResponse() error = %v, want schema validation error", err)
				}
			})
		}
	}
}

func TestStructuredOutputAllowsGenAINullableField(t *testing.T) {
	nullable := true
	cfg := &genai.GenerateContentConfig{ResponseSchema: &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"answer": {Type: genai.TypeString, Nullable: &nullable},
		},
	}}
	message := &anthropicapi.Message{
		Model:      "claude-sonnet-4-5",
		StopReason: anthropicapi.StopReason("end_turn"),
		Content: []anthropicapi.ContentBlockUnion{{
			Type: "text", Text: `{"answer":null}`,
		}},
	}
	if _, err := toLLMResponse(message, cfg); err != nil {
		t.Fatalf("nullable field was rejected: %v", err)
	}
}

func TestThoughtSignatureRoundTrip(t *testing.T) {
	signature := "opaque-signature"
	message := &anthropicapi.Message{
		Model:      "claude-sonnet-4-5",
		StopReason: anthropicapi.StopReason("end_turn"),
		Content: []anthropicapi.ContentBlockUnion{{
			Type: "thinking", Thinking: "reasoning", Signature: signature,
		}},
	}
	response, err := toLLMResponse(message, nil)
	if err != nil {
		t.Fatal(err)
	}
	block, _, err := convertPart(response.Content.Parts[0])
	if err != nil {
		t.Fatal(err)
	}
	if block.OfThinking == nil || block.OfThinking.Signature != signature {
		t.Fatalf("thought signature was not preserved")
	}
}

func TestToolCallRoundTrip(t *testing.T) {
	message := &anthropicapi.Message{
		Model:      "claude-sonnet-4-5",
		StopReason: anthropicapi.StopReason("tool_use"),
		Content: []anthropicapi.ContentBlockUnion{{
			Type: "tool_use", ID: "call-1", Name: "lookup", Input: json.RawMessage("{\"code\":\"ABC\"}"),
		}},
	}
	response, err := toLLMResponse(message, nil)
	if err != nil {
		t.Fatal(err)
	}
	call := response.Content.Parts[0].FunctionCall
	if call == nil || call.ID != "call-1" || call.Args["code"] != "ABC" {
		t.Fatalf("tool call lost ADK ID or arguments: %+v", call)
	}
	block, _, err := convertPart(response.Content.Parts[0])
	if err != nil {
		t.Fatal(err)
	}
	if block.OfToolUse == nil || block.OfToolUse.ID != "call-1" {
		t.Fatalf("tool replay lost ID: %+v", block)
	}
}
