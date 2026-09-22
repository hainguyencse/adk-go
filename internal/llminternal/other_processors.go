// Copyright 2025 Google LLC
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
	"iter"

	"google.golang.org/adk/agent"
	icontext "google.golang.org/adk/internal/context"
	"google.golang.org/adk/internal/utils"
	"google.golang.org/adk/model"
	"google.golang.org/adk/planner"
	"google.golang.org/adk/session"
)

func nlPlanningRequestProcessor(ctx agent.InvocationContext, req *model.LLMRequest, f *Flow) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		configuredPlanner := plannerFor(ctx)
		if configuredPlanner == nil {
			return
		}

		if builtIn, ok := configuredPlanner.(*planner.BuiltInPlanner); ok {
			builtIn.ApplyThinkingConfig(req)
			return
		}

		if instruction := configuredPlanner.BuildPlanningInstruction(ctx, icontext.NewReadonlyContext(ctx), req); instruction != "" {
			utils.AppendInstructions(req, instruction)
		}
		removeThoughtFromRequest(req)
	}
}

func codeExecutionRequestProcessor(ctx agent.InvocationContext, req *model.LLMRequest, f *Flow) iter.Seq2[*session.Event, error] {
	// TODO: implement (adk-python src/google/adk/flows/llm_flows/_code_execution.py)
	return func(yield func(*session.Event, error) bool) {}
}

func authPreprocessor(ctx agent.InvocationContext, req *model.LLMRequest, f *Flow) iter.Seq2[*session.Event, error] {
	// TODO: implement (adk-python src/google/adk/auth/auth_preprocessor.py)
	return func(yield func(*session.Event, error) bool) {}
}

func nlPlanningResponseProcessor(ctx agent.InvocationContext, req *model.LLMRequest, resp *model.LLMResponse) error {
	if resp == nil || resp.Content == nil || len(resp.Content.Parts) == 0 {
		return nil
	}

	configuredPlanner := plannerFor(ctx)
	if configuredPlanner == nil {
		return nil
	}

	processedParts := configuredPlanner.ProcessPlanningResponse(ctx, icontext.NewCallbackContext(ctx), resp.Content.Parts)
	if len(processedParts) > 0 {
		resp.Content.Parts = processedParts
	}
	return nil
}

func plannerFor(ctx agent.InvocationContext) planner.Planner {
	llmAgent := asLLMAgent(ctx.Agent())
	if llmAgent == nil {
		return nil
	}
	return llmAgent.internal().Planner
}

func removeThoughtFromRequest(req *model.LLMRequest) {
	for _, content := range req.Contents {
		if content == nil {
			continue
		}
		for _, part := range content.Parts {
			if part != nil {
				part.Thought = false
			}
		}
	}
}

func codeExecutionResponseProcessor(ctx agent.InvocationContext, req *model.LLMRequest, resp *model.LLMResponse) error {
	// TODO: implement (adk-python src/google/adk_code_execution.py)
	return nil
}
