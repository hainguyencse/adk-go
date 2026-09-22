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

package llminternal

import (
	"context"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	icontext "google.golang.org/adk/internal/context"
	"google.golang.org/adk/internal/utils"
	"google.golang.org/adk/model"
	"google.golang.org/adk/planner"
)

type testPlanner struct {
	readonlyCtx agent.ReadonlyContext
	callbackCtx agent.CallbackContext
	request     *model.LLMRequest
	response    []*genai.Part
	instruction string
	processed   []*genai.Part
}

func (p *testPlanner) BuildPlanningInstruction(_ context.Context, ctx agent.ReadonlyContext, req *model.LLMRequest) string {
	p.readonlyCtx = ctx
	p.request = req
	return p.instruction
}

func (p *testPlanner) ProcessPlanningResponse(_ context.Context, ctx agent.CallbackContext, parts []*genai.Part) []*genai.Part {
	p.callbackCtx = ctx
	p.response = parts
	return p.processed
}

func planningInvocationContext(t *testing.T, p planner.Planner) agent.InvocationContext {
	t.Helper()
	a := &mockLLMAgent{
		Agent: utils.Must(agent.New(agent.Config{Name: "planner_agent"})),
		s:     &State{Planner: p},
	}
	return icontext.NewInvocationContext(t.Context(), icontext.InvocationContextParams{Agent: a})
}

func TestNLPlanningRequestProcessor(t *testing.T) {
	t.Parallel()

	p := &testPlanner{instruction: "Make a plan first."}
	req := &model.LLMRequest{Contents: []*genai.Content{{Parts: []*genai.Part{
		{Text: "old thought", Thought: true},
	}}}}
	ctx := planningInvocationContext(t, p)

	for _, err := range nlPlanningRequestProcessor(ctx, req, &Flow{}) {
		if err != nil {
			t.Fatalf("nlPlanningRequestProcessor() unexpected error: %v", err)
		}
	}

	if p.readonlyCtx == nil || p.request != req {
		t.Error("nlPlanningRequestProcessor() did not call the configured planner")
	}
	if req.Contents[0].Parts[0].Thought {
		t.Error("nlPlanningRequestProcessor() did not clear an old thought flag")
	}
	if got := utils.TextParts(req.Config.SystemInstruction); len(got) != 1 || got[0] != p.instruction {
		t.Errorf("system instruction = %q, want %q", got, p.instruction)
	}
}

func TestNLPlanningRequestProcessorBuiltIn(t *testing.T) {
	t.Parallel()

	thinkingConfig := &genai.ThinkingConfig{IncludeThoughts: true}
	req := &model.LLMRequest{Contents: []*genai.Content{{Parts: []*genai.Part{
		{Text: "model thought", Thought: true},
	}}}}
	ctx := planningInvocationContext(t, planner.NewBuiltInPlanner(thinkingConfig))

	for range nlPlanningRequestProcessor(ctx, req, &Flow{}) {
	}

	if req.Config == nil || req.Config.ThinkingConfig != thinkingConfig {
		t.Errorf("thinking config = %v, want %v", req.Config, thinkingConfig)
	}
	if !req.Contents[0].Parts[0].Thought {
		t.Error("built-in planner unexpectedly cleared the thought flag")
	}
}

func TestNLPlanningResponseProcessor(t *testing.T) {
	t.Parallel()

	want := []*genai.Part{{Text: "final answer"}}
	p := &testPlanner{processed: want}
	resp := &model.LLMResponse{Content: &genai.Content{Parts: []*genai.Part{{Text: "raw response"}}}}
	ctx := planningInvocationContext(t, p)

	if err := nlPlanningResponseProcessor(ctx, &model.LLMRequest{}, resp); err != nil {
		t.Fatalf("nlPlanningResponseProcessor() unexpected error: %v", err)
	}

	if p.callbackCtx == nil || len(p.response) != 1 || p.response[0].Text != "raw response" {
		t.Error("nlPlanningResponseProcessor() did not call the configured planner")
	}
	if len(resp.Content.Parts) != 1 || resp.Content.Parts[0] != want[0] {
		t.Errorf("response parts = %v, want %v", resp.Content.Parts, want)
	}
}

func TestNLPlanningResponseProcessorKeepsPartsForEmptyResult(t *testing.T) {
	t.Parallel()

	original := &genai.Part{Text: "unchanged"}
	p := &testPlanner{}
	resp := &model.LLMResponse{Content: &genai.Content{Parts: []*genai.Part{original}}}

	if err := nlPlanningResponseProcessor(planningInvocationContext(t, p), &model.LLMRequest{}, resp); err != nil {
		t.Fatalf("nlPlanningResponseProcessor() unexpected error: %v", err)
	}
	if len(resp.Content.Parts) != 1 || resp.Content.Parts[0] != original {
		t.Errorf("response parts = %v, want original response", resp.Content.Parts)
	}
}
