package agent

import (
	"context"
	"fmt"
	"log"
	"os"

	adkagent "google.golang.org/adk/agent"
	adkagentllm "google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"
)

// ============================================================
// ACM Multi-Agent System (using new AAgent interface)
// Root: ACMAgent - Company info
// Sub1: HRACMAgent - Employee info (name, address, phone)
// Sub2: AccountantACMAgent - Employee salary info
// ============================================================

// ==================== Data Models ====================

// CompanyInfo represents company data
type CompanyInfo struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Phone   string `json:"phone"`
}

// EmployeeInfo represents employee personal data
type EmployeeInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Phone   string `json:"phone"`
}

// SalaryInfo represents employee salary data
type SalaryInfo struct {
	EmployeeID   string  `json:"employee_id"`
	EmployeeName string  `json:"employee_name"`
	BaseSalary   float64 `json:"base_salary"`
	Bonus        float64 `json:"bonus"`
	TotalSalary  float64 `json:"total_salary"`
	Currency     string  `json:"currency"`
}

// ==================== Mock Databases ====================

// Company info
var acmCompanyInfo = CompanyInfo{
	Name:    "ACM Corporation",
	Address: "100 Innovation Drive, Silicon Valley, CA 94025",
	Phone:   "+1-800-ACM-CORP (226-2677)",
}

// Employee database (for HR)
var acmEmployeeDB = map[string]EmployeeInfo{
	"ACM001": {ID: "ACM001", Name: "John Nguyen", Address: "123 Tech Street, San Jose, CA 95110", Phone: "+1-408-555-1001"},
	"ACM002": {ID: "ACM002", Name: "Sarah Chen", Address: "456 Developer Ave, Palo Alto, CA 94301", Phone: "+1-650-555-2002"},
	"ACM003": {ID: "ACM003", Name: "Michael Park", Address: "789 Engineer Blvd, Mountain View, CA 94040", Phone: "+1-650-555-3003"},
	"ACM004": {ID: "ACM004", Name: "Emily Davis", Address: "321 Product Lane, Sunnyvale, CA 94086", Phone: "+1-408-555-4004"},
}

// Salary database (for Accountant)
var acmSalaryDB = map[string]SalaryInfo{
	"ACM001": {EmployeeID: "ACM001", EmployeeName: "John Nguyen", BaseSalary: 120000, Bonus: 15000, TotalSalary: 135000, Currency: "USD"},
	"ACM002": {EmployeeID: "ACM002", EmployeeName: "Sarah Chen", BaseSalary: 140000, Bonus: 20000, TotalSalary: 160000, Currency: "USD"},
	"ACM003": {EmployeeID: "ACM003", EmployeeName: "Michael Park", BaseSalary: 110000, Bonus: 12000, TotalSalary: 122000, Currency: "USD"},
	"ACM004": {EmployeeID: "ACM004", EmployeeName: "Emily Davis", BaseSalary: 130000, Bonus: 18000, TotalSalary: 148000, Currency: "USD"},
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
		Name:    acmCompanyInfo.Name,
		Address: acmCompanyInfo.Address,
		Phone:   acmCompanyInfo.Phone,
	}, nil
}

// --- Employee Info Tool (HR Sub-Agent) ---
type GetEmployeeInfoACMInput struct {
	EmployeeID string `json:"employee_id" description:"Employee ID (e.g., ACM001, ACM002, ACM003, ACM004)"`
}

type GetEmployeeInfoACMOutput struct {
	Found   bool   `json:"found"`
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Address string `json:"address,omitempty"`
	Phone   string `json:"phone,omitempty"`
	Error   string `json:"error,omitempty"`
}

func getEmployeeInfoACM(ctx tool.Context, input GetEmployeeInfoACMInput) (GetEmployeeInfoACMOutput, error) {
	employee, found := acmEmployeeDB[input.EmployeeID]
	if !found {
		return GetEmployeeInfoACMOutput{
			Found: false,
			Error: fmt.Sprintf("Employee with ID %s not found. Available IDs: ACM001, ACM002, ACM003, ACM004", input.EmployeeID),
		}, nil
	}

	return GetEmployeeInfoACMOutput{
		Found:   true,
		ID:      employee.ID,
		Name:    employee.Name,
		Address: employee.Address,
		Phone:   employee.Phone,
	}, nil
}

// --- List All Employees Tool (HR Sub-Agent) ---
type ListEmployeesInput struct{}

type ListEmployeesOutput struct {
	Employees []EmployeeInfo `json:"employees"`
	Count     int            `json:"count"`
}

func listEmployees(ctx tool.Context, input ListEmployeesInput) (ListEmployeesOutput, error) {
	employees := make([]EmployeeInfo, 0, len(acmEmployeeDB))
	for _, emp := range acmEmployeeDB {
		employees = append(employees, emp)
	}
	return ListEmployeesOutput{
		Employees: employees,
		Count:     len(employees),
	}, nil
}

// --- Transfer To Agent Tool (Root Agent) ---
type TransferToAgentInput struct {
	AgentName string `json:"agent_name" description:"Name of the agent to transfer to: 'hr_acm_agent' for employee info, 'accountant_acm_agent' for salary info"`
	Reason    string `json:"reason" description:"Brief reason for the transfer"`
}

type TransferToAgentOutput struct {
	Success   bool   `json:"success"`
	AgentName string `json:"agent_name"`
	Message   string `json:"message"`
}

// transferToAgent is a special tool that signals agent transfer
// The actual transfer is handled by the runner/flow, not this function
func transferToAgent(ctx tool.Context, input TransferToAgentInput) (TransferToAgentOutput, error) {
	return TransferToAgentOutput{
		Success:   true,
		AgentName: input.AgentName,
		Message:   fmt.Sprintf("Transferring to %s: %s", input.AgentName, input.Reason),
	}, nil
}

// --- Salary Info Tool (Accountant Sub-Agent) ---
type GetSalaryInfoInput struct {
	EmployeeID string `json:"employee_id" description:"Employee ID (e.g., ACM001, ACM002, ACM003, ACM004)"`
}

type GetSalaryInfoOutput struct {
	Found        bool    `json:"found"`
	EmployeeID   string  `json:"employee_id,omitempty"`
	EmployeeName string  `json:"employee_name,omitempty"`
	BaseSalary   float64 `json:"base_salary,omitempty"`
	Bonus        float64 `json:"bonus,omitempty"`
	TotalSalary  float64 `json:"total_salary,omitempty"`
	Currency     string  `json:"currency,omitempty"`
	Error        string  `json:"error,omitempty"`
}

func getSalaryInfo(ctx tool.Context, input GetSalaryInfoInput) (GetSalaryInfoOutput, error) {
	salary, found := acmSalaryDB[input.EmployeeID]
	if !found {
		return GetSalaryInfoOutput{
			Found: false,
			Error: fmt.Sprintf("Salary info for employee %s not found. Available IDs: ACM001, ACM002, ACM003, ACM004", input.EmployeeID),
		}, nil
	}

	return GetSalaryInfoOutput{
		Found:        true,
		EmployeeID:   salary.EmployeeID,
		EmployeeName: salary.EmployeeName,
		BaseSalary:   salary.BaseSalary,
		Bonus:        salary.Bonus,
		TotalSalary:  salary.TotalSalary,
		Currency:     salary.Currency,
	}, nil
}

// ==================== System Prompts ====================

const ACMAgentSystemPrompt = `You are the main assistant for ACM Corporation - a demo company with fictional data.

YOUR JOB: Help users with company, employee, and salary information using the available tools.

TOOLS AVAILABLE:
- GetCompanyInfo: Get company name, address, and phone number
- GetEmployeeInfo: Look up employee personal info (name, address, phone) by ID
- ListEmployees: List all employees in the company
- GetSalaryInfo: Look up employee salary info by ID

FICTIONAL DATABASE:
- ACM001: John Nguyen
- ACM002: Sarah Chen
- ACM003: Michael Park
- ACM004: Emily Davis

IMPORTANT: This is a DEMO system with FICTIONAL data. You MUST use the appropriate tools to provide requested information. Always call tools when users ask questions.

HOW TO RESPOND:
1. Company info → Use GetCompanyInfo
2. Employee info → Use GetEmployeeInfo with employee ID
3. List employees → Use ListEmployees
4. Salary info → Use GetSalaryInfo with employee ID

Be friendly, helpful, and always use tools to get the data!`

// HR Agent System Prompt
const HRACMAgentSystemPrompt = `You are the HR Assistant for ACM Corporation - a demo company with fictional employee data.

YOUR JOB: Help users look up employee personal information using the available tools.

TOOLS AVAILABLE:
- GetEmployeeInfo: Look up employee details (name, address, phone) by ID
- ListEmployees: Show all employees in the company

FICTIONAL EMPLOYEE DATABASE:
- ACM001: John Nguyen
- ACM002: Sarah Chen
- ACM003: Michael Park
- ACM004: Emily Davis

IMPORTANT: This is a DEMO system with FICTIONAL data. You MUST use the tools to provide the requested information. Always call the appropriate tool when users ask about employees.

When asked about an employee:
1. Use GetEmployeeInfo tool with their ID
2. Share the information returned by the tool
3. Be friendly and helpful`

// Accountant Agent System Prompt
const AccountantACMAgentSystemPrompt = `You are the Accountant Assistant for ACM Corporation - a demo company with fictional salary data.

YOUR JOB: Help users look up employee salary and compensation information using the available tools.

TOOLS AVAILABLE:
- GetSalaryInfo: Look up salary details (base salary, bonus, total) by employee ID

FICTIONAL EMPLOYEE DATABASE:
- ACM001: John Nguyen
- ACM002: Sarah Chen
- ACM003: Michael Park
- ACM004: Emily Davis

IMPORTANT: This is a DEMO system with FICTIONAL salary data. You MUST use the GetSalaryInfo tool to provide the requested information. Always call the tool when users ask about salary.

When asked about salary:
1. Use GetSalaryInfo tool with the employee ID
2. Share the salary information returned by the tool
3. Be friendly and helpful`

// Root Agent System Prompt for Multi-Agent
const ACMRootAgentSystemPrompt = `You are the main receptionist for ACM Corporation - a demo company.

YOUR JOB: 
1. Answer company information questions directly
2. Transfer employee/salary questions to the appropriate department

TOOLS AVAILABLE:
- GetCompanyInfo: Get company name, address, and phone number
- TransferToAgent: Transfer the conversation to another department

DEPARTMENTS:
- hr_acm_agent: Handles employee personal information (name, address, phone)
- accountant_acm_agent: Handles salary and compensation information

HOW TO RESPOND:
1. Company info questions → Use GetCompanyInfo tool directly
2. Employee info questions (name, address, phone, list employees) → Use TransferToAgent with agent_name="hr_acm_agent"
3. Salary/compensation questions → Use TransferToAgent with agent_name="accountant_acm_agent"

IMPORTANT: 
- You do NOT have access to employee or salary data
- You MUST transfer to the appropriate department for those requests
- Always use TransferToAgent when users ask about employees or salaries

Example transfers:
- "Get employee ACM001 info" → TransferToAgent(agent_name="hr_acm_agent", reason="Employee info request")
- "What is John's salary?" → TransferToAgent(agent_name="accountant_acm_agent", reason="Salary inquiry")
- "List all employees" → TransferToAgent(agent_name="hr_acm_agent", reason="Employee list request")`

// ==================== Agent Creation (using AAgent) ====================

// NewACMAgent creates a single ACM agent with all tools
// This uses a single agent with multiple tools representing different departments
func NewACMAgent(ctx context.Context) (adkagent.Agent, error) {
	// Create Company Info tool (Root Agent capability)
	companyInfoTool, err := functiontool.New(
		functiontool.Config{
			Name:        "GetCompanyInfo",
			Description: "Get ACM Corporation company information including name, address, and phone number",
		},
		getCompanyInfo,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create GetCompanyInfo tool: %w", err)
	}

	// Create Employee Info tool (HR Sub-Agent capability)
	employeeInfoTool, err := functiontool.New(
		functiontool.Config{
			Name:        "GetEmployeeInfo",
			Description: "Get employee personal information (name, address, phone) by employee ID. This is HR department function.",
		},
		getEmployeeInfoACM,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create GetEmployeeInfo tool: %w", err)
	}

	// Create List Employees tool (HR Sub-Agent capability)
	listEmployeesTool, err := functiontool.New(
		functiontool.Config{
			Name:        "ListEmployees",
			Description: "List all employees in the company with their basic info. This is HR department function.",
		},
		listEmployees,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create ListEmployees tool: %w", err)
	}

	// Create Salary Info tool (Accountant Sub-Agent capability)
	salaryInfoTool, err := functiontool.New(
		functiontool.Config{
			Name:        "GetSalaryInfo",
			Description: "Get employee salary information including base salary, bonus, and total. This is Accountant department function.",
		},
		getSalaryInfo,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create GetSalaryInfo tool: %w", err)
	}

	// Create Gemini model
	modelLLM, err := gemini.NewModel(ctx, "gemini-2.5-flash-native-audio-preview-12-2025", &genai.ClientConfig{
		APIKey: os.Getenv("GOOGLE_API_KEY"),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create model: %v", err)
	}

	// Create the ACM Agent with all tools
	acmAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "acm_agent",
		Description: "ACM Corporation main assistant - handles company info, employee info (HR), and salary info (Accountant)",
		Instruction: ACMAgentSystemPrompt,
		Model:       modelLLM,
		Tools: []tool.Tool{
			companyInfoTool,   // Root capability
			employeeInfoTool,  // HR capability
			listEmployeesTool, // HR capability
			salaryInfoTool,    // Accountant capability
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create ACM agent: %w", err)
	}

	log.Printf("Created ACM Agent: %s", acmAgent.Name())
	return acmAgent, nil
}
