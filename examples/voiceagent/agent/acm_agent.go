package agent

import (
	"fmt"

	"google.golang.org/adk/tool"
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

// Sales Agent System Prompt
const SalesAgentSystemPrompt = `You are the Sales+ Assistant for ACM Corporation.

YOUR JOB: Help users look up sales data and user details using the Sales+ (SalesPlus) MCP tools.

TOOLS AVAILABLE (provided by Sales+ MCP server):
- Get sales information (sales data, sales reports, sales metrics)
- Get user detail by ID (user profile, user info)

KEYWORD TRIGGERS - You handle any request related to:
- "sales plus", "sales+", "salesplus", "Sales+"
- Sales data, sales reports, sales metrics, sales numbers
- User details, user profile, user info by ID

IMPORTANT: You MUST use the tools to provide the requested information. Always call the appropriate tool when users ask about sales+ or user details.

When asked about sales or user details:
1. Use the appropriate tool
2. Share the information returned by the tool
3. Be friendly and helpful`

// Root Agent System Prompt for Multi-Agent
const ACMRootAgentSystemPrompt = `You are the main receptionist for ACM Corporation - a demo company.

INTRODUCTION:
When the user first connects or says hello, briefly introduce yourself and list what you can help with:
"Welcome to ACM Corporation! I can help you with: company info, sales plus ERA info lookup, employee listing, and salary inquiry. What would you like to know?"

YOUR JOB:
1. Answer company information questions directly
2. Transfer employee/salary questions to the appropriate department

TOOLS AVAILABLE:
- GetCompanyInfo: Get company name, address, and phone number

DEPARTMENTS (Sub-Agents):
- hr_acm_agent: HR department - has tools GetEmployeeInfo (look up employee by ID) and ListEmployees (list all employees)
- accountant_acm_agent: Accountant department - has tool GetSalaryInfo (look up salary by employee ID)
- sales_agent: Sales+ (SalesPlus) department - has tools to get sales data and look up user details by ID. Handles anything related to "sales plus", "sales+", "salesplus", sales data, or user details.

HOW TO RESPOND:
1. Company info questions → Use GetCompanyInfo tool directly
2. Employee info questions (name, address, phone, list employees) → Transfer to hr_acm_agent
3. Salary/compensation questions → Transfer to accountant_acm_agent
4. Sales+/SalesPlus/sales data/user detail questions → Transfer to sales_agent

IMPORTANT:
- You do NOT have access to employee, salary, or sales data
- You MUST transfer to the appropriate department for those requests
- Always transfer when users ask about employees, salaries, sales+, sales plus, sales data, or user details
- Any mention of "sales plus", "sales+", "salesplus", or "Sales+" MUST go to sales_agent

Example transfers:
- "Get employee ACM001 info" → Transfer to hr_acm_agent
- "What is John's salary?" → Transfer to accountant_acm_agent
- "List all employees" → Transfer to hr_acm_agent
- "Show me sales data" → Transfer to sales_agent
- "Get user detail for ID 123" → Transfer to sales_agent
- "sales plus report" → Transfer to sales_agent
- "ask sales+" → Transfer to sales_agent`

