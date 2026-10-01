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
	"iter"
	"strings"

	anthropicapi "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/vertex"
	"golang.org/x/oauth2/google"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/genai"
)

const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// Config only supports Claude through Google Cloud. There is deliberately no
// direct-Anthropic API fallback.
type Config struct {
	ProjectID       string
	Location        string
	CredentialsJSON string
}

type Model struct {
	name   string
	client anthropicapi.MessageService
}

var _ adkmodel.LLM = (*Model)(nil)
var _ adkmodel.GenerateContentConfigValidator = (*Model)(nil)

// NewModel returns an ADK model backed by Claude on Google Cloud Vertex AI.
//
// modelName is separate from Config so NewModel can be used from a
// model.Factory registered with model.Register. Registration remains opt-in,
// because the registry factory must capture the application's Google Cloud
// configuration:
//
//	model.Register(`(?i)^claude-.*`, func(ctx context.Context, name string) (model.LLM, error) {
//		return anthropic.NewModel(ctx, name, cfg)
//	})
//
// This package deliberately does not fall back to the direct Anthropic API.
func NewModel(ctx context.Context, modelName string, cfg *Config) (adkmodel.LLM, error) {
	if !strings.HasPrefix(modelName, "claude-") {
		return nil, fmt.Errorf("Claude model ID is required")
	}
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.ProjectID == "" || cfg.Location == "" {
		return nil, fmt.Errorf("Google Cloud project and Claude-supported location are required")
	}

	var creds *google.Credentials
	var err error
	if cfg.CredentialsJSON != "" {
		creds, err = google.CredentialsFromJSON(ctx, []byte(cfg.CredentialsJSON), cloudPlatformScope)
	} else {
		creds, err = google.FindDefaultCredentials(ctx, cloudPlatformScope)
	}
	if err != nil {
		return nil, fmt.Errorf("load Google Cloud credentials for Claude: %w", err)
	}
	if creds.TokenSource == nil {
		return nil, fmt.Errorf("Google Cloud credentials have no token source")
	}

	return &Model{
		name: modelName,
		// Construct the service directly: NewClient reads ANTHROPIC_API_KEY,
		// ANTHROPIC_AUTH_TOKEN, and ANTHROPIC_BASE_URL from the environment.
		// A Google-only adapter must never inherit those direct-API settings.
		client: anthropicapi.NewMessageService(
			vertex.WithCredentials(ctx, cfg.Location, cfg.ProjectID, creds),
		),
	}, nil
}

func (m *Model) Name() string { return m.name }

// ValidateGenerateContentConfig checks options that can be rejected before ADK
// assembles the final request. GenerateContent validates again after assembly.
func (m *Model) ValidateGenerateContentConfig(cfg *genai.GenerateContentConfig) error {
	return ValidateGenerateContentConfig(m.name, cfg)
}

// The current ADK fork includes Connect in model.LLM. Claude's Messages API
// does not implement the Gemini Live protocol used by the app's audio agents.
func (m *Model) Connect(context.Context, *adkmodel.LLMRequest) (adkmodel.LiveConnection, error) {
	return nil, fmt.Errorf("Claude on Google Cloud does not support this ADK live connection")
}

func (m *Model) GenerateContent(ctx context.Context, req *adkmodel.LLMRequest, stream bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		params, err := buildRequest(m.name, req)
		if err != nil {
			yield(nil, err)
			return
		}

		if !stream {
			message, err := m.client.New(ctx, params)
			if err != nil {
				yield(nil, fmt.Errorf("call Claude on Google Cloud: %w", err))
				return
			}
			response, err := toLLMResponse(message, req.Config)
			yield(response, err)
			return
		}

		s := m.client.NewStreaming(ctx, params)
		defer s.Close()
		var message anthropicapi.Message
		for s.Next() {
			event := s.Current()
			if err := message.Accumulate(event); err != nil {
				yield(nil, fmt.Errorf("accumulate Claude stream: %w", err))
				return
			}
			// Do not expose partial structured JSON that may fail validation in
			// the final snapshot; tool calls are still handled by that snapshot.
			if delta, ok := event.AsAny().(anthropicapi.ContentBlockDeltaEvent); ok && !expectsJSONResponse(req.Config) {
				if textDelta, ok := delta.Delta.AsAny().(anthropicapi.TextDelta); ok && textDelta.Text != "" {
					if !yield(&adkmodel.LLMResponse{
						Content: &genai.Content{Role: "model", Parts: []*genai.Part{genai.NewPartFromText(textDelta.Text)}},
						Partial: true,
					}, nil) {
						return
					}
				}
			}
		}
		if err := s.Err(); err != nil {
			yield(nil, fmt.Errorf("stream Claude on Google Cloud: %w", err))
			return
		}
		response, err := toLLMResponse(&message, req.Config)
		yield(response, err)
	}
}
