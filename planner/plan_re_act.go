// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package planner

import (
	"context"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/model"
)

const (
	PlanningTag    = "/*PLANNING*/"
	ReplanningTag  = "/*REPLANNING*/"
	ReasoningTag   = "/*REASONING*/"
	ActionTag      = "/*ACTION*/"
	FinalAnswerTag = "/*FINAL_ANSWER*/"
)

const (
	HighLevelPreamble = `
When answering the question, try to leverage the available tools to gather the information instead of your memorized knowledge.

Follow this process when answering the question: (1) first come up with a plan in natural language text format; (2) Then use tools to execute the plan and provide reasoning between tool code snippets to make a summary of current state and next step. Tool code snippets and reasoning should be interleaved with each other. (3) In the end, return one final answer.

Follow this format when answering the question: (1) The planning part should be under ` + PlanningTag + `. (2) The tool code snippets should be under ` + ActionTag + `, and the reasoning parts should be under ` + ReasoningTag + `. (3) The final answer part should be under ` + FinalAnswerTag + `.
`

	PlanningPreamble = `
Below are the requirements for the planning:
The plan is made to answer the user query if following the plan. The plan is coherent and covers all aspects of information from user query, and only involves the tools that are accessible by the agent. The plan contains the decomposed steps as a numbered list where each step should use one or multiple available tools. By reading the plan, you can intuitively know which tools to trigger or what actions to take.
If the initial plan cannot be successfully executed, you should learn from previous execution results and revise your plan. The revised plan should be under ` + ReplanningTag + `. Then use tools to follow the new plan.
`

	// PlanningRreamble preserves the misspelled name exposed by the reference
	// implementation. New code should use PlanningPreamble.
	// Deprecated: use PlanningPreamble.
	PlanningRreamble = PlanningPreamble

	ReasoningPreamble = `
Below are the requirements for the reasoning:
The reasoning makes a summary of the current trajectory based on the user query and tool outputs. Based on the tool outputs and plan, the reasoning also comes up with instructions to the next steps, making the trajectory closer to the final answer.
`

	FinalAnswerPreamble = `
Below are the requirements for the final answer:
The final answer should be precise and follow query formatting requirements. Some queries may not be answerable with the available tools and information. In those cases, inform the user why you cannot process their query and ask for more information.
`

	// ToolCodeWithoutPythonLibrariesPreamble contains the requirements for
	// custom tools and libraries.
	ToolCodeWithoutPythonLibrariesPreamble = `
Below are the requirements for the tool code:

**Custom Tools:** The available tools are described in the context and can be directly used.
- Code must be valid self-contained Python snippets with no imports and no references to tools or Python libraries that are not in the context.
- You cannot use any parameters or fields that are not explicitly defined in the APIs in the context.
- The code snippets should be readable, efficient, and directly relevant to the user query and reasoning steps.
- When using the tools, you should use the library name together with the function name, e.g., vertex_search.search().
- If Python libraries are not provided in the context, NEVER write your own code other than the function calls using the provided tools.
`

	UserInputPreamble = `
VERY IMPORTANT instruction that you MUST follow in addition to the above instructions:

You should ask for clarification if you need more information to answer the question.
You should prefer using the information available in the context instead of repeated tool use.
`
)

var planningTags = []string{PlanningTag, ReasoningTag, ActionTag, ReplanningTag}

// PlanReActPlanner constrains model responses to plan, reason, act, and then
// provide a final answer. It does not require model-native thinking support.
type PlanReActPlanner struct{}

var _ Planner = (*PlanReActPlanner)(nil)

// NewPlanReActPlanner returns a Plan-Re-Act planner.
func NewPlanReActPlanner() *PlanReActPlanner {
	return &PlanReActPlanner{}
}

// BuildPlanningInstruction implements Planner.
func (*PlanReActPlanner) BuildPlanningInstruction(context.Context, agent.ReadonlyContext, *model.LLMRequest) string {
	return strings.Join([]string{
		HighLevelPreamble,
		PlanningPreamble,
		ReasoningPreamble,
		FinalAnswerPreamble,
		ToolCodeWithoutPythonLibrariesPreamble,
		UserInputPreamble,
	}, "\n\n")
}

// ProcessPlanningResponse implements Planner.
func (*PlanReActPlanner) ProcessPlanningResponse(_ context.Context, _ agent.CallbackContext, responseParts []*genai.Part) []*genai.Part {
	if len(responseParts) == 0 {
		return nil
	}

	preservedParts := make([]*genai.Part, 0, len(responseParts))
	firstFunctionCallIndex := -1
	for i, part := range responseParts {
		if part == nil {
			preservedParts = append(preservedParts, nil)
			continue
		}
		if part.FunctionCall != nil {
			// Ignore malformed calls while looking for the first valid group.
			if part.FunctionCall.Name == "" {
				continue
			}
			preservedParts = append(preservedParts, part)
			firstFunctionCallIndex = i
			break
		}

		preservedParts = appendNonFunctionCallParts(preservedParts, part)
	}

	// Preserve only the consecutive group of function calls starting at the
	// first valid call. Text after that group belongs to a later model turn.
	if firstFunctionCallIndex >= 0 {
		for _, part := range responseParts[firstFunctionCallIndex+1:] {
			if part == nil || part.FunctionCall == nil {
				break
			}
			preservedParts = append(preservedParts, part)
		}
	}

	return preservedParts
}

func appendNonFunctionCallParts(parts []*genai.Part, responsePart *genai.Part) []*genai.Part {
	if responsePart.Text != "" && strings.Contains(responsePart.Text, FinalAnswerTag) {
		reasoningText, finalAnswerText := splitByLastPattern(responsePart.Text, FinalAnswerTag)
		reasoningText = stripPlanningTags(reasoningText)
		if reasoningText != "" {
			reasoningPart := genai.NewPartFromText(reasoningText)
			reasoningPart.Thought = true
			parts = append(parts, reasoningPart)
		}
		if finalAnswerText != "" {
			parts = append(parts, genai.NewPartFromText(finalAnswerText))
		}
		return parts
	}

	if responsePart.Text != "" && hasPlanningPrefix(responsePart.Text) {
		responsePart.Text = stripPlanningTags(responsePart.Text)
		responsePart.Thought = true
	}
	return append(parts, responsePart)
}

func splitByLastPattern(text, separator string) (before, after string) {
	index := strings.LastIndex(text, separator)
	if index < 0 {
		return text, ""
	}
	return text[:index], text[index+len(separator):]
}

func stripPlanningTags(text string) string {
	for _, tag := range planningTags {
		text = strings.ReplaceAll(text, tag, "")
	}
	return text
}

func hasPlanningPrefix(text string) bool {
	for _, tag := range planningTags {
		if strings.HasPrefix(text, tag) {
			return true
		}
	}
	return false
}
