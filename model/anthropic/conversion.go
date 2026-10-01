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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	anthropicapi "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/google/jsonschema-go/jsonschema"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/genai"
)

const redactedSignaturePrefix = "anthropic:redacted:"

func buildRequest(name string, req *adkmodel.LLMRequest) (anthropicapi.MessageNewParams, error) {
	if req == nil {
		return anthropicapi.MessageNewParams{}, fmt.Errorf("nil ADK LLM request")
	}
	params := anthropicapi.MessageNewParams{
		Model:     anthropicapi.Model(name),
		MaxTokens: 4096,
	}
	if req.Config != nil {
		cfg := req.Config
		if cfg.MaxOutputTokens > 0 {
			params.MaxTokens = int64(cfg.MaxOutputTokens)
		}
		params.StopSequences = cfg.StopSequences
		if cfg.Temperature != nil {
			params.Temperature = param.NewOpt(float64(*cfg.Temperature))
		}
		if cfg.TopP != nil {
			params.TopP = param.NewOpt(float64(*cfg.TopP))
		}
		if cfg.TopK != nil {
			params.TopK = param.NewOpt(int64(*cfg.TopK))
		}
		if cfg.SystemInstruction != nil {
			for _, part := range cfg.SystemInstruction.Parts {
				if part == nil {
					continue
				}
				if part.Text == "" {
					return params, fmt.Errorf("Claude system instruction only supports text")
				}
				params.System = append(params.System, anthropicapi.TextBlockParam{Text: part.Text})
			}
		}
		if cfg.ThinkingConfig != nil {
			budget, enabled, err := thinkingBudget(cfg.ThinkingConfig)
			if err != nil {
				return params, err
			}
			if enabled {
				if cfg.MaxOutputTokens == 0 && params.MaxTokens <= budget {
					params.MaxTokens = budget + 2048
				}
				if budget >= params.MaxTokens {
					return params, fmt.Errorf("Claude thinking budget %d must be less than max output tokens %d", budget, params.MaxTokens)
				}
				params.Thinking = anthropicapi.ThinkingConfigParamOfEnabled(budget)
			}
		}
		if expectsJSONResponse(cfg) {
			instruction := "Return only valid JSON, with no Markdown fence or explanation."
			raw, err := normalizedResponseSchema(cfg)
			if err != nil {
				return params, err
			}
			if raw != nil {
				instruction += " The JSON must match this schema: " + string(raw)
			}
			params.System = append(params.System, anthropicapi.TextBlockParam{Text: instruction})
		}
		var err error
		params.Tools, params.ToolChoice, err = convertTools(cfg)
		if err != nil {
			return params, err
		}
	}

	for _, content := range req.Contents {
		if content == nil {
			continue
		}
		message, err := convertContent(content)
		if err != nil {
			return params, err
		}
		if len(message.Content) == 0 {
			continue
		}
		// Anthropic accepts adjacent messages of the same role as one turn.
		// Merge here to keep tool results and user text together.
		n := len(params.Messages)
		if n > 0 && params.Messages[n-1].Role == message.Role {
			params.Messages[n-1].Content = append(params.Messages[n-1].Content, message.Content...)
		} else {
			params.Messages = append(params.Messages, message)
		}
	}
	if len(params.Messages) == 0 {
		params.Messages = []anthropicapi.MessageParam{
			anthropicapi.NewUserMessage(anthropicapi.NewTextBlock("Follow the system instruction.")),
		}
	}
	return params, nil
}

func expectsJSONResponse(cfg *genai.GenerateContentConfig) bool {
	return cfg != nil && (cfg.ResponseSchema != nil || cfg.ResponseJsonSchema != nil || cfg.ResponseMIMEType == "application/json")
}

// normalizedResponseSchema keeps the prompt and local validation on the same
// schema. GenAI Schema uses uppercase type names, unlike JSON Schema.
func normalizedResponseSchema(cfg *genai.GenerateContentConfig) ([]byte, error) {
	if cfg == nil {
		return nil, nil
	}
	var source any
	if cfg.ResponseJsonSchema != nil {
		source = cfg.ResponseJsonSchema
	} else if cfg.ResponseSchema != nil {
		source = cfg.ResponseSchema
	} else {
		return nil, nil
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return nil, fmt.Errorf("marshal response schema: %w", err)
	}
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("decode response schema: %w", err)
	}
	normalizeSchemaTypes(normalized)
	normalizeNullableSchemaTypes(normalized)
	raw, err = json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("normalize response schema: %w", err)
	}
	return raw, nil
}

func normalizeNullableSchemaTypes(value any) {
	switch v := value.(type) {
	case map[string]any:
		if nullable, _ := v["nullable"].(bool); nullable {
			if typ, ok := v["type"].(string); ok && typ != "" && typ != "null" {
				v["type"] = []any{typ, "null"}
			}
			delete(v, "nullable")
		}
		for _, child := range v {
			normalizeNullableSchemaTypes(child)
		}
	case []any:
		for _, child := range v {
			normalizeNullableSchemaTypes(child)
		}
	}
}

func validateResponseSchema(text string, cfg *genai.GenerateContentConfig) error {
	raw, err := normalizedResponseSchema(cfg)
	if err != nil {
		return err
	}
	if raw == nil {
		return nil
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return fmt.Errorf("decode response schema for validation: %w", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return fmt.Errorf("resolve response schema: %w", err)
	}
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return fmt.Errorf("decode Claude JSON response: %w", err)
	}
	if err := resolved.Validate(value); err != nil {
		return fmt.Errorf("Claude JSON response does not match response schema: %w", err)
	}
	return nil
}

func thinkingBudget(cfg *genai.ThinkingConfig) (int64, bool, error) {
	if cfg.ThinkingBudget != nil {
		switch budget := *cfg.ThinkingBudget; {
		case budget == 0:
			return 0, false, nil
		case budget == -1:
			return 10000, true, nil
		case budget < 1024:
			return 0, false, fmt.Errorf("Claude thinking budget must be at least 1024 tokens")
		default:
			return int64(budget), true, nil
		}
	}
	switch cfg.ThinkingLevel {
	case genai.ThinkingLevelHigh:
		return 10000, true, nil
	case genai.ThinkingLevelMedium:
		return 4096, true, nil
	case genai.ThinkingLevelLow, genai.ThinkingLevelMinimal:
		return 1024, true, nil
	case "", genai.ThinkingLevelUnspecified:
		if cfg.IncludeThoughts {
			return 10000, true, nil
		}
		return 0, false, nil
	default:
		return 0, false, fmt.Errorf("unsupported Claude thinking level %q", cfg.ThinkingLevel)
	}
}

func convertContent(content *genai.Content) (anthropicapi.MessageParam, error) {
	var result anthropicapi.MessageParam
	switch content.Role {
	case "user":
		result.Role = anthropicapi.MessageParamRoleUser
	case "model", "assistant":
		result.Role = anthropicapi.MessageParamRoleAssistant
	default:
		return result, fmt.Errorf("unsupported ADK role %q for Claude", content.Role)
	}
	for _, part := range content.Parts {
		if part == nil {
			continue
		}
		block, role, err := convertPart(part)
		if err != nil {
			return result, err
		}
		if role != "" {
			if len(result.Content) > 0 && result.Role != role {
				return result, fmt.Errorf("Claude message mixes incompatible user and assistant parts")
			}
			result.Role = role
		}
		result.Content = append(result.Content, block)
	}
	return result, nil
}

func convertPart(part *genai.Part) (anthropicapi.ContentBlockParamUnion, anthropicapi.MessageParamRole, error) {
	var zero anthropicapi.ContentBlockParamUnion
	if part.FunctionResponse != nil {
		resp := part.FunctionResponse
		if resp.ID == "" {
			return zero, "", fmt.Errorf("Claude tool result %q has no call ID", resp.Name)
		}
		raw, err := json.Marshal(resp.Response)
		if err != nil {
			return zero, "", fmt.Errorf("marshal tool result %q: %w", resp.Name, err)
		}
		text := anthropicapi.TextBlockParam{Text: string(raw)}
		return anthropicapi.ContentBlockParamUnion{OfToolResult: &anthropicapi.ToolResultBlockParam{
			ToolUseID: resp.ID,
			Content:   []anthropicapi.ToolResultBlockParamContentUnion{{OfText: &text}},
		}}, anthropicapi.MessageParamRoleUser, nil
	}
	if part.FunctionCall != nil {
		call := part.FunctionCall
		if call.ID == "" {
			return zero, "", fmt.Errorf("Claude tool call %q has no call ID", call.Name)
		}
		args := call.Args
		if args == nil {
			args = map[string]any{}
		}
		return anthropicapi.ContentBlockParamUnion{OfToolUse: &anthropicapi.ToolUseBlockParam{
			ID: call.ID, Name: call.Name, Input: args,
		}}, anthropicapi.MessageParamRoleAssistant, nil
	}
	if part.Thought {
		signature := string(part.ThoughtSignature)
		if strings.HasPrefix(signature, redactedSignaturePrefix) {
			return anthropicapi.ContentBlockParamUnion{OfRedactedThinking: &anthropicapi.RedactedThinkingBlockParam{
				Data: strings.TrimPrefix(signature, redactedSignaturePrefix),
			}}, anthropicapi.MessageParamRoleAssistant, nil
		}
		if signature == "" {
			return zero, "", fmt.Errorf("Claude thought cannot be replayed without its signature")
		}
		return anthropicapi.ContentBlockParamUnion{OfThinking: &anthropicapi.ThinkingBlockParam{
			Thinking: part.Text, Signature: signature,
		}}, anthropicapi.MessageParamRoleAssistant, nil
	}
	if part.InlineData != nil {
		blob := part.InlineData
		switch blob.MIMEType {
		case "text/plain", "text/csv", "application/json":
			return anthropicapi.NewTextBlock(string(blob.Data)), "", nil
		case "image/jpeg", "image/png", "image/gif", "image/webp":
			return anthropicapi.ContentBlockParamUnion{OfImage: &anthropicapi.ImageBlockParam{
				Source: anthropicapi.ImageBlockParamSourceUnion{OfBase64: &anthropicapi.Base64ImageSourceParam{
					Data: base64.StdEncoding.EncodeToString(blob.Data), MediaType: anthropicapi.Base64ImageSourceMediaType(blob.MIMEType),
				}},
			}}, "", nil
		case "application/pdf":
			return anthropicapi.ContentBlockParamUnion{OfDocument: &anthropicapi.DocumentBlockParam{
				Source: anthropicapi.DocumentBlockParamSourceUnion{OfBase64: &anthropicapi.Base64PDFSourceParam{
					Data: base64.StdEncoding.EncodeToString(blob.Data),
				}},
			}}, "", nil
		default:
			return zero, "", fmt.Errorf("Claude does not support inline MIME type %q", blob.MIMEType)
		}
	}
	if part.Text != "" {
		return anthropicapi.NewTextBlock(part.Text), "", nil
	}
	return zero, "", fmt.Errorf("unsupported ADK content part for Claude")
}

func convertTools(cfg *genai.GenerateContentConfig) ([]anthropicapi.ToolUnionParam, anthropicapi.ToolChoiceUnionParam, error) {
	var choice anthropicapi.ToolChoiceUnionParam
	var tools []anthropicapi.ToolUnionParam
	mode := genai.FunctionCallingConfigModeAuto
	var allowed []string
	if cfg.ToolConfig != nil && cfg.ToolConfig.FunctionCallingConfig != nil {
		mode = cfg.ToolConfig.FunctionCallingConfig.Mode
		allowed = cfg.ToolConfig.FunctionCallingConfig.AllowedFunctionNames
	}
	if mode == genai.FunctionCallingConfigModeNone {
		// Omitting all tools is stronger than relying on tool_choice=none.
		return nil, choice, nil
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = true
	}
	for _, tool := range cfg.Tools {
		if tool == nil {
			continue
		}
		if tool.GoogleSearch != nil || tool.GoogleSearchRetrieval != nil || tool.CodeExecution != nil ||
			tool.URLContext != nil || tool.GoogleMaps != nil || tool.Retrieval != nil ||
			tool.EnterpriseWebSearch != nil || tool.ComputerUse != nil ||
			tool.FileSearch != nil || tool.ParallelAISearch != nil ||
			len(tool.MCPServers) > 0 || tool.ExaAISearch != nil {
			return nil, choice, fmt.Errorf("Gemini-native tool cannot be used with Claude on Google Cloud")
		}
		for _, decl := range tool.FunctionDeclarations {
			if decl == nil {
				continue
			}
			if mode == genai.FunctionCallingConfigModeAny && len(allowedSet) > 0 && !allowedSet[decl.Name] {
				continue
			}
			schema, err := toolSchema(decl)
			if err != nil {
				return nil, choice, fmt.Errorf("Claude tool %q: %w", decl.Name, err)
			}
			p := anthropicapi.ToolParam{
				Name:        decl.Name,
				InputSchema: schema,
				Description: param.NewOpt(decl.Description),
			}
			tools = append(tools, anthropicapi.ToolUnionParam{OfTool: &p})
		}
	}
	if len(tools) == 0 {
		if mode == genai.FunctionCallingConfigModeAny {
			return nil, choice, fmt.Errorf("Claude tool mode ANY has no eligible function declarations")
		}
		return nil, choice, nil
	}
	switch mode {
	case "", genai.FunctionCallingConfigModeUnspecified, genai.FunctionCallingConfigModeAuto:
		choice.OfAuto = &anthropicapi.ToolChoiceAutoParam{}
	case genai.FunctionCallingConfigModeAny:
		if len(allowed) == 1 {
			choice = anthropicapi.ToolChoiceParamOfTool(allowed[0])
		} else {
			choice.OfAny = &anthropicapi.ToolChoiceAnyParam{}
		}
	default:
		return nil, choice, fmt.Errorf("unsupported Claude tool mode %q", mode)
	}
	return tools, choice, nil
}

func toolSchema(decl *genai.FunctionDeclaration) (anthropicapi.ToolInputSchemaParam, error) {
	var schema anthropicapi.ToolInputSchemaParam
	source := decl.ParametersJsonSchema
	if source == nil {
		source = decl.Parameters
	}
	if source == nil {
		return schema, nil
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return schema, err
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return schema, err
	}
	if obj == nil {
		return schema, nil
	}
	normalizeSchemaTypes(obj)
	if kind, _ := obj["type"].(string); kind != "" && kind != "object" {
		return schema, fmt.Errorf("tool parameters must be an object schema, got %q", kind)
	}
	if properties, ok := obj["properties"]; ok {
		schema.Properties = properties
		delete(obj, "properties")
	}
	if required, ok := obj["required"].([]any); ok {
		for _, item := range required {
			if name, ok := item.(string); ok {
				schema.Required = append(schema.Required, name)
			}
		}
		delete(obj, "required")
	}
	delete(obj, "type")
	schema.ExtraFields = obj
	return schema, nil
}

func normalizeSchemaTypes(value any) {
	switch v := value.(type) {
	case map[string]any:
		if typ, ok := v["type"].(string); ok {
			v["type"] = strings.ToLower(typ)
		}
		for _, child := range v {
			normalizeSchemaTypes(child)
		}
	case []any:
		for _, child := range v {
			normalizeSchemaTypes(child)
		}
	}
}
