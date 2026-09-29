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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	anthropicapi "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	adkmodel "google.golang.org/adk/model"
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
