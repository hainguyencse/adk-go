package agent

import (
	"context"
	"fmt"
	"iter"
	"os"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	adkagentllm "google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/agent/workflowagents/loopagent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// mapPlusRunLive runs search → analytics → summary in sequence.
// Any sub-agent after search_agent can call restart_sequence to restart from search_agent.
func mapPlusRunLive(ctx adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		subAgents := ctx.Agent().SubAgents()
	restart:
		for {
			fmt.Println("[mapPlusRunLive] start sequence")

			for _, subAgent := range subAgents {
				ctx.SetLiveSessionResumptionHandle("")

				shouldRestart := false
				for event, err := range subAgent.RunLive(ctx) {
					if !yield(event, err) {
						return
					}
					if err != nil {
						return
					}
					if event != nil && event.Actions.Escalate {
						if v, ok := event.Actions.StateDelta[restartSequenceStateKey]; ok && v == true {
							shouldRestart = true
						}
						break
					}
				}

				if shouldRestart {
					fmt.Println("[mapPlusRunLive] restarting from search_agent")
					continue restart
				}
			}

			fmt.Println("[mapPlusRunLive] end sequence")
			break restart
		}
	}
}

func GetModel(ctx context.Context) (adkmodel.LLM, error) {
	// gemini-2.5-flash-native-audio-preview-09-2025
	// gemini-2.0-flash-live-001
	modelName := os.Getenv("MODEL_NAME")
	if modelName == "" {
		modelName = "gemini-2.5-flash-native-audio-preview-09-2025"
	}

	modelLLM, err := gemini.NewModel(ctx, modelName, &genai.ClientConfig{
		APIKey: os.Getenv("GOOGLE_API_KEY"),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create model: %w", err)
	}

	return modelLLM, err
}

func NewMapPlusAgent(ctx context.Context, llmModel adkmodel.LLM) (adkagent.Agent, error) {
	taskCompletedTool, err := newTaskCompletedTool()
	if err != nil {
		return nil, fmt.Errorf("failed to create tool: %w", err)
	}

	restartSequenceTool, err := newRestartSequenceTool()
	if err != nil {
		return nil, fmt.Errorf("failed to create tool: %w", err)
	}

	searchLocationTool, err := newSearchLocationTool()
	if err != nil {
		return nil, fmt.Errorf("failed to create tool: %w", err)
	}

	analyticsLocationTool, err := newAnalyticsLocationTool()
	if err != nil {
		return nil, fmt.Errorf("failed to create tool: %w", err)
	}

	summaryLocationTool, err := newSummaryLocationTool()
	if err != nil {
		return nil, fmt.Errorf("failed to create tool: %w", err)
	}

	// search_agent: task_completed only (first agent, no restart).
	// IncludeContentsNone prevents the accumulated session history (tool calls
	// from prior pipeline runs) from being sent via SendHistory, which causes
	// a Gemini Live 1011 error when the history grows large.
	// search_agent only needs the live audio from the current user request.
	searchAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:            "search_agent",
		Description:     "Parse map plus search, filter, sort from user requirements",
		Instruction:     searchAgentPrompt,
		Model:           llmModel,
		Tools:           []tool.Tool{searchLocationTool, taskCompletedTool},
		IncludeContents: adkagentllm.IncludeContentsNone,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	// analytics_agent: task_completed + restart_sequence
	// IncludeContentsNone + session state injection ({search_result}) avoids
	// cross-round history pollution while still receiving search_agent's output.
	analyticsAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:            "analytics_agent",
		Description:     "Get and analytics to find best projects base on search params",
		Instruction:     analyticsAgentPrompt,
		Model:           llmModel,
		Tools:           []tool.Tool{analyticsLocationTool, taskCompletedTool, restartSequenceTool},
		IncludeContents: adkagentllm.IncludeContentsNone,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	// summary_agent: task_completed + restart_sequence
	// IncludeContentsNone + session state injection ({analytics_result}) avoids
	// cross-round history pollution while still receiving analytics_agent's output.
	summaryAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:            "summary_agent",
		Description:     "Get project ids from analytics_agent and summary projects detail info",
		Instruction:     summaryAgentPrompt,
		Model:           llmModel,
		Tools:           []tool.Tool{summaryLocationTool, taskCompletedTool, restartSequenceTool},
		IncludeContents: adkagentllm.IncludeContentsNone,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	mapPlusAgent, err := loopagent.New(loopagent.Config{
		AgentConfig: adkagent.Config{
			Name:        "map_plus_agent",
			Description: "Executes search → analytics → summary in sequence with restart support",
			SubAgents:   []adkagent.Agent{searchAgent, analyticsAgent, summaryAgent},
			RunLive:     mapPlusRunLive,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	return mapPlusAgent, nil
}

func NewRootAgent(ctx context.Context, llmModel adkmodel.LLM) (adkagent.Agent, error) {
	mapPlusAgent, err := NewMapPlusAgent(ctx, llmModel)
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	rootAgent, err := llmagent.New(adkagentllm.Config{
		Name:        "root_agent",
		Description: "Root Agent. Transfer to agent if user request search map, search project",
		Instruction: rootAgentPrompt,
		Model:       llmModel,
		Tools:       []tool.Tool{},
		SubAgents: []adkagent.Agent{
			mapPlusAgent,
		},
	})

	return rootAgent, nil
}
