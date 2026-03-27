package agent

import (
	"context"
	"fmt"
	"os"

	adkagent "google.golang.org/adk/agent"
	adkagentllm "google.golang.org/adk/agent/llmagent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

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

func NewMapAgent(ctx context.Context, llmModel adkmodel.LLM) (adkagent.Agent, error) {
	executeMapQueryTool, err := newExecuteMapQueryTool()
	if err != nil {
		return nil, fmt.Errorf("failed to create execute_map_query tool: %w", err)
	}

	mapAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "map_agent",
		Description: "Search projects (Condo/HDB/Landed) with summary transactions/listings by nearby Location",
		Instruction: mapAgentPrompt,
		Model:       llmModel,
		Tools: []tool.Tool{
			executeMapQueryTool,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create map agent: %w", err)
	}

	return mapAgent, nil
}

func NewMultiAgent(ctx context.Context, llmModel adkmodel.LLM) (adkagent.Agent, error) {
	mapAgent, err := NewMapAgent(ctx, llmModel)
	if err != nil {
		return nil, fmt.Errorf("failed to create map agent: %w", err)
	}

	rootAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "root_agent",
		Description: "Root Agent. Use to transfer to agent for related feature/topic. SubAgents: map_agent",
		Instruction: rootAgentPrompt,
		Model:       llmModel,
		Tools:       []tool.Tool{},
		SubAgents: []adkagent.Agent{
			mapAgent,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create root agent: %w", err)
	}

	return rootAgent, nil
}
