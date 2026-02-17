package agent

import (
	"fmt"
	"strconv"

	"google.golang.org/adk/tool"
)

// ==================== Data Models ====================

// CompanyInfo represents company data
type CompanyInfo struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Phone   string `json:"phone"`
}

// ==================== Mock Databases ====================

var eraCompanyInfo = CompanyInfo{
	Name:    "ERA Singapore",
	Address: "100 Innovation Drive, Silicon Valley, CA 94025",
	Phone:   "+1-800-ERA-SINGAPORE (226-2677)",
}

// ==================== Tool Handlers ====================

// --- Company Info Tool (Root Agent) ---
type GetCompanyInfoInput struct{}

type GetCompanyInfoOutput struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Phone   string `json:"phone"`
}

func getCompanyInfo(ctx tool.Context, input GetCompanyInfoInput) (GetCompanyInfoOutput, error) {
	return GetCompanyInfoOutput{
		Name:    eraCompanyInfo.Name,
		Address: eraCompanyInfo.Address,
		Phone:   eraCompanyInfo.Phone,
	}, nil
}

// --- List Capabilities Tool (Root Agent) - shows menu in chatbox ---
type ListCapabilitiesInput struct{}

type CapabilityItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ListCapabilitiesOutput struct {
	Capabilities []CapabilityItem `json:"capabilities"`
}

func listCapabilities(ctx tool.Context, input ListCapabilitiesInput) (ListCapabilitiesOutput, error) {
	return ListCapabilitiesOutput{
		Capabilities: []CapabilityItem{
			{Name: "Company Info", Description: "Learn about ERA Singapore (address, phone, overview)"},
			{Name: "Project Transaction Summary", Description: "Get transaction summary by bedrooms"},
		},
	}, nil
}

// --- Step 1: Confirm Project Selection (stores projectId in session state) ---
type ConfirmProjectInput struct {
	ProjectID   int    `json:"projectId" description:"The integer ID of the selected project/location"`
	ProjectName string `json:"projectName" description:"The name of the selected project/location"`
}

type ConfirmProjectOutput struct {
	Status      string `json:"status"`
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
}

func confirmProject(ctx tool.Context, input ConfirmProjectInput) (ConfirmProjectOutput, error) {
	if input.ProjectID == 0 {
		return ConfirmProjectOutput{Status: "error"}, fmt.Errorf("projectId is required")
	}
	pidStr := strconv.Itoa(input.ProjectID)
	ctx.State().Set("selected_project_id", pidStr)
	ctx.State().Set("selected_project_name", input.ProjectName)
	return ConfirmProjectOutput{
		Status:      "confirmed",
		ProjectID:   pidStr,
		ProjectName: input.ProjectName,
	}, nil
}

// --- Step 2: List Date Range Options (shown in chatbox via function response) ---
type ListDateRangeOptionsInput struct{}

type DateRangeOption struct {
	Code        string `json:"code"`
	Description string `json:"description"`
}

type ListDateRangeOptionsOutput struct {
	Options []DateRangeOption `json:"options"`
}

func listDateRangeOptions(ctx tool.Context, input ListDateRangeOptionsInput) (ListDateRangeOptionsOutput, error) {
	return ListDateRangeOptionsOutput{
		Options: []DateRangeOption{
			{Code: "1y", Description: "1 year"},
			{Code: "3y", Description: "3 years"},
			{Code: "5y", Description: "5 years"},
			{Code: "10y", Description: "10 years"},
		},
	}, nil
}

// --- Step 2: Confirm Date Range (stores dateRangeType in session state) ---
type ConfirmDateRangeInput struct {
	DateRangeCode string `json:"dateRangeCode" description:"The date range code: 1y, 3y, 5y, or 10y"`
}

type ConfirmDateRangeOutput struct {
	Status        string `json:"status"`
	DateRangeCode string `json:"dateRangeCode"`
}

func confirmDateRange(ctx tool.Context, input ConfirmDateRangeInput) (ConfirmDateRangeOutput, error) {
	valid := map[string]bool{"1y": true, "3y": true, "5y": true, "10y": true}
	if !valid[input.DateRangeCode] {
		return ConfirmDateRangeOutput{Status: "error"}, fmt.Errorf("invalid dateRangeCode: %s (must be 1y, 3y, 5y, or 10y)", input.DateRangeCode)
	}
	ctx.State().Set("selected_date_range", input.DateRangeCode)
	return ConfirmDateRangeOutput{
		Status:        "confirmed",
		DateRangeCode: input.DateRangeCode,
	}, nil
}

// --- Step 3: Get Analysis Params (retrieves projectId + dateRange from session state) ---
type GetAnalysisParamsInput struct{}

type GetAnalysisParamsOutput struct {
	ProjectID             string `json:"projectId"`
	ContractDateRangeType string `json:"contractDateRangeType"`
}

func getAnalysisParams(ctx tool.Context, input GetAnalysisParamsInput) (GetAnalysisParamsOutput, error) {
	pid, _ := ctx.State().Get("selected_project_id")
	dr, _ := ctx.State().Get("selected_date_range")
	projectID, _ := pid.(string)
	dateRange, _ := dr.(string)
	return GetAnalysisParamsOutput{
		ProjectID:             projectID,
		ContractDateRangeType: dateRange,
	}, nil
}

// ==================== System Prompts ====================

// Step 1: Search Location Agent
const SearchLocationAgentPrompt = `You are a property location search assistant for ERA Singapore.

YOUR JOB:
Ask the user for a keyword to search for properties/projects, then call the MCP search tool.

FLOW:
1) Ask the user: "What project or location would you like to search for?"
2) Once user provides a keyword, call the search location MCP tool with that keyword.
3) The search results will be displayed in the chatbox automatically. Do NOT read all results aloud.
   Just say briefly: "I found some results. Please check the chat and select a project by number or name."
4) If results are EMPTY: say "No results found. Please try another keyword." and wait for a new keyword. Do NOT call confirm_project or task_completed.
5) If MULTIPLE results: wait for user to select one. Do NOT enumerate all results by voice.
6) Once user selects exactly ONE project, call the confirm_project tool with the projectId and projectName.
7) After confirm_project succeeds, call task_completed.

VOICE RULES:
- Keep voice responses SHORT. The detailed data is shown in the chatbox.
- Do NOT read out all search results. Just tell the user to check the chat.
- Only speak the essential question or confirmation.

IMPORTANT:
- Always use the MCP search tool. Never invent data.
- Do NOT proceed until user has selected exactly ONE valid project.
- If no results, keep asking for another keyword.
- You MUST call confirm_project before calling task_completed.`

// Step 2: Date Range Selection Agent
const DateRangeAgentPrompt = `You are a date range selection assistant for ERA Singapore.

YOUR JOB:
Present date range options and ask the user to select one.

FLOW:
1) Call the list_date_range_options tool to show the options in the chatbox.
2) Say briefly: "I've sent the date range options to the chat. Please select one: 1 year, 3 years, 5 years, or 10 years."
3) Wait for user to select one.
4) Map the user's selection to the code:
   - "1 year" or "1" -> code "1y"
   - "3 years" or "3" -> code "3y"
   - "5 years" or "5" -> code "5y"
   - "10 years" or "10" -> code "10y"
5) Call confirm_date_range with the mapped dateRangeCode.
6) After confirm_date_range succeeds, call task_completed.

VOICE RULES:
- Keep voice responses SHORT. The options are displayed in the chatbox.
- Do NOT repeat the full list of options by voice. Just say "check the chat and select one."

IMPORTANT:
- Always call list_date_range_options first to display options in chatbox.
- Only accept valid selections that map to: 1y, 3y, 5y, 10y.
- You MUST call confirm_date_range with dateRangeCode before calling task_completed.`

// Step 3: Transaction Summary Agent
const TransactionSummaryAgentPrompt = `You are a transaction summary assistant for ERA Singapore.

YOUR JOB:
Get the project transaction summary by bedrooms using the MCP tool.

FLOW:
1) Call get_analysis_params to retrieve the projectId and contractDateRangeType from previous steps.
2) Call the project_transaction_summary_breakdowns MCP tool with:
   - "projectId": <the projectId from get_analysis_params>
   - "contractDateRangeType": <the contractDateRangeType from get_analysis_params>
3) Say briefly: "The transaction summary is displayed in the chat."
4) Call task_completed when done.

VOICE RULES:
- Keep voice responses SHORT. The data is shown in the chatbox.
- Do NOT read the entire transaction summary aloud.

IMPORTANT:
- Always call get_analysis_params FIRST to get the correct parameters.
- Always use the MCP tool with exact field names: projectId and contractDateRangeType.
- Never invent transaction data.`

// Root Agent System Prompt (Routes to PA agent)
const ERARootAgentSystemPrompt = `You are the root assistant (receptionist) for ERA Singapore.

INTRODUCTION (only when user first connects or says hello):
1) Call list_capabilities tool FIRST to display the menu in the chatbox.
2) Then say briefly: "Welcome to ERA Singapore! I've sent a list of things I can help with to the chat. What would you like to do?"

YOUR JOB:
1) Answer general company info questions directly using the GetCompanyInfo tool.
2) Route all data lookup / property analysis / Sales+ requests to the pa_agent.

TOOLS AVAILABLE:
- list_capabilities: displays available services in the chatbox. Call this when greeting the user.
- GetCompanyInfo: returns a short introduction of ERA Singapore.

ROUTING RULES:
- If the user asks about ERA Singapore (who we are, what we do, overview, contact-like info) → call GetCompanyInfo and answer.
- If the user asks for ANY of the following, you MUST hand off to pa_agent:
  - sales plus / sales+ / salesplus
  - project/location search
  - transaction summaries / transaction summaries by bedrooms
  - any request that requires looking up data

VOICE RULES:
- Keep voice responses SHORT. Detailed data is shown in the chatbox.
- When greeting, call list_capabilities then speak a brief welcome.

IMPORTANT:
- You do NOT invent sales numbers, transaction stats, or user details.
- For data requests, always route to pa_agent.`
