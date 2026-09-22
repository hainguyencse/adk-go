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

// Package planner defines planning strategies for LLM agents.
package planner

import (
	"context"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
)

// Planner allows an LLM agent to generate plans that guide its actions.
type Planner interface {
	// BuildPlanningInstruction builds a system instruction to append to the LLM
	// request. An empty string means that no instruction is needed.
	BuildPlanningInstruction(ctx context.Context, readonlyCtx agent.ReadonlyContext, request *model.LLMRequest) string

	// ProcessPlanningResponse processes model response parts. A nil or empty
	// result means that the response should remain unchanged.
	ProcessPlanningResponse(ctx context.Context, callbackCtx agent.CallbackContext, responseParts []*genai.Part) []*genai.Part
}
