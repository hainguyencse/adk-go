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

// Package sequentialagent provides an agent that runs its sub-agents in a sequence.
package sequentialagent

import (
	"fmt"
	"iter"

	"google.golang.org/adk/agent"
	agentinternal "google.golang.org/adk/internal/agent"
	"google.golang.org/adk/internal/llminternal"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

const (
	taskCompletedToolName = "task_completed"
	taskCompletedInstr    = `
If you finished the user's request according to its description, call the task_completed function to exit so the next agents can take over. When calling this function, do not generate any text other than the function call.`
)

// taskCompletedArgs is the input type for the task_completed tool (no args needed).
type taskCompletedArgs struct{}

// taskCompletedResult is the output type for the task_completed tool.
type taskCompletedResult struct {
	Result string `json:"result"`
}

// newTaskCompletedTool creates the task_completed function tool.
// When the model calls this tool, it sets Escalate=true on the event actions,
// signaling the sequential agent to move to the next sub-agent.
func newTaskCompletedTool() (tool.Tool, error) {
	return functiontool.New(
		functiontool.Config{
			Name:        taskCompletedToolName,
			Description: "Signals that the agent has successfully completed the user's question or task.",
		},
		func(ctx tool.Context, args taskCompletedArgs) (taskCompletedResult, error) {
			ctx.Actions().Escalate = true
			return taskCompletedResult{Result: "Task completion signaled."}, nil
		},
	)
}

// New creates a SequentialAgent.
//
// SequentialAgent executes its sub-agents once, in the order they are listed.
//
// Use the SequentialAgent when you want the execution to occur in a fixed,
// strict order.
func New(cfg Config) (agent.Agent, error) {
	// // Set RunLive before passing to loopagent so it overrides the default.
	if cfg.AgentConfig.Run != nil {
		return nil, fmt.Errorf("LoopAgent doesn't allow custom Run implementations")
	}

	sequentialAgentImpl := &sequentialAgent{}
	cfg.AgentConfig.Run = sequentialAgentImpl.Run

	sequentialAgent, err := agent.New(cfg.AgentConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create base agent: %w", err)
	}

	internalAgent, ok := sequentialAgent.(agentinternal.Agent)
	if !ok {
		return nil, fmt.Errorf("internal error: failed to convert to internal agent")
	}
	state := agentinternal.Reveal(internalAgent)
	state.AgentType = agentinternal.TypeSequentialAgent
	state.Config = cfg

	return sequentialAgent, nil
}

// Config defines the configuration for a SequentialAgent.
type Config struct {
	// Basic agent setup.
	AgentConfig agent.Config
}

// injectTaskCompletedTool adds the task_completed tool and instruction to each
// LlmAgent sub-agent that doesn't already have it. This allows the model to
// signal completion in live mode.
func injectTaskCompletedTool(subAgents []agent.Agent) error {
	var tcTool tool.Tool

	for _, subAgent := range subAgents {
		llmA, ok := subAgent.(llminternal.Agent)
		if !ok {
			continue
		}
		state := llminternal.Reveal(llmA)

		// Dedup: skip if already injected.
		hasTC := false
		for _, t := range state.Tools {
			if t.Name() == taskCompletedToolName {
				hasTC = true
				break
			}
		}
		if hasTC {
			continue
		}

		// Lazily create the tool (only if we have at least one LlmAgent).
		if tcTool == nil {
			var err error
			tcTool, err = newTaskCompletedTool()
			if err != nil {
				return err
			}
		}

		state.Tools = append(state.Tools, tcTool)
		state.Instruction += taskCompletedInstr
	}
	return nil
}

type sequentialAgent struct{}

func (a *sequentialAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		for _, subAgent := range ctx.Agent().SubAgents() {
			for event, err := range subAgent.Run(ctx) {
				// TODO: ensure consistency -- if there's an error, return and close iterator, verify everywhere in ADK.
				if !yield(event, err) {
					return
				}
			}
		}
	}
}

func (a *sequentialAgent) RunLive(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		subAgents := ctx.Agent().SubAgents()
		if len(subAgents) == 0 {
			return
		}

		if err := injectTaskCompletedTool(subAgents); err != nil {
			yield(nil, fmt.Errorf("failed to initialize task_completed tool: %w", err))
			return
		}

		for _, subAgent := range subAgents {
			// Clear resumption handle between sub-agents since each gets a fresh
			// live session with different tools/instructions.
			ctx.SetLiveSessionResumptionHandle("")

			for event, err := range subAgent.RunLive(ctx) {
				if !yield(event, err) {
					return
				}
				// When task_completed is called, the event has Escalate=true.
				// Break to move to the next sub-agent.
				if event != nil && event.Actions.Escalate {
					break
				}
			}
		}
	}
}
