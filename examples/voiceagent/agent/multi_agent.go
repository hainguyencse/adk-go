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
// Root: ERA Singapore - welcome and routing
// Sub1: PAAgent - Search Location & Get Transaction Summary By Bedrooms (via MCP server)
// Sub2: PropertyReportAgent - Search Location, Select Unit, Select Sections, Create Report, Get Report
// ============================================================

// NewMultiAgentSystem creates a multi-agent system with:
// - Root agent: Handles info and routes to sub-agents
// - PAAgent sub-agent: Search Location & Get Transaction Summary By Bedrooms
// - PropertyReportAgent sub-agent: Generate property reports for specific units
func NewMultiAgentSystem(ctx context.Context) (adkagent.Agent, error) {
	// Create Gemini model (shared across all agents)
	modelLLM, err := gemini.NewModel(ctx, "gemini-2.5-flash-native-audio-preview-09-2025", &genai.ClientConfig{
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

	// ----- Function tools for state management -----

	// Property Analysis function tools
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

	getPropertyAnalysisTransactionSummaryParamsTool, err := functiontool.New(
		functiontool.Config{
			Name:        "get_property_analysis_transaction_summary_params",
			Description: "Retrieve the selected projectId and contractDateRangeType from previous steps",
		},
		getPropertyAnalysisTransactionSummaryParams,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create getPropertyAnalysisTransactionSummaryParams tool: %w", err)
	}

	// Property Report function tools
	getProjectIDTool, err := functiontool.New(
		functiontool.Config{
			Name:        "get_project_id",
			Description: "Retrieve the selected projectId from session state",
		},
		getProjectID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create get_project_id tool: %w", err)
	}

	confirmUnitTool, err := functiontool.New(
		functiontool.Config{
			Name:        "confirm_unit",
			Description: "Confirm the user's unit selection and store the unitId for later use",
		},
		confirmUnit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create confirm_unit tool: %w", err)
	}

	confirmPropertyReportSectionsTool, err := functiontool.New(
		functiontool.Config{
			Name:        "confirm_property_report_sections",
			Description: "Confirm the user's section selection and store the sections list for report creation",
		},
		confirmSections,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create confirm_property_report_sections tool: %w", err)
	}

	getCreatePropertyReportParamsTool, err := functiontool.New(
		functiontool.Config{
			Name:        "get_create_property_report_params",
			Description: "Retrieve the selected projectId, unitId, and sections from previous steps for report creation",
		},
		getCreateReportParams,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create get_create_property_report_params tool: %w", err)
	}

	storePropertyReportIDTool, err := functiontool.New(
		functiontool.Config{
			Name:        "store_property_report_id",
			Description: "Store the property report ID from the create report response for later retrieval",
		},
		storeReportID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create store_property_report_id tool: %w", err)
	}

	getReportIDTool, err := functiontool.New(
		functiontool.Config{
			Name:        "get_property_report_id",
			Description: "Retrieve the property report ID from session state",
		},
		getReportID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create get_property_report_id tool: %w", err)
	}

	waitOneSecondTool, err := functiontool.New(
		functiontool.Config{
			Name:        "wait_one_second",
			Description: "Wait 1 second before the next action (used for polling report status)",
		},
		waitOneSecond,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create wait_one_second tool: %w", err)
	}

	// // Back to menu tool (shared across all sub-agents in sequential workflows)
	backToMenuTool, err := functiontool.New(
		functiontool.Config{
			Name:        "back_to_menu",
			Description: "Return to the main menu. Call this when the user wants to go back, cancel, or start over.",
		},
		backToMenu,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create back_to_menu tool: %w", err)
	}

	// ----- Init Agents -----

	// Search Location Agent (MCP search + confirm_project)
	searchLocationAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "search_location_agent",
		Description: "Search for property locations/projects by keyword",
		Instruction: SearchLocationAgentPrompt,
		Model:       modelLLM,
		Tools: []tool.Tool{
			confirmProjectTool,
			backToMenuTool,
		},
		Toolsets: []tool.Toolset{
			salesPlusMCPToolSet,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create search location agent: %w", err)
	}

	// Date Range Selection Agent (list options + confirm selection)
	dateRangeAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "date_range_agent",
		Description: "Select date range type for transaction analysis (1y, 3y, 5y, 10y)",
		Instruction: DateRangeAgentPrompt,
		Model:       modelLLM,
		Tools: []tool.Tool{
			listDateRangeOptionsTool,
			confirmDateRangeTool,
			backToMenuTool,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create date range agent: %w", err)
	}

	// Calculate Property Analysis Transaction Summary Agent (get params from state + MCP call)
	calculatePropertyAnalysisTransactionSummaryAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "calculate_property_analysis_transaction_summary_agent",
		Description: "Get project transaction summary breakdowns by bedrooms",
		Instruction: TransactionSummaryAgentPrompt,
		Model:       modelLLM,
		Tools: []tool.Tool{
			getPropertyAnalysisTransactionSummaryParamsTool,
			backToMenuTool,
		},
		Toolsets: []tool.Toolset{
			salesPlusMCPToolSet,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create property analysis transaction summary agent: %w", err)
	}

	// [root_agent] / Property Analysis Transaction Summary Agent: sequential workflow (search -> date range -> transaction summary)
	propertyAnalysisTransactionSummaryAgent, err := sequentialagent.New(sequentialagent.Config{
		AgentConfig: adkagent.Config{
			Name:        "property_analysis_transaction_summary_agent",
			Description: "Property Analysis - sequential workflow: search location, select date range, calculate transaction summary",
			SubAgents:   []adkagent.Agent{searchLocationAgent, dateRangeAgent, calculatePropertyAnalysisTransactionSummaryAgent},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create PA agent: %w", err)
	}

	// Property Report: list steps tool
	listStepsCreatePropertyReportTool, err := functiontool.New(
		functiontool.Config{
			Name:        "ListStepsCreatePropertyReport",
			Description: "List the steps for creating a property report",
		},
		listStepsCreatePropertyReport,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create ListStepsCreatePropertyReport tool: %w", err)
	}

	// Property Report Introduction Agent (tells user the workflow steps)
	propertyReportIntroAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "property_report_intro_agent",
		Description: "Introduce the property report workflow steps to the user",
		Instruction: PropertyReportIntroAgentPrompt,
		Model:       modelLLM,
		Tools: []tool.Tool{
			listStepsCreatePropertyReportTool,
			backToMenuTool,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create property report intro agent: %w", err)
	}

	// Search Location Agent (separate instance - agents can only have 1 parent)
	propertyReportSearchLocationAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "property_report_search_location_agent",
		Description: "Search for property locations/projects by keyword (for property report)",
		Instruction: SearchLocationAgentPrompt,
		Model:       modelLLM,
		Tools: []tool.Tool{
			confirmProjectTool,
			backToMenuTool,
		},
		Toolsets: []tool.Toolset{
			salesPlusMCPToolSet,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create search location agent (PR): %w", err)
	}

	// --- Function tools for property report ---

	// Step 2: Project Unit Agent (search units + confirm selection)
	projectUnitAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "project_unit_agent",
		Description: "Search and select a specific unit within a project",
		Instruction: ProjectUnitAgentPrompt,
		Model:       modelLLM,
		Tools: []tool.Tool{
			getProjectIDTool,
			confirmUnitTool,
			backToMenuTool,
		},
		Toolsets: []tool.Toolset{
			salesPlusMCPToolSet,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create project unit agent: %w", err)
	}

	// Step 3: Property Report Section Agent (list sections + confirm selection)
	propertyReportSectionAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "property_report_section_agent",
		Description: "Select report sections to include in the property report",
		Instruction: PropertyReportSectionAgentPrompt,
		Model:       modelLLM,
		Tools: []tool.Tool{
			confirmPropertyReportSectionsTool,
			backToMenuTool,
		},
		Toolsets: []tool.Toolset{
			salesPlusMCPToolSet,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create property report section agent: %w", err)
	}

	// Step 4: Create Property Report Agent (get params + MCP create)
	createPropertyReportAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "create_property_report_agent",
		Description: "Create a property report with selected project, unit, and sections",
		Instruction: CreatePropertyReportAgentPrompt,
		Model:       modelLLM,
		Tools: []tool.Tool{
			getCreatePropertyReportParamsTool,
			storePropertyReportIDTool,
			backToMenuTool,
		},
		Toolsets: []tool.Toolset{
			salesPlusMCPToolSet,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create create property report agent: %w", err)
	}

	// Step 5: Get Property Report Agent (polls until ready)
	getPropertyReportAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "get_property_report_agent",
		Description: "Poll property report status until ready and share download link",
		Instruction: GetPropertyReportAgentPrompt,
		Model:       modelLLM,
		Tools: []tool.Tool{
			getReportIDTool,
			waitOneSecondTool,
			backToMenuTool,
		},
		Toolsets: []tool.Toolset{
			salesPlusMCPToolSet,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create get property report agent: %w", err)
	}

	// Property Report Agent: sequential workflow (intro -> search -> unit -> sections -> create -> get)
	propertyReportAgent, err := sequentialagent.New(sequentialagent.Config{
		AgentConfig: adkagent.Config{
			Name:        "property_report_agent",
			Description: "Property Report - sequential workflow: search location, select unit, select sections, create report, get report",
			SubAgents:   []adkagent.Agent{propertyReportIntroAgent, propertyReportSearchLocationAgent, projectUnitAgent, propertyReportSectionAgent, createPropertyReportAgent, getPropertyReportAgent},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create property report agent: %w", err)
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
			propertyAnalysisTransactionSummaryAgent,
			propertyReportAgent,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create root agent: %w", err)
	}

	log.Printf("Created Multi-Agent System:")
	log.Printf("  Root Agent: %s", rootAgent.Name())
	log.Printf("  Sub-Agent: %s (Property Analysis Sequential)", propertyAnalysisTransactionSummaryAgent.Name())
	log.Printf("    Step 1: %s (Search Location)", searchLocationAgent.Name())
	log.Printf("    Step 2: %s (Date Range)", dateRangeAgent.Name())
	log.Printf("    Step 3: %s (Transaction Summary)", calculatePropertyAnalysisTransactionSummaryAgent.Name())
	log.Printf("  Sub-Agent: %s (Property Report Sequential)", propertyReportAgent.Name())
	log.Printf("    Step 0: %s (Introduction)", propertyReportIntroAgent.Name())
	log.Printf("    Step 1: %s (Search Location)", propertyReportSearchLocationAgent.Name())
	log.Printf("    Step 2: %s (Project Unit)", projectUnitAgent.Name())
	log.Printf("    Step 3: %s (Section Selection)", propertyReportSectionAgent.Name())
	log.Printf("    Step 4: %s (Create Report)", createPropertyReportAgent.Name())
	log.Printf("    Step 5: %s (Get Report)", getPropertyReportAgent.Name())

	return rootAgent, nil
}
