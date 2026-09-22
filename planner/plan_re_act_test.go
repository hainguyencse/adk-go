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
	"strings"
	"testing"

	"google.golang.org/genai"
)

func TestPlanReActPlannerBuildPlanningInstruction(t *testing.T) {
	t.Parallel()

	got := NewPlanReActPlanner().BuildPlanningInstruction(t.Context(), nil, nil)
	for _, want := range []string{PlanningTag, ReplanningTag, ReasoningTag, ActionTag, FinalAnswerTag} {
		if !strings.Contains(got, want) {
			t.Errorf("planning instruction does not contain %q", want)
		}
	}
}

func TestPlanReActPlannerStripsPlanningTags(t *testing.T) {
	t.Parallel()

	first := &genai.Part{Text: PlanningTag + "Step 1: look it up.", ThoughtSignature: []byte("sig1")}
	second := &genai.Part{Text: ReasoningTag + "I need to call the tool.", ThoughtSignature: []byte("sig2")}
	call := genai.NewPartFromFunctionCall("lookup", map[string]any{"q": "test"})

	got := NewPlanReActPlanner().ProcessPlanningResponse(t.Context(), nil, []*genai.Part{first, second, call})

	if len(got) != 3 {
		t.Fatalf("ProcessPlanningResponse() returned %d parts, want 3", len(got))
	}
	for i, part := range got[:2] {
		if strings.Contains(part.Text, "/*") {
			t.Errorf("part %d still contains a planning tag: %q", i, part.Text)
		}
		if !part.Thought {
			t.Errorf("part %d is not marked as thought", i)
		}
	}
	if string(got[0].ThoughtSignature) != "sig1" || string(got[1].ThoughtSignature) != "sig2" {
		t.Error("ProcessPlanningResponse() did not preserve part metadata")
	}
	if got[2].FunctionCall == nil || got[2].FunctionCall.Name != "lookup" {
		t.Errorf("function call = %v, want lookup", got[2].FunctionCall)
	}
}

func TestPlanReActPlannerSplitsFinalAnswer(t *testing.T) {
	t.Parallel()

	input := PlanningTag + "Initial plan.\n" + ReasoningTag + "Some reasoning.\n" + FinalAnswerTag + "The answer is 42."
	got := NewPlanReActPlanner().ProcessPlanningResponse(t.Context(), nil, []*genai.Part{{Text: input}})

	if len(got) != 2 {
		t.Fatalf("ProcessPlanningResponse() returned %d parts, want 2", len(got))
	}
	if got[0].Text != "Initial plan.\nSome reasoning.\n" || !got[0].Thought {
		t.Errorf("reasoning part = %#v, want stripped thought", got[0])
	}
	if got[1].Text != "The answer is 42." || got[1].Thought {
		t.Errorf("final answer part = %#v, want non-thought final answer", got[1])
	}
}

func TestPlanReActPlannerDoesNotMarkEmbeddedTagAsThought(t *testing.T) {
	t.Parallel()

	part := &genai.Part{Text: "Answer with a stray " + PlanningTag + " tag."}
	got := NewPlanReActPlanner().ProcessPlanningResponse(t.Context(), nil, []*genai.Part{part})

	if len(got) != 1 || got[0] != part {
		t.Fatalf("ProcessPlanningResponse() did not preserve the part")
	}
	if got[0].Thought {
		t.Error("part with a non-leading tag was marked as thought")
	}
}

func TestPlanReActPlannerPreservesBareTag(t *testing.T) {
	t.Parallel()

	part := &genai.Part{Text: ActionTag}
	got := NewPlanReActPlanner().ProcessPlanningResponse(t.Context(), nil, []*genai.Part{part})

	if len(got) != 1 || got[0].Text != "" || !got[0].Thought {
		t.Errorf("bare tag result = %#v, want an empty thought part", got)
	}
}

func TestPlanReActPlannerPreservesConsecutiveFunctionCalls(t *testing.T) {
	t.Parallel()

	first := genai.NewPartFromFunctionCall("get_weather", map[string]any{"city": "SF"})
	second := genai.NewPartFromFunctionCall("get_time", map[string]any{"city": "SF"})
	trailingText := &genai.Part{Text: "must not be included with the calls"}
	lateCall := genai.NewPartFromFunctionCall("late_call", nil)
	got := NewPlanReActPlanner().ProcessPlanningResponse(t.Context(), nil, []*genai.Part{
		{Text: ReasoningTag + "I will look it up."},
		first,
		second,
		trailingText,
		lateCall,
	})

	if len(got) != 3 {
		t.Fatalf("ProcessPlanningResponse() returned %d parts, want reasoning plus two calls", len(got))
	}
	if got[1] != first || got[2] != second {
		t.Errorf("function-call group = %v, want get_weather and get_time", got[1:])
	}
}

func TestPlanReActPlannerPreservesParallelCallsAtStart(t *testing.T) {
	t.Parallel()

	first := genai.NewPartFromFunctionCall("get_weather", nil)
	second := genai.NewPartFromFunctionCall("get_time", nil)
	got := NewPlanReActPlanner().ProcessPlanningResponse(t.Context(), nil, []*genai.Part{first, second})

	if len(got) != 2 || got[0] != first || got[1] != second {
		t.Errorf("ProcessPlanningResponse() returned %v, want both parallel calls", got)
	}
}
