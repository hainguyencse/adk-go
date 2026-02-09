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
// Multi-Agent System
// Root: ACMRootAgent - Company info and routing
// Sub1: HRACMAgent - Employee info (name, address, phone)
// Sub2: SalaryACMAgent - Employee salary info
// ============================================================

// ==================== Data Models ====================
// (Reusing from acm_agent.go)

// ==================== Tool Handlers ====================
// (Reusing from acm_agent.go)

// ==================== System Prompts ====================
// (Reusing constants from acm_agent.go)

// ==================== Agent Creation ====================

// NewMultiAgentSystem creates a multi-agent system with:
// - Root agent: Handles company info and routes to sub-agents
// - HR sub-agent: Handles employee info
// - Salary sub-agent: Handles salary info
func NewMultiAgentSystem(ctx context.Context) (adkagent.Agent, error) {
	// Create Gemini model (shared across all agents)
	modelLLM, err := gemini.NewModel(ctx, "gemini-2.5-flash-native-audio-preview-12-2025", &genai.ClientConfig{
		APIKey: os.Getenv("GOOGLE_API_KEY"),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create model: %w", err)
	}

	// ========== Create HR Sub-Agent ==========
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

	hrAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "hr_acm_agent",
		Description: "HR department - handles employee personal information (name, address, phone)",
		Instruction: HRACMAgentSystemPrompt,
		Model:       modelLLM,
		Tools: []tool.Tool{
			employeeInfoTool,
			listEmployeesTool,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create HR agent: %w", err)
	}

	// ========== Create Salary Sub-Agent ==========
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

	salaryAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "accountant_acm_agent",
		Description: "Accountant department - handles employee salary and compensation information",
		Instruction: AccountantACMAgentSystemPrompt,
		Model:       modelLLM,
		Tools: []tool.Tool{
			salaryInfoTool,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Salary agent: %w", err)
	}

	// ========== Create Root Agent ==========
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

	rootAgent, err := adkagentllm.New(adkagentllm.Config{
		Name:        "acm_root_agent",
		Description: "Main receptionist for ACM Corporation - handles company info and routes to HR or Accountant departments",
		Instruction: ACMRootAgentSystemPrompt,
		Model:       modelLLM,
		Tools: []tool.Tool{
			companyInfoTool,
		},
		SubAgents: []adkagent.Agent{
			hrAgent,
			salaryAgent,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create root agent: %w", err)
	}

	log.Printf("Created Multi-Agent System:")
	log.Printf("  Root Agent: %s", rootAgent.Name())
	log.Printf("  Sub-Agent 1: %s (HR)", hrAgent.Name())
	log.Printf("  Sub-Agent 2: %s (Salary)", salaryAgent.Name())

	return rootAgent, nil
}
