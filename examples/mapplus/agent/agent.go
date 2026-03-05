package agent

import (
	"context"
	"fmt"
	"os"

	adkagent "google.golang.org/adk/agent"
	adkagentllm "google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/agent/workflowagents/sequentialagent"
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

func NewMapPlusAgent(ctx context.Context, llmModel adkmodel.LLM) (adkagent.Agent, error) {
	searchLocationTool, err := newSearchLocationTool()
	if err != nil {
		return nil, fmt.Errorf("failed to create tool: %w", err)
	}

	newAnalyticsLocationTool, err := newAnalyticsLocationTool()
	if err != nil {
		return nil, fmt.Errorf("failed to create tool: %w", err)
	}

	newSummaryLocationTool, err := newSummaryLocationTool()
	if err != nil {
		return nil, fmt.Errorf("failed to create tool: %w", err)
	}

	searchAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "search_agent",
		Description: "Parse map plus search, filter, sort from user requirements",
		Instruction: searchAgentPrompt,
		Model:       llmModel,
		Tools: []tool.Tool{
			searchLocationTool,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	analyticsAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "analytics_agent",
		Description: "Get and analytics to find best projects base on search params",
		Instruction: analyticsAgentPrompt,
		Model:       llmModel,
		Tools: []tool.Tool{
			newAnalyticsLocationTool,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	summaryAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "summary_agent",
		Description: "Get project ids from analytics_agent and summary projects detail info",
		Instruction: summaryAgentPrompt,
		Model:       llmModel,
		Tools: []tool.Tool{
			newSummaryLocationTool,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	mapPlusAgent, err := sequentialagent.New(sequentialagent.Config{
		AgentConfig: adkagent.Config{
			Name:        "map_plus_agent",
			Description: "Executes a sequence of search, analytics, summary",
			SubAgents:   []adkagent.Agent{searchAgent, analyticsAgent, summaryAgent},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	return mapPlusAgent, nil
}
