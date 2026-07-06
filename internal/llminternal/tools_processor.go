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
	"fmt"
	"iter"

	"google.golang.org/adk/agent"
	icontext "google.golang.org/adk/internal/context"
	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
)

// toolProcessor populates f.Tools from the agent's tools and toolsets.
// Tools are cached in f.Tools after the first call. If any toolset implements
// tool.DynamicToolset and returns NeedsMidTurnReload() == true, the cache is
// invalidated before each LLM call so that newly activated skills' additional
// tools become available within the same turn.
func toolProcessor(ctx agent.InvocationContext, req *model.LLMRequest, f *Flow) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		llmAgent, ok := ctx.Agent().(Agent)
		if !ok {
			yield(nil, fmt.Errorf("agent %v is not an LLMAgent", ctx.Agent().Name()))
			return
		}

		if f.Tools != nil {
			// Check whether any toolset needs mid-turn reload (e.g. SkillToolset
			// after a skill has been activated this turn).
			needsReload := false
			for _, ts := range Reveal(llmAgent).Toolsets {
				if dts, ok := ts.(tool.DynamicToolset); ok && dts.NeedsMidTurnReload() {
					needsReload = true
					break
				}
			}
			if !needsReload {
				return
			}
			f.Tools = nil
		}

		tools := Reveal(llmAgent).Tools
		for _, toolSet := range Reveal(llmAgent).Toolsets {
			tsTools, err := toolSet.Tools(icontext.NewReadonlyContext(ctx))
			if err != nil {
				yield(nil, fmt.Errorf("failed to extract tools from the tool set %q: %w", toolSet.Name(), err))
				return
			}

			tools = append(tools, tsTools...)
		}
		f.Tools = tools
	}
}
