package agent

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	adkagent "google.golang.org/adk/agent"
	adkagentllm "google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/agent/workflowagents/sequentialagent"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/adk/tool/mcptoolset"
	"google.golang.org/genai"
)

// ============================================================
// Multi-Agent System
// Root: SALES PLUS AGENT - welcome and routing
// Sub1: PAAgent - Search Location & Get Transaction Summary By Bedrooms (via MCP server)
// ============================================================

// NewMultiAgentSystem creates a multi-agent system with:
// - Root agent: Handles info and routes to sub-agents
// - PAAgent sub-agent: Search Location & Get Transaction Summary By Bedrooms
func NewMultiAgentSystem(ctx context.Context) (adkagent.Agent, error) {
	// Create Gemini model (shared across all agents)
	modelLLM, err := gemini.NewModel(ctx, "gemini-2.5-flash-native-audio-preview-12-2025", &genai.ClientConfig{
		APIKey: os.Getenv("GOOGLE_API_KEY"),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create model: %w", err)
	}

	// ========== MCP Server ==========
	salesPlusMCPServer := os.Getenv("SALES_PLUS_MCP")
	if salesPlusMCPServer == "" {
		salesPlusMCPServer = "http://localhost:8888"
	}

	salesPlusMCPTransport := &mcp.StreamableClientTransport{
		Endpoint: fmt.Sprintf("%s/streaming", salesPlusMCPServer),
	}

	salesPlusMCPToolSet, err := mcptoolset.New(mcptoolset.Config{
		Transport: salesPlusMCPTransport,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create sales MCP tool set: %w", err)
	}

	// ========== PA Sequential Agent (3 steps) ==========

	// --- Function tools for state management ---
	confirmProjectTool, err := functiontool.New(
		functiontool.Config{
			Name:        "confirm_project",
			Description: "Confirm the user's project selection and store the projectId for later use",
		},
		confirmProject,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create confirm_project tool: %w", err)
	}

	listDateRangeOptionsTool, err := functiontool.New(
		functiontool.Config{
			Name:        "list_date_range_options",
			Description: "List available date range options for transaction analysis",
		},
		listDateRangeOptions,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create list_date_range_options tool: %w", err)
	}

	confirmDateRangeTool, err := functiontool.New(
		functiontool.Config{
			Name:        "confirm_date_range",
			Description: "Confirm the user's date range selection and store it for later use",
		},
		confirmDateRange,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create confirm_date_range tool: %w", err)
	}

	getAnalysisParamsTool, err := functiontool.New(
		functiontool.Config{
			Name:        "get_analysis_params",
			Description: "Retrieve the selected projectId and contractDateRangeType from previous steps",
		},
		getAnalysisParams,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create get_analysis_params tool: %w", err)
	}

	// Step 1: Search Location Agent (MCP search + confirm_project)
	searchLocationAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "search_location_agent",
		Description: "Search for property locations/projects by keyword",
		Instruction: SearchLocationAgentPrompt,
		Model:       modelLLM,
		Tools: []tool.Tool{
			confirmProjectTool,
		},
		Toolsets: []tool.Toolset{
			salesPlusMCPToolSet,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create search location agent: %w", err)
	}

	// Step 2: Date Range Selection Agent (list options + confirm selection)
	dateRangeAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "date_range_agent",
		Description: "Select date range type for transaction analysis (1y, 3y, 5y, 10y)",
		Instruction: DateRangeAgentPrompt,
		Model:       modelLLM,
		Tools: []tool.Tool{
			listDateRangeOptionsTool,
			confirmDateRangeTool,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create date range agent: %w", err)
	}

	// Step 3: Transaction Summary Agent (get params from state + MCP call)
	transactionSummaryAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "transaction_summary_agent",
		Description: "Get project transaction summary breakdowns by bedrooms",
		Instruction: TransactionSummaryAgentPrompt,
		Model:       modelLLM,
		Tools: []tool.Tool{
			getAnalysisParamsTool,
		},
		Toolsets: []tool.Toolset{
			salesPlusMCPToolSet,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create transaction summary agent: %w", err)
	}

	// PA Agent: sequential workflow (search -> date range -> transaction summary -> export PDF)
	paAgent, err := sequentialagent.New(sequentialagent.Config{
		AgentConfig: adkagent.Config{
			Name:        "pa_agent",
			Description: "Property Analysis - sequential workflow: search location, select date range, get transaction summary",
			SubAgents:   []adkagent.Agent{searchLocationAgent, dateRangeAgent, transactionSummaryAgent},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create PA agent: %w", err)
	}

	// ========== Create Root Agent ==========
	companyInfoTool, err := functiontool.New(
		functiontool.Config{
			Name:        "GetCompanyInfo",
			Description: "ERA Singapore is one of the leading real estate agencies in Singapore, providing comprehensive property services including residential, commercial, and project marketing",
		},
		getCompanyInfo,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create GetCompanyInfo tool: %w", err)
	}

	listCapabilitiesTool, err := functiontool.New(
		functiontool.Config{
			Name:        "list_capabilities",
			Description: "Display the list of available services/capabilities in the chatbox for the user to read",
		},
		listCapabilities,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create list_capabilities tool: %w", err)
	}

	rootAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "root_agent",
		Description: "Main for ERA Singapore - handles company info and routes to PA or Sales departments",
		Instruction: ERARootAgentSystemPrompt,
		Model:       modelLLM,
		Tools: []tool.Tool{
			companyInfoTool,
			listCapabilitiesTool,
		},
		SubAgents: []adkagent.Agent{
			paAgent,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create root agent: %w", err)
	}

	log.Printf("Created Multi-Agent System:")
	log.Printf("  Root Agent: %s", rootAgent.Name())
	log.Printf("  Sub-Agent: %s (PA Sequential)", paAgent.Name())
	log.Printf("    Step 1: %s (Search Location)", searchLocationAgent.Name())
	log.Printf("    Step 2: %s (Date Range)", dateRangeAgent.Name())
	log.Printf("    Step 3: %s (Transaction Summary)", transactionSummaryAgent.Name())

	return rootAgent, nil
}
