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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	anthropicapi "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"
)

func TestGenerateContentCallsSDKAndConvertsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if !strings.Contains(string(body), "hello Claude") {
			t.Errorf("request lost user content: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"content\":[{\"type\":\"text\",\"text\":\"Hello\"}],\"stop_reason\":\"end_turn\",\"stop_sequence\":null,\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}")
	}))
	defer server.Close()
	m := &Model{
		name: "claude-sonnet-4-5",
		client: anthropicapi.NewMessageService(
			option.WithBaseURL(server.URL),
			option.WithAPIKey("test-only"),
		),
	}
	req := &adkmodel.LLMRequest{Contents: []*genai.Content{
		genai.NewContentFromText("hello Claude", "user"),
	}}
	var response *adkmodel.LLMResponse
	for got, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
		response = got
	}
	if response == nil || !response.TurnComplete || response.Content.Parts[0].Text != "Hello" {
		t.Fatalf("unexpected ADK response: %+v", response)
	}
	if response.UsageMetadata.PromptTokenCount != 3 || response.UsageMetadata.CandidatesTokenCount != 2 {
		t.Fatalf("usage lost: %+v", response.UsageMetadata)
	}
}

func TestGenerateContentOpus47SendsAdaptiveThinking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Thinking struct {
				Type    string `json:"type"`
				Display string `json:"display"`
			} `json:"thinking"`
			OutputConfig struct {
				Effort string `json:"effort"`
			} `json:"output_config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode Claude request: %v", err)
		}
		if body.Thinking.Type != "adaptive" || body.Thinking.Display != "summarized" || body.OutputConfig.Effort != "high" {
			t.Errorf("wrong Claude thinking request: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg_test","type":"message","role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"Hello"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":3,"output_tokens":2}}`)
	}))
	defer server.Close()
	m := &Model{
		name: "claude-opus-4-7",
		client: anthropicapi.NewMessageService(
			option.WithBaseURL(server.URL),
			option.WithAPIKey("test-only"),
		),
	}
	req := &adkmodel.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("hello Claude", genai.RoleUser)},
		Config: &genai.GenerateContentConfig{ThinkingConfig: &genai.ThinkingConfig{
			ThinkingLevel:   genai.ThinkingLevelHigh,
			IncludeThoughts: true,
		}},
	}
	var response *adkmodel.LLMResponse
	for got, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
		response = got
	}
	if response == nil || response.Content == nil || response.Content.Parts[0].Text != "Hello" {
		t.Fatalf("unexpected ADK response: %+v", response)
	}
}

func TestToolCallRoundTripThroughADKRunner(t *testing.T) {
	const toolUseID = "toolu_test_lookup"
	var requestCount atomic.Int32
	var toolCallCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read Messages request: %v", err)
			return
		}
		var request struct {
			Messages []struct {
				Role    string           `json:"role"`
				Content []map[string]any `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("decode Messages request: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch requestCount.Add(1) {
		case 1:
			if !strings.Contains(string(body), `"name":"lookup"`) {
				t.Errorf("first request did not declare lookup tool: %s", body)
			}
			fmt.Fprint(w, `{"id":"msg_tool","type":"message","role":"assistant","model":"claude-opus-4-7","content":[{"type":"tool_use","id":"`+toolUseID+`","name":"lookup","input":{"code":"ABC"}}],"stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":3,"output_tokens":5}}`)
		case 2:
			found := false
			for i, message := range request.Messages {
				for _, block := range message.Content {
					if block["type"] != "tool_use" || block["id"] != toolUseID {
						continue
					}
					if message.Role != "assistant" || i+1 >= len(request.Messages) || request.Messages[i+1].Role != "user" {
						t.Errorf("tool_use must be followed by a user tool_result: %s", body)
						continue
					}
					for _, result := range request.Messages[i+1].Content {
						if result["type"] == "tool_result" && result["tool_use_id"] == toolUseID &&
							strings.Contains(fmt.Sprint(result["content"]), "found ABC") {
							found = true
						}
					}
				}
			}
			if !found {
				t.Errorf("second request lost matching tool_result or handler output: %s", body)
			}
			fmt.Fprint(w, `{"id":"msg_final","type":"message","role":"assistant","model":"claude-opus-4-7","content":[{"type":"text","text":"Found ABC"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":3}}`)
		default:
			t.Errorf("unexpected extra Messages request: %s", body)
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	lookup, err := functiontool.New(functiontool.Config{Name: "lookup", Description: "Look up a code"},
		func(_ tool.Context, input struct {
			Code string `json:"code"`
		}) (map[string]string, error) {
			toolCallCount.Add(1)
			return map[string]string{"value": "found " + input.Code}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	m := &Model{
		name: "claude-opus-4-7",
		client: anthropicapi.NewMessageService(
			option.WithBaseURL(server.URL),
			option.WithAPIKey("test-only"),
		),
	}
	a, err := llmagent.New(llmagent.Config{
		Name: "claude_tool_test", Model: m, Tools: []tool.Tool{lookup},
	})
	if err != nil {
		t.Fatal(err)
	}
	service := session.InMemoryService()
	created, err := service.Create(context.Background(), &session.CreateRequest{AppName: "claude_tool_test", UserID: "user"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := runner.New(runner.Config{AppName: "claude_tool_test", Agent: a, SessionService: service})
	if err != nil {
		t.Fatal(err)
	}
	var finalText string
	for event, err := range r.Run(context.Background(), "user", created.Session.ID(), genai.NewContentFromText("Look up ABC", genai.RoleUser), agent.RunConfig{}) {
		if err != nil {
			t.Fatal(err)
		}
		if event.IsFinalResponse() && event.Content != nil {
			for _, part := range event.Content.Parts {
				finalText += part.Text
			}
		}
	}
	if got := requestCount.Load(); got != 2 {
		t.Errorf("Messages request count = %d, want 2", got)
	}
	if got := toolCallCount.Load(); got != 1 {
		t.Errorf("ADK tool handler call count = %d, want 1", got)
	}
	if finalText != "Found ABC" {
		t.Errorf("final answer = %q, want %q", finalText, "Found ABC")
	}
}

func TestGenerateContentStreamClosesAndReturnsFinalResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			"{\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}",
			"{\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}",
			"{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}",
			"{\"type\":\"content_block_stop\",\"index\":0}",
			"{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":2}}",
			"{\"type\":\"message_stop\"}",
		}
		eventTypes := []string{
			"message_start", "content_block_start", "content_block_delta",
			"content_block_stop", "message_delta", "message_stop",
		}
		for i, event := range events {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventTypes[i], event)
		}
	}))
	defer server.Close()
	m := &Model{
		name: "claude-sonnet-4-5",
		client: anthropicapi.NewMessageService(
			option.WithBaseURL(server.URL),
			option.WithAPIKey("test-only"),
		),
	}
	req := &adkmodel.LLMRequest{Contents: []*genai.Content{
		genai.NewContentFromText("hello Claude", "user"),
	}}
	var partials int
	var final *adkmodel.LLMResponse
	for got, err := range m.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatal(err)
		}
		if got.Partial {
			partials++
		} else {
			final = got
		}
	}
	if partials != 1 || final == nil || !final.TurnComplete || final.Content.Parts[0].Text != "Hello" {
		t.Fatalf("unexpected stream: partials=%d final=%+v", partials, final)
	}
}

func TestStructuredStreamWaitsForSchemaValidation(t *testing.T) {
	for _, test := range []struct {
		name    string
		text    string
		wantErr bool
	}{
		{"valid", `{"answer":"ready"}`, false},
		{"invalid", `{}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				events := []struct{ kind, data string }{
					{"message_start", `{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-opus-4-7","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":3,"output_tokens":0}}}`},
					{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
					{"content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, test.text)},
					{"content_block_stop", `{"type":"content_block_stop","index":0}`},
					{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}`},
					{"message_stop", `{"type":"message_stop"}`},
				}
				for _, event := range events {
					fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.kind, event.data)
				}
			}))
			defer server.Close()
			m := &Model{
				name: "claude-opus-4-7",
				client: anthropicapi.NewMessageService(
					option.WithBaseURL(server.URL), option.WithAPIKey("test-only"),
				),
			}
			req := &adkmodel.LLMRequest{
				Contents: []*genai.Content{genai.NewContentFromText("answer", genai.RoleUser)},
				Config: &genai.GenerateContentConfig{ResponseJsonSchema: map[string]any{
					"type": "object", "required": []string{"answer"},
				}},
			}
			var partials int
			var final *adkmodel.LLMResponse
			var responseErr error
			for got, err := range m.GenerateContent(context.Background(), req, true) {
				if err != nil {
					responseErr = err
					continue
				}
				if got.Partial {
					partials++
				} else {
					final = got
				}
			}
			if partials != 0 {
				t.Errorf("structured stream exposed %d unvalidated partials", partials)
			}
			if (responseErr != nil) != test.wantErr {
				t.Fatalf("stream response = (%+v, %v), want error %t", final, responseErr, test.wantErr)
			}
			if test.wantErr && final != nil {
				t.Fatalf("invalid JSON schema response was returned: %+v", final)
			}
			if !test.wantErr && (final == nil || final.Content.Parts[0].Text != test.text) {
				t.Fatalf("valid structured response = %+v, want %q", final, test.text)
			}
		})
	}
}

func TestNewModelValidatesConfigurationBeforeLoadingCredentials(t *testing.T) {
	tests := []struct {
		name      string
		modelName string
		config    *Config
		want      string
	}{
		{
			name:      "model",
			modelName: "gemini-2.5-flash",
			config:    &Config{ProjectID: "project", Location: "us-east5"},
			want:      "Claude model ID is required",
		},
		{
			name:      "nil config",
			modelName: "claude-sonnet-4-5",
			want:      "Google Cloud project and Claude-supported location are required",
		},
		{
			name:      "location",
			modelName: "claude-sonnet-4-5",
			config:    &Config{ProjectID: "project"},
			want:      "Google Cloud project and Claude-supported location are required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewModel(context.Background(), tt.modelName, tt.config)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewModel() error = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestNewModelWorksAsRegisteredFactory(t *testing.T) {
	const (
		name    = "claude-registry-test-001"
		pattern = "^" + name + "$"
	)
	adkmodel.Register(pattern, func(ctx context.Context, modelName string) (adkmodel.LLM, error) {
		return NewModel(ctx, modelName, &Config{})
	})

	_, err := adkmodel.NewLLM(context.Background(), name)
	if err == nil || !strings.Contains(err.Error(), "Google Cloud project and Claude-supported location are required") {
		t.Fatalf("NewLLM() error = %v, want captured Claude factory config error", err)
	}
}
