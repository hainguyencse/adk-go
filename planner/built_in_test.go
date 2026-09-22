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

package planner

import (
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/model"
)

func TestBuiltInPlannerApplyThinkingConfig(t *testing.T) {
	t.Parallel()

	budget := int32(1024)
	want := &genai.ThinkingConfig{IncludeThoughts: true, ThinkingBudget: &budget}
	p := NewBuiltInPlanner(want)
	req := &model.LLMRequest{}

	p.ApplyThinkingConfig(req)

	if req.Config == nil {
		t.Fatal("ApplyThinkingConfig() did not initialize request.Config")
	}
	if req.Config.ThinkingConfig != want {
		t.Errorf("ApplyThinkingConfig() set %p, want %p", req.Config.ThinkingConfig, want)
	}
}

func TestBuiltInPlannerApplyThinkingConfigOverridesRequest(t *testing.T) {
	t.Parallel()

	want := &genai.ThinkingConfig{IncludeThoughts: true}
	requestBudget := int32(1)
	req := &model.LLMRequest{Config: &genai.GenerateContentConfig{
		ThinkingConfig: &genai.ThinkingConfig{ThinkingBudget: &requestBudget},
	}}

	NewBuiltInPlanner(want).ApplyThinkingConfig(req)

	if req.Config.ThinkingConfig != want {
		t.Errorf("ApplyThinkingConfig() set %p, want %p", req.Config.ThinkingConfig, want)
	}
}

func TestBuiltInPlannerNilThinkingConfigDoesNotChangeRequest(t *testing.T) {
	t.Parallel()

	req := &model.LLMRequest{}
	NewBuiltInPlanner(nil).ApplyThinkingConfig(req)

	if req.Config != nil {
		t.Errorf("ApplyThinkingConfig() initialized request.Config, want nil")
	}
}
