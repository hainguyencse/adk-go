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
	"context"
	"encoding/json"
	"strings"
	"testing"

	anthropicapi "github.com/anthropics/anthropic-sdk-go"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/genai"
)

func TestValidateGenerateContentConfig(t *testing.T) {
	tests := []struct {
		name      string
		modelName string
		config    *genai.GenerateContentConfig
		wantError string
	}{
		{name: "nil config", modelName: "claude-opus-4-7"},
		{
			name:      "adaptive thinking",
			modelName: "claude-opus-4-7",
			config: &genai.GenerateContentConfig{ThinkingConfig: &genai.ThinkingConfig{
				ThinkingLevel: genai.ThinkingLevelMedium, IncludeThoughts: true,
			}},
		},
		{
			name:      "explicit zero temperature",
			modelName: "claude-opus-4-7",
			config:    &genai.GenerateContentConfig{Temperature: genai.Ptr(float32(0))},
			wantError: "does not support explicit Temperature",
		},
		{
			name:      "top p",
			modelName: "claude-opus-4-7",
			config:    &genai.GenerateContentConfig{TopP: genai.Ptr(float32(0.5))},
			wantError: "does not support explicit TopP",
		},
		{
			name:      "top k",
			modelName: "claude-opus-4-7",
			config:    &genai.GenerateContentConfig{TopK: genai.Ptr(float32(1))},
			wantError: "does not support explicit TopK",
		},
		{
			name:      "fixed adaptive budget",
			modelName: "claude-opus-4-7",
			config: &genai.GenerateContentConfig{ThinkingConfig: &genai.ThinkingConfig{
				ThinkingBudget: genai.Ptr(int32(2048)),
			}},
			wantError: "does not support fixed ThinkingBudget",
		},
		{
			name:      "manual budget exceeds output",
			modelName: "claude-sonnet-4-5",
			config: &genai.GenerateContentConfig{
				MaxOutputTokens: 2048,
				ThinkingConfig:  &genai.ThinkingConfig{ThinkingBudget: genai.Ptr(int32(2048))},
			},
			wantError: "must be less than max output tokens",
		},
		{
			name:      "manual budget is supported",
			modelName: "claude-sonnet-4-5",
			config: &genai.GenerateContentConfig{ThinkingConfig: &genai.ThinkingConfig{
				ThinkingBudget:  genai.Ptr(int32(2048)),
				IncludeThoughts: true,
			}},
		},
		{
			name:      "manual minimum budget is supported",
			modelName: "claude-sonnet-4-5",
			config: &genai.GenerateContentConfig{ThinkingConfig: &genai.ThinkingConfig{
				ThinkingBudget: genai.Ptr(int32(1024)),
			}},
		},
		{
			name:      "manual budget below minimum",
			modelName: "claude-sonnet-4-5",
			config: &genai.GenerateContentConfig{ThinkingConfig: &genai.ThinkingConfig{
				ThinkingBudget: genai.Ptr(int32(1023)),
			}},
			wantError: "requires a fixed ThinkingBudget of at least 1024",
		},
		{
			name:      "manual thinking disabled",
			modelName: "claude-sonnet-4-5",
			config: &genai.GenerateContentConfig{ThinkingConfig: &genai.ThinkingConfig{
				ThinkingBudget: genai.Ptr(int32(0)),
			}},
		},
		{
			name:      "manual level needs budget",
			modelName: "claude-sonnet-4-5",
			config: &genai.GenerateContentConfig{ThinkingConfig: &genai.ThinkingConfig{
				ThinkingLevel: genai.ThinkingLevelHigh,
			}},
			wantError: "does not support ThinkingLevel",
		},
		{
			name:      "manual level is not silently ignored with a budget",
			modelName: "claude-sonnet-4-5",
			config: &genai.GenerateContentConfig{ThinkingConfig: &genai.ThinkingConfig{
				ThinkingLevel:  genai.ThinkingLevelLow,
				ThinkingBudget: genai.Ptr(int32(2048)),
			}},
			wantError: "does not support ThinkingLevel",
		},
		{
			name:      "manual include thoughts needs budget",
			modelName: "claude-sonnet-4-5",
			config: &genai.GenerateContentConfig{ThinkingConfig: &genai.ThinkingConfig{
				IncludeThoughts: true,
			}},
			wantError: "requires an explicit ThinkingBudget",
		},
		{
			name:      "manual dynamic budget is unsupported",
			modelName: "claude-sonnet-4-5",
			config: &genai.GenerateContentConfig{ThinkingConfig: &genai.ThinkingConfig{
				ThinkingBudget: genai.Ptr(int32(-1)),
			}},
			wantError: "requires a fixed ThinkingBudget",
		},
		{
			name:      "manual thoughts cannot be included when disabled",
			modelName: "claude-sonnet-4-5",
			config: &genai.GenerateContentConfig{ThinkingConfig: &genai.ThinkingConfig{
				ThinkingBudget:  genai.Ptr(int32(0)),
				IncludeThoughts: true,
			}},
			wantError: "cannot include thoughts",
		},
		{
			name:      "unknown model with options",
			modelName: "claude-opus-6",
			config:    &genai.GenerateContentConfig{Temperature: genai.Ptr(float32(0.5))},
			wantError: "no declared thinking and sampling capabilities",
		},
		{name: "unknown model without special options", modelName: "claude-opus-6", config: &genai.GenerateContentConfig{}},
		{name: "non-Claude name without special options", modelName: "future-model", config: &genai.GenerateContentConfig{}},
		{name: "empty model name", modelName: " ", wantError: "model name is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateGenerateContentConfig(tt.modelName, tt.config)
			if tt.wantError == "" && err != nil {
				t.Fatalf("ValidateGenerateContentConfig() error = %v, want nil", err)
			}
			if tt.wantError != "" && (err == nil || !strings.Contains(err.Error(), tt.wantError)) {
				t.Fatalf("ValidateGenerateContentConfig() error = %v, want %q", err, tt.wantError)
			}

			_, requestErr := buildRequest(tt.modelName, &adkmodel.LLMRequest{Config: tt.config})
			if (err == nil) != (requestErr == nil) || (err != nil && err.Error() != requestErr.Error()) {
				t.Fatalf("preflight error = %v, request error = %v", err, requestErr)
			}
		})
	}
}

func TestValidateGenerateContentConfigDoesNotMutateInput(t *testing.T) {
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: "Be concise."}}},
		ThinkingConfig: &genai.ThinkingConfig{
			ThinkingLevel: genai.ThinkingLevelMedium,
		},
		ToolConfig: &genai.ToolConfig{
			FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeAny},
		},
	}
	before, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateGenerateContentConfig("claude-opus-4-7", cfg); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("validation changed config:\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestModelValidateGenerateContentConfig(t *testing.T) {
	m := &Model{name: "claude-opus-4-7"}
	err := m.ValidateGenerateContentConfig(&genai.GenerateContentConfig{
		Temperature: genai.Ptr(float32(0.5)),
	})
	if err == nil || !strings.Contains(err.Error(), "does not support explicit Temperature") {
		t.Fatalf("Model.ValidateGenerateContentConfig() error = %v, want unsupported Temperature", err)
	}
}

func TestGenerateContentRevalidatesRequestConfig(t *testing.T) {
	m := &Model{name: "claude-opus-4-7"}
	req := &adkmodel.LLMRequest{Config: &genai.GenerateContentConfig{
		Temperature: genai.Ptr(float32(0)),
	}}
	for response, err := range m.GenerateContent(context.Background(), req, false) {
		if response != nil || err == nil || !strings.Contains(err.Error(), "does not support explicit Temperature") {
			t.Fatalf("GenerateContent() = (%+v, %v), want local config error", response, err)
		}
		return
	}
	t.Fatal("GenerateContent() returned no response or error")
}

func TestValidateGenerateContentConfigDefersUnpopulatedToolChoice(t *testing.T) {
	cfg := &genai.GenerateContentConfig{ToolConfig: &genai.ToolConfig{
		FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeAny},
	}}
	if err := ValidateGenerateContentConfig("claude-opus-4-7", cfg); err != nil {
		t.Fatalf("preflight rejected tools that ADK has not attached yet: %v", err)
	}
	_, err := buildRequest("claude-opus-4-7", &adkmodel.LLMRequest{Config: cfg})
	if err == nil || !strings.Contains(err.Error(), "ANY has no eligible function declarations") {
		t.Fatalf("final request error = %v, want missing tool declarations", err)
	}
}

func TestExplicitManualThinkingBudgetRaisesDefaultMaxTokens(t *testing.T) {
	req := &adkmodel.LLMRequest{Config: &genai.GenerateContentConfig{
		ThinkingConfig: &genai.ThinkingConfig{
			ThinkingBudget: genai.Ptr(int32(10000)),
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

func TestOpus47EmptyConfigOmitsThinking(t *testing.T) {
	params, err := buildRequest("claude-opus-4-7", &adkmodel.LLMRequest{Config: &genai.GenerateContentConfig{}})
	if err != nil {
		t.Fatal(err)
	}
	if params.MaxTokens != 4096 || params.Thinking.OfAdaptive != nil || params.Thinking.OfEnabled != nil {
		t.Fatalf("empty config changed default output or enabled thinking: %+v", params)
	}
}

func TestOpus47UsesAdaptiveThinking(t *testing.T) {
	for _, tt := range []struct {
		name   string
		model  string
		level  genai.ThinkingLevel
		budget *int32
		want   anthropicapi.OutputConfigEffort
	}{
		{name: "Opus 4.7 high", model: "claude-opus-4-7", level: genai.ThinkingLevelHigh, want: anthropicapi.OutputConfigEffortHigh},
		{name: "Opus 4.8 medium", model: "claude-opus-4-8", level: genai.ThinkingLevelMedium, want: anthropicapi.OutputConfigEffortMedium},
		{name: "Opus 5 low", model: "claude-opus-5", level: genai.ThinkingLevelLow, want: anthropicapi.OutputConfigEffortLow},
		{name: "minimal approximates low", model: "claude-opus-4-7", level: genai.ThinkingLevelMinimal, want: anthropicapi.OutputConfigEffortLow},
		{name: "dynamic budget", model: "claude-opus-4-7", budget: genai.Ptr(int32(-1))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			params, err := buildRequest(tt.model, &adkmodel.LLMRequest{Config: &genai.GenerateContentConfig{
				MaxOutputTokens: 8192,
				ThinkingConfig:  &genai.ThinkingConfig{ThinkingLevel: tt.level, ThinkingBudget: tt.budget},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if params.Thinking.OfAdaptive == nil || params.Thinking.OfEnabled != nil || params.OutputConfig.Effort != tt.want {
				t.Fatalf("wrong adaptive thinking config: %+v, effort %q", params.Thinking, params.OutputConfig.Effort)
			}
			if params.MaxTokens != 8192 {
				t.Fatalf("max tokens = %d, want 8192", params.MaxTokens)
			}
			encoded, err := json.Marshal(params)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "budget_tokens") || !strings.Contains(string(encoded), `"type":"adaptive"`) {
				t.Fatalf("invalid Opus request: %s", encoded)
			}
		})
	}
}

func TestOpus47IncludeThoughtsRequestsSummary(t *testing.T) {
	params, err := buildRequest("claude-opus-4-7", &adkmodel.LLMRequest{Config: &genai.GenerateContentConfig{
		ThinkingConfig: &genai.ThinkingConfig{IncludeThoughts: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if params.Thinking.OfAdaptive == nil ||
		params.Thinking.OfAdaptive.Display != anthropicapi.ThinkingConfigAdaptiveDisplaySummarized ||
		params.MaxTokens != 4096 {
		t.Fatalf("IncludeThoughts must use summarized adaptive thinking without changing max tokens: %+v", params)
	}
}

func TestOpus47ThinkingBudget(t *testing.T) {
	for _, tt := range []struct {
		name         string
		budget       int32
		wantAdaptive bool
		wantError    bool
	}{
		{name: "zero disables thinking", budget: 0},
		{name: "dynamic enables adaptive thinking", budget: -1, wantAdaptive: true},
		{name: "fixed budget is unsupported", budget: 2048, wantError: true},
		{name: "invalid negative budget", budget: -2, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			params, err := buildRequest("claude-opus-4-7", &adkmodel.LLMRequest{Config: &genai.GenerateContentConfig{
				ThinkingConfig: &genai.ThinkingConfig{ThinkingBudget: &tt.budget},
			}})
			if (err != nil) != tt.wantError {
				t.Fatalf("buildRequest() error = %v, want error %t", err, tt.wantError)
			}
			if tt.wantError {
				if !strings.Contains(err.Error(), "fixed ThinkingBudget") {
					t.Fatalf("error does not explain unsupported budget: %v", err)
				}
				return
			}
			if (params.Thinking.OfAdaptive != nil) != tt.wantAdaptive {
				t.Fatalf("adaptive thinking = %+v, want %t", params.Thinking, tt.wantAdaptive)
			}
		})
	}
}

func TestDefaultOnModelsHandleDisabledThinking(t *testing.T) {
	zero := int32(0)
	for _, tt := range []struct {
		model       string
		wantDisable bool
		wantError   bool
	}{
		{model: "claude-opus-5", wantDisable: true},
		{model: "claude-sonnet-5", wantDisable: true},
		{model: "claude-fable-5", wantError: true},
	} {
		t.Run(tt.model, func(t *testing.T) {
			params, err := buildRequest(tt.model, &adkmodel.LLMRequest{Config: &genai.GenerateContentConfig{
				ThinkingConfig: &genai.ThinkingConfig{ThinkingBudget: &zero},
			}})
			if (err != nil) != tt.wantError {
				t.Fatalf("buildRequest() error = %v, want error %t", err, tt.wantError)
			}
			if tt.wantError {
				if !strings.Contains(err.Error(), "does not support disabling thinking") {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if (params.Thinking.OfDisabled != nil) != tt.wantDisable {
				t.Fatalf("disabled thinking = %+v, want %t", params.Thinking, tt.wantDisable)
			}
			encoded, err := json.Marshal(params)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(encoded), `"type":"disabled"`) {
				t.Fatalf("disabled thinking was not serialized: %s", encoded)
			}
		})
	}
}

func TestOpus47RejectsExplicitSampling(t *testing.T) {
	for _, tt := range []struct {
		name string
		cfg  *genai.GenerateContentConfig
	}{
		{name: "Temperature", cfg: &genai.GenerateContentConfig{Temperature: genai.Ptr(float32(0))}},
		{name: "TopP", cfg: &genai.GenerateContentConfig{TopP: genai.Ptr(float32(0.5))}},
		{name: "TopK", cfg: &genai.GenerateContentConfig{TopK: genai.Ptr(float32(1))}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildRequest("claude-opus-4-7", &adkmodel.LLMRequest{Config: tt.cfg})
			if err == nil || !strings.Contains(err.Error(), tt.name) || !strings.Contains(err.Error(), "omit the field") {
				t.Fatalf("buildRequest() error = %v, want clear %s rejection", err, tt.name)
			}
		})
	}
}

func TestUnknownModelRequiresCapabilityReviewForThinking(t *testing.T) {
	for _, name := range []string{"claude-opus-6", "claude-opus-5-5", "future-model"} {
		t.Run(name, func(t *testing.T) {
			_, err := buildRequest(name, &adkmodel.LLMRequest{Config: &genai.GenerateContentConfig{
				ThinkingConfig: &genai.ThinkingConfig{IncludeThoughts: true},
			}})
			if err == nil || !strings.Contains(err.Error(), "no declared thinking and sampling capabilities") {
				t.Fatalf("buildRequest() error = %v, want capability review", err)
			}
		})
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
