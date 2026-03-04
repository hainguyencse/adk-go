package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

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

// NOTE: Capabilities is a JSON string (not []CapabilityItem) because the Gemini
// native audio preview model's Live API does not support array-type fields
// in function declarations (both parameters and response schemas).
type ListCapabilitiesOutput struct {
	Capabilities string `json:"capabilities" description:"JSON string containing the list of available capabilities"`
}

func listCapabilities(ctx tool.Context, input ListCapabilitiesInput) (ListCapabilitiesOutput, error) {
	type CapabilityItem struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	capabilities := []CapabilityItem{
		{Name: "Company Info", Description: "Learn about ERA Singapore (address, phone, overview)"},
		{Name: "Project Transaction Summary", Description: "Get transaction summary by bedrooms"},
		{Name: "Property Report", Description: "Generate a detailed property report for a specific unit"},
	}
	capJSON, _ := json.Marshal(capabilities)
	return ListCapabilitiesOutput{
		Capabilities: string(capJSON),
	}, nil
}

type ListStepsInput struct{}

// NOTE: Steps is a JSON string (not []StepItem) because the Gemini
// native audio preview model's Live API does not support array-type parameters
// in function declarations.
type ListStepsOutput struct {
	Steps string `json:"steps" description:"JSON string containing the list of steps for creating a property report"`
}

func listStepsCreatePropertyReport(ctx tool.Context, input ListStepsInput) (ListStepsOutput, error) {
	type StepItem struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	steps := []StepItem{
		{Name: "Select Location", Description: "Search for a property location or project"},
		{Name: "Select Unit", Description: "Select a specific unit within the project"},
		{Name: "Select Sections", Description: "Select the sections of the report to generate"},
		{Name: "Generate Report", Description: "Generate the detailed property report for the selected unit and sections"},
		{Name: "Get Report", Description: "Get the report download link once it's ready"},
	}
	stepsJSON, _ := json.Marshal(steps)
	return ListStepsOutput{
		Steps: string(stepsJSON),
	}, nil
}

// --- Step 1: Confirm Project Selection (stores projectId in session state) ---
type ConfirmProjectInput struct {
	ProjectID   string `json:"projectId" description:"The ID of the selected project/location"`
	ProjectName string `json:"projectName" description:"The name of the selected project/location"`
}

type ConfirmProjectOutput struct {
	Status      string `json:"status"`
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
}

func confirmProject(ctx tool.Context, input ConfirmProjectInput) (ConfirmProjectOutput, error) {
	if input.ProjectID == "" {
		return ConfirmProjectOutput{Status: "error"}, fmt.Errorf("projectId is required")
	}
	pidStr := input.ProjectID
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

// NOTE: Options is a JSON string (not []DateRangeOption) because the Gemini
// native audio preview model's Live API does not support array-type fields
// in function declarations (both parameters and response schemas).
type ListDateRangeOptionsOutput struct {
	Options string `json:"options" description:"JSON string containing the list of date range options"`
}

func listDateRangeOptions(ctx tool.Context, input ListDateRangeOptionsInput) (ListDateRangeOptionsOutput, error) {
	type DateRangeOption struct {
		Code        string `json:"code"`
		Description string `json:"description"`
	}
	options := []DateRangeOption{
		{Code: "1y", Description: "1 year"},
		{Code: "3y", Description: "3 years"},
		{Code: "5y", Description: "5 years"},
		{Code: "10y", Description: "10 years"},
	}
	optJSON, _ := json.Marshal(options)
	return ListDateRangeOptionsOutput{
		Options: string(optJSON),
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

func getPropertyAnalysisTransactionSummaryParams(ctx tool.Context, input GetAnalysisParamsInput) (GetAnalysisParamsOutput, error) {
	pid, _ := ctx.State().Get("selected_project_id")
	dr, _ := ctx.State().Get("selected_date_range")
	projectID, _ := pid.(string)
	dateRange, _ := dr.(string)
	return GetAnalysisParamsOutput{
		ProjectID:             projectID,
		ContractDateRangeType: dateRange,
	}, nil
}

// ==================== Property Report Tool Handlers ====================

// --- Get Project ID (retrieves projectId from session state for unit search) ---
type GetProjectIDInput struct{}

type GetProjectIDOutput struct {
	ProjectID string `json:"projectId"`
}

func getProjectID(ctx tool.Context, input GetProjectIDInput) (GetProjectIDOutput, error) {
	pid, _ := ctx.State().Get("selected_project_id")
	projectID, _ := pid.(string)
	return GetProjectIDOutput{
		ProjectID: projectID,
	}, nil
}

// --- Confirm Unit Selection (stores unitId in session state) ---
type ConfirmUnitInput struct {
	UnitID     string `json:"unitId" description:"The integer ID of the selected unit"`
	UnitNumber string `json:"unitNumber" description:"The unit number of the selected unit"`
	Address    string `json:"address" description:"The address of the selected unit"`
}

type ConfirmUnitOutput struct {
	Status     string `json:"status"`
	UnitID     string `json:"unitId"`
	UnitNumber string `json:"unitNumber"`
	Address    string `json:"address"`
}

func confirmUnit(ctx tool.Context, input ConfirmUnitInput) (ConfirmUnitOutput, error) {
	if input.UnitID == "" {
		return ConfirmUnitOutput{Status: "error"}, fmt.Errorf("unitId is required")
	}
	uidStr := input.UnitID
	ctx.State().Set("selected_unit_id", uidStr)
	ctx.State().Set("selected_unit_number", input.UnitNumber)
	return ConfirmUnitOutput{
		Status:     "confirmed",
		UnitID:     uidStr,
		UnitNumber: input.UnitNumber,
		Address:    input.Address,
	}, nil
}

// --- Confirm Sections (stores sections in session state) ---
// NOTE: Sections is a comma-separated string (not []string) because the Gemini
// native audio preview model's Live API does not support array-type parameters
// in function declarations.
type ConfirmSectionsInput struct {
	Sections string `json:"sections" description:"Comma-separated list of section names to include in the property report"`
}

type ConfirmSectionsOutput struct {
	Status   string `json:"status"`
	Sections string `json:"sections"`
}

func confirmSections(ctx tool.Context, input ConfirmSectionsInput) (ConfirmSectionsOutput, error) {
	if input.Sections == "" {
		return ConfirmSectionsOutput{Status: "error"}, fmt.Errorf("at least one section is required")
	}
	// Parse comma-separated string into array for storage
	var sections []string
	for _, s := range strings.Split(input.Sections, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			sections = append(sections, s)
		}
	}
	if len(sections) == 0 {
		return ConfirmSectionsOutput{Status: "error"}, fmt.Errorf("at least one section is required")
	}
	sectionsJSON, _ := json.Marshal(sections)
	ctx.State().Set("selected_sections", string(sectionsJSON))
	return ConfirmSectionsOutput{
		Status:   "confirmed",
		Sections: input.Sections,
	}, nil
}

// --- Get Create Report Params (retrieves projectId, unitId, sections from state) ---
type GetCreateReportParamsInput struct{}

type GetCreateReportParamsOutput struct {
	ProjectID string `json:"projectId"`
	UnitID    string `json:"unitId"`
	Sections  string `json:"sections" description:"Comma-separated list of section names"`
}

func getCreateReportParams(ctx tool.Context, input GetCreateReportParamsInput) (GetCreateReportParamsOutput, error) {
	pid, _ := ctx.State().Get("selected_project_id")
	uid, _ := ctx.State().Get("selected_unit_id")
	sectionsStr, _ := ctx.State().Get("selected_sections")

	projectID, _ := pid.(string)
	unitID, _ := uid.(string)

	// Convert stored JSON array back to comma-separated string
	var sections []string
	if s, ok := sectionsStr.(string); ok {
		json.Unmarshal([]byte(s), &sections)
	}

	return GetCreateReportParamsOutput{
		ProjectID: projectID,
		UnitID:    unitID,
		Sections:  strings.Join(sections, ", "),
	}, nil
}

// --- Store Report ID (stores report ID in session state) ---
type StoreReportIDInput struct {
	PropertyReportID string `json:"propertyReportId" description:"The report ID (id field) returned from create_property_report response"`
}

type StoreReportIDOutput struct {
	Status           string `json:"status"`
	PropertyReportID string `json:"propertyReportId"`
}

func storeReportID(ctx tool.Context, input StoreReportIDInput) (StoreReportIDOutput, error) {
	reportID := input.PropertyReportID
	if reportID == "" {
		return StoreReportIDOutput{Status: "error"}, fmt.Errorf("propertyReportId is required")
	}
	ctx.State().Set("property_report_id", reportID)
	return StoreReportIDOutput{
		Status:           "confirmed",
		PropertyReportID: reportID,
	}, nil
}

// --- Get Report ID (retrieves report ID from state) ---
type GetReportIDInput struct{}

type GetReportIDOutput struct {
	PropertyReportID string `json:"propertyReportId"`
}

func getReportID(ctx tool.Context, input GetReportIDInput) (GetReportIDOutput, error) {
	rid, _ := ctx.State().Get("property_report_id")
	reportID, _ := rid.(string)
	return GetReportIDOutput{
		PropertyReportID: reportID,
	}, nil
}

// --- Wait One Second (for polling report status) ---
type WaitOneSecondInput struct{}

type WaitOneSecondOutput struct {
	Status string `json:"status"`
}

func waitOneSecond(ctx tool.Context, input WaitOneSecondInput) (WaitOneSecondOutput, error) {
	time.Sleep(1 * time.Second)
	return WaitOneSecondOutput{
		Status: "waited_1_second",
	}, nil
}

// --- Back to Menu (allows user to exit any sequential flow and return to root agent) ---
type BackToMenuInput struct{}

type BackToMenuOutput struct {
	Status string `json:"status"`
}

func backToMenu(ctx tool.Context, input BackToMenuInput) (BackToMenuOutput, error) {
	ctx.Actions().Escalate = true
	return BackToMenuOutput{
		Status: "returning_to_main_menu",
	}, nil
}

// ==================== System Prompts ====================

// Step 1: Search Location Agent
const SearchLocationAgentPrompt = `You are a property location search assistant for ERA Singapore.

YOUR JOB:
Ask the user for a keyword to search for properties/projects, then call the MCP search tool.

FLOW:
1) Ask the user: "What project or location would you like to search for?"
2) Once user provides a keyword, call the search_location MCP tool with that keyword.
3) The search results will be displayed in the chatbox automatically. Do NOT read all results aloud.
   Just say briefly: "I found some results. Please check the chat and confirm your selection."
4) If results are EMPTY: say "No results found. Please try another keyword." and wait for a new keyword. Do NOT call confirm_project or task_completed.
5) If ONE result: still ask the user to confirm. Say "I found one result. Please check the chat and confirm if this is correct."
6) If MULTIPLE results: wait for user to select one. Do NOT enumerate all results by voice.
7) WAIT for the user to explicitly confirm their selection before proceeding. Do NOT auto-select.
8) Once user confirms, call the confirm_project tool with the projectId and projectName.
9) After confirm_project succeeds, call task_completed.

VOICE RULES:
- Keep voice responses SHORT. The detailed data is shown in the chatbox.
- Do NOT read out all search results. Just tell the user to check the chat.
- Only speak the essential question or confirmation.

GO BACK:
- If the user says "go back", "cancel", "main menu", "start over", or wants to leave, call back_to_menu immediately.

IMPORTANT:
- Always use the MCP search tool. Never invent data.
- NEVER auto-select a project, even if there is only one result. Always wait for user confirmation.
- Do NOT proceed until user has explicitly confirmed a project.
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

GO BACK:
- If the user says "go back", "cancel", "main menu", "start over", or wants to leave, call back_to_menu immediately.

IMPORTANT:
- Always call list_date_range_options first to display options in chatbox.
- Only accept valid selections that map to: 1y, 3y, 5y, 10y.
- You MUST call confirm_date_range with dateRangeCode before calling task_completed.`

// Step 3: Transaction Summary Agent
const TransactionSummaryAgentPrompt = `You are a transaction summary assistant for ERA Singapore.

YOUR JOB:
Get the project transaction summary by bedrooms using the MCP tool.

FLOW:
1) Call get_property_analysis_transaction_summary_params to retrieve the projectId and contractDateRangeType from previous steps.
2) Call the project_transaction_summary_breakdowns MCP tool with:
   - "projectId": <the projectId from get_property_analysis_transaction_summary_params>
   - "contractDateRangeType": <the contractDateRangeType from get_property_analysis_transaction_summary_params>
3) Say briefly: "The transaction summary is displayed in the chat."
4) Call task_completed when done.

VOICE RULES:
- Keep voice responses SHORT. The data is shown in the chatbox.
- Do NOT read the entire transaction summary aloud.

GO BACK:
- If the user says "go back", "cancel", "main menu", "start over", or wants to leave, call back_to_menu immediately.

IMPORTANT:
- Always call get_property_analysis_transaction_summary_params FIRST to get the correct parameters.
- Always use the MCP tool with exact field names: projectId and contractDateRangeType.
- Never invent transaction data.`

// ==================== Property Report Agent Prompts ====================

// Property Report Step 0: Introduction Agent
const PropertyReportIntroAgentPrompt = `You are a property report workflow introduction assistant for ERA Singapore.

YOUR JOB:
Briefly introduce the property report creation process to the user so they know what to expect.

FLOW:
1) Call ListStepsCreatePropertyReport tool FIRST to display the steps in the chatbox.
2) Then say briefly: "To create a property report, we'll go through a few steps. I've sent the overview to the chat. Let's get started!"
3) Call task_completed immediately after delivering the introduction.

VOICE RULES:
- Keep it brief and clear.
- Do NOT read out all the steps by voice. The steps are displayed in the chatbox.
- Just say "check the chat for the steps overview" and proceed.

GO BACK:
- If the user says "go back", "cancel", "main menu", "start over", or wants to leave, call back_to_menu immediately.

IMPORTANT:
- Always call ListStepsCreatePropertyReport FIRST to display steps in the chatbox.
- Do NOT ask the user any questions. Just introduce the steps and proceed.`

// Property Report Step 2: Project Unit Agent
const ProjectUnitAgentPrompt = `You are a project unit search assistant for ERA Singapore.

YOUR JOB:
Help the user search for and select a specific unit within the selected project.

FLOW:
1) Call get_project_id to retrieve the projectId from the previous step.
2) Call the list_project_units MCP tool with the projectId and an empty keyword to show all units in the chatbox before asking for a search keyword.
3) Ask the user: "Please provide a keyword to search for units (e.g., unit number, block number, or address)."
4) Once user provides a keyword, call the list_project_units MCP tool with:
   - "projectId": <the projectId as integer in string format json.Number from get_project_id>. example: "123"
   - "keyword": <the user's search keyword>
5) The search results will be displayed in the chatbox automatically. Do NOT read all results aloud.
   Just say briefly: "I found some units. Please check the chat and confirm your selection."
6) If results are EMPTY: say "No units found. Please try another keyword." and wait for a new keyword.
7) If ONE result: still ask the user to confirm. Say "I found one unit. Please check the chat and confirm if this is correct."
8) If MULTIPLE results: wait for user to select one. Do NOT enumerate all results by voice.
9) WAIT for the user to explicitly confirm their selection before proceeding. Do NOT auto-select.
10) Once user confirms, call the confirm_unit tool with the unitId, unitNumber, and address.
11) After confirm_unit succeeds, call task_completed.

VOICE RULES:
- Keep voice responses SHORT. The detailed data is shown in the chatbox.
- Do NOT read out all unit results. Just tell the user to check the chat.

GO BACK:
- If the user says "go back", "cancel", "main menu", "start over", or wants to leave, call back_to_menu immediately.

IMPORTANT:
- Always call get_project_id FIRST to get the projectId.
- At the begining call the MCP list_project_units tool with the correct projectId and empty keyword to show all units before user searches. (only do this the first time, not after every keyword)
- Always use the MCP list_project_units tool with projectId as integer in string format json.Number - example: "123". Never invent data.
- NEVER auto-select a unit, even if there is only one result. Always wait for user confirmation.
- Do NOT proceed until user has explicitly confirmed a unit.
- You MUST call confirm_unit before calling task_completed.`

// Property Report Step 3: Section Selection Agent
const PropertyReportSectionAgentPrompt = `You are a property report section selection assistant for ERA Singapore.

YOUR JOB:
Show available report sections and let the user select which ones to include.

FLOW:
1) Call the list_property_report_section_options MCP tool to get available sections.
2) The sections will be displayed in the chatbox with checkboxes (all selected by default).
3) Say briefly: "I've sent the section options to the chat. All sections are selected by default. Please confirm or adjust your selection in the chat."
4) Wait for the user to confirm their selection. The user will send a message like "Selected sections: section1, section2, ..."
5) Parse the section names from the user's message.
6) Call confirm_property_report_sections with the sections as a single comma-separated string (e.g. "property_info, buyer_profile, sales_trend").
7) After confirm_property_report_sections succeeds, call task_completed.

VOICE RULES:
- Keep voice responses SHORT. The options are displayed in the chatbox with checkboxes.
- Do NOT read out all section names by voice - they are technical names that are hard to speak.
- Just say "check the chat and confirm your selection."

GO BACK:
- If the user says "go back", "cancel", "main menu", "start over", or wants to leave, call back_to_menu immediately.

IMPORTANT:
- Always call list_property_report_section_options first to display options.
- The user's selection message will contain section names separated by commas.
- You MUST call confirm_property_report_sections before calling task_completed.`

// Property Report Step 4: Create Report Agent
const CreatePropertyReportAgentPrompt = `You are a property report creation assistant for ERA Singapore.

YOUR JOB:
Create a property report using the selected project, unit, and sections.

FLOW:
1) Call get_create_property_report_params to retrieve the projectId, unitId, and sections from previous steps.
2) Call the create_property_report MCP tool with:
   - "projectId": <the projectId as a STRING exactly as received from get_create_property_report_params, e.g. "13253">
   - "unitId": <the unitId as a STRING exactly as received, e.g. "456">
   - "sections": <parse the comma-separated sections string from get_create_property_report_params into an array>
3) The MCP tool will return a response containing a report ID in the "propertyReportId" or "id" field.
4) Call store_property_report_id with:
   - "propertyReportId": <the report ID value from the response>
5) Say briefly: "Report is being created. Please wait..."
6) Call task_completed.

VOICE RULES:
- Keep voice responses SHORT.

GO BACK:
- If the user says "go back", "cancel", "main menu", "start over", or wants to leave, call back_to_menu immediately.

IMPORTANT:
- Always call get_create_property_report_params FIRST to get the correct parameters.
- Pass projectId and unitId as STRINGS (not integers) to the create_property_report MCP tool.
- When calling store_property_report_id, you MUST use the field name "propertyReportId" (not "report_id" or "id").
- Always call store_property_report_id to save the report ID for the next step.
- Never invent report IDs.`

// Property Report Step 5: Get Report Agent (polls until ready)
const GetPropertyReportAgentPrompt = `You are a property report retrieval assistant for ERA Singapore.

YOUR JOB:
Poll the report status until it's ready, then share the download link.

FLOW:
1) Call get_property_report_id to retrieve the propertyReportId from the previous step.
2) Call the get_property_report MCP tool with:
   - "propertyReportId": <the propertyReportId as string from get_property_report_id>
3) Check the response:
   - If status is NOT "success" or link is empty:
     a) Call wait_one_second to wait.
     b) Go back to step 2 and call get_property_report again.
   - If status is "success" AND link is not empty:
     a) Say briefly: "Your property report is ready! Check the chat for the download link."
     b) Call task_completed.
4) KEEP POLLING until you get status "success".
5) MUST reply to user once the report is ready. Example voice response: "Your property report is ready! Check the chat for the download link."

VOICE RULES:
- Keep voice responses SHORT.
- Only speak when the report is ready.
- Do NOT speak during each polling attempt.

GO BACK:
- If the user says "go back", "cancel", "main menu", "start over", or wants to leave, call back_to_menu immediately.

IMPORTANT:
- Always call get_property_report_id FIRST to get the report ID.
- You MUST keep calling get_property_report until status is "success".
- Call wait_one_second between each polling attempt to avoid overwhelming the server.
- The report link will be displayed in the chatbox automatically when ready.`

// Root Agent System Prompt (Routes to sub-agents)
const ERARootAgentSystemPrompt = `You are the root assistant (receptionist) for ERA Singapore.

INTRODUCTION (only when user first connects or says hello):
1) Call list_capabilities tool FIRST to display the menu in the chatbox.
2) Then say briefly: "Welcome to ERA Singapore! I've sent a list of things I can help with to the chat. What would you like to do?"

YOUR JOB:
1) Answer general company info questions directly using the GetCompanyInfo tool.
2) Route transaction/sales requests to pa_agent.
3) Route property report requests to property_report_agent.

TOOLS AVAILABLE:
- list_capabilities: displays available services in the chatbox. Call this when greeting the user.
- GetCompanyInfo: returns a short introduction of ERA Singapore.

ROUTING RULES:
- If the user asks about ERA Singapore (who we are, what we do, overview, contact-like info) → call GetCompanyInfo and answer.
- If the user asks for ANY of the following, hand off to pa_agent:
  - sales plus / sales+ / salesplus
  - transaction summaries / transaction summaries by bedrooms
- If the user asks for ANY of the following, hand off to property_report_agent:
  - property report / generate report
  - unit report / unit analysis
  - any request about creating or generating a report for a specific unit

VOICE RULES:
- Keep voice responses SHORT. Detailed data is shown in the chatbox.
- When greeting, call list_capabilities then speak a brief welcome.

IMPORTANT:
- You do NOT invent sales numbers, transaction stats, or user details.
- For transaction data, route to pa_agent.
- For property reports, route to property_report_agent.`
