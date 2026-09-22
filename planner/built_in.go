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
	"context"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
)

// BuiltInPlanner uses a model's built-in thinking features.
//
// Models that do not support thinking may reject requests configured with this
// planner.
type BuiltInPlanner struct {
	thinkingConfig *genai.ThinkingConfig
}

var _ Planner = (*BuiltInPlanner)(nil)

// NewBuiltInPlanner returns a planner with the provided thinking configuration.
func NewBuiltInPlanner(thinkingConfig *genai.ThinkingConfig) *BuiltInPlanner {
	return &BuiltInPlanner{thinkingConfig: thinkingConfig}
}

// ApplyThinkingConfig applies the planner's thinking configuration to request.
// The planner configuration takes precedence over GenerateContentConfig.
func (p *BuiltInPlanner) ApplyThinkingConfig(request *model.LLMRequest) {
	if p.thinkingConfig == nil {
		return
	}
	if request.Config == nil {
		request.Config = &genai.GenerateContentConfig{}
	}
	request.Config.ThinkingConfig = p.thinkingConfig
}

// BuildPlanningInstruction implements Planner. Built-in planning does not need
// an additional system instruction.
func (*BuiltInPlanner) BuildPlanningInstruction(context.Context, agent.ReadonlyContext, *model.LLMRequest) string {
	return ""
}

// ProcessPlanningResponse implements Planner. Built-in planning does not
// rewrite response parts.
func (*BuiltInPlanner) ProcessPlanningResponse(context.Context, agent.CallbackContext, []*genai.Part) []*genai.Part {
	return nil
}
