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

package llminternal

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	icontext "google.golang.org/adk/internal/context"
	"google.golang.org/adk/model"
	"google.golang.org/adk/platform"
	"google.golang.org/adk/tool"
)

type taskIndexKey struct{}

func TestHandleFunctionCallsUsesTaskRunnerContextAndPreservesOrder(t *testing.T) {
	var executionOrder []int
	runnerUsed := false
	parent := platform.WithTaskRunner(t.Context(), func(ctx context.Context, tasks []func(context.Context)) {
		runnerUsed = true
		// Complete in reverse order to distinguish execution order from response order.
		for i := len(tasks) - 1; i >= 0; i-- {
			tasks[i](context.WithValue(ctx, taskIndexKey{}, i))
		}
	})
	ctx := icontext.NewInvocationContext(parent, icontext.InvocationContextParams{
		Agent:        &mockAgent{name: "tester"},
		InvocationID: "invocation",
		Branch:       "branch",
		RunConfig:    &agent.RunConfig{},
	})
	var parts []*genai.Part
	tools := map[string]tool.Tool{}
	for i := range 3 {
		name := fmt.Sprintf("tool_%d", i)
		parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: name, Name: name}})
		tools[name] = &mockFunctionTool{
			name:          name,
			isLongRunning: i == 1,
			runFunc: func(ctx tool.Context, _ map[string]any) (map[string]any, error) {
				if got := ctx.Value(taskIndexKey{}); got != i {
					t.Errorf("tool %d context value = %v, want %d", i, got, i)
				}
				executionOrder = append(executionOrder, i)
				if i == 1 {
					return nil, nil // Deferred long-running response must leave no placeholder.
				}
				ctx.Actions().StateDelta["winner"] = i
				ctx.Actions().ArtifactDelta = map[string]int64{name: int64(i)}
				return map[string]any{"index": i}, nil
			},
		}
	}
	ev, err := (&Flow{}).handleFunctionCalls(ctx, tools, &model.LLMResponse{Content: &genai.Content{Parts: parts}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !runnerUsed {
		t.Fatal("installed task runner was not used")
	}
	if diff := cmp.Diff([]int{2, 1, 0}, executionOrder); diff != "" {
		t.Fatalf("execution order (-want +got): %s", diff)
	}
	if ev == nil || ev.Content == nil {
		t.Fatal("missing merged response")
	}
	var ids []string
	for _, part := range ev.Content.Parts {
		ids = append(ids, part.FunctionResponse.ID)
	}
	if diff := cmp.Diff([]string{"tool_0", "tool_2"}, ids); diff != "" {
		t.Errorf("response order (-want +got): %s", diff)
	}
	if got := ev.Actions.StateDelta["winner"]; got != 2 {
		t.Errorf("merged state winner = %v, want 2", got)
	}
	if diff := cmp.Diff(map[string]int64{"tool_0": 0, "tool_2": 2}, ev.Actions.ArtifactDelta); diff != "" {
		t.Errorf("artifact deltas (-want +got): %s", diff)
	}
}

func TestHandleFunctionCallsDefaultStartsAllToolsConcurrently(t *testing.T) {
	parent, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ctx := icontext.NewInvocationContext(parent, icontext.InvocationContextParams{
		Agent:     &mockAgent{name: "tester"},
		RunConfig: &agent.RunConfig{},
	})
	var started atomic.Int32
	allStarted := make(chan struct{})
	fn := &mockFunctionTool{name: "barrier", runFunc: func(ctx tool.Context, _ map[string]any) (map[string]any, error) {
		if started.Add(1) == 3 {
			close(allStarted)
		}
		select {
		case <-allStarted:
			return map[string]any{"ok": true}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	var parts []*genai.Part
	for i := range 3 {
		parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: fmt.Sprint(i), Name: fn.Name()}})
	}
	ev, err := (&Flow{}).handleFunctionCalls(ctx, map[string]tool.Tool{fn.Name(): fn}, &model.LLMResponse{Content: &genai.Content{Parts: parts}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if parent.Err() != nil {
		t.Fatal("tools did not all start concurrently before the deadline")
	}
	if ev == nil || ev.Content == nil || len(ev.Content.Parts) != 3 {
		t.Fatal("missing completed tool responses")
	}
	for _, part := range ev.Content.Parts {
		if part.FunctionResponse.Response["ok"] != true {
			t.Error("tool did not complete the concurrency barrier")
		}
	}
}

func TestHandleFunctionCallsTaskRunnerPreservesCacheHits(t *testing.T) {
	parent := platform.WithTaskRunner(t.Context(), func(ctx context.Context, tasks []func(context.Context)) {
		for _, task := range tasks {
			task(ctx)
		}
	})
	cache := &testToolResponseCache{}
	ctx := icontext.NewInvocationContext(parent, icontext.InvocationContextParams{
		Agent:             &mockAgent{name: "tester"},
		RunConfig:         &agent.RunConfig{DedupeToolCalls: true},
		ToolResponseCache: cache,
	})
	calls := 0
	fn := &mockFunctionTool{name: "cached", runFunc: func(tool.Context, map[string]any) (map[string]any, error) {
		calls++
		return map[string]any{"ok": true}, nil
	}}
	resp := &model.LLMResponse{Content: &genai.Content{Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: "call", Name: fn.Name()}}}}}
	f := &Flow{}
	for i := range 2 {
		ev, err := f.handleFunctionCalls(ctx, map[string]tool.Tool{fn.Name(): fn}, resp, nil)
		if err != nil {
			t.Fatal(err)
		}
		if ev == nil {
			t.Fatal("missing response")
		}
		if got := ev.CustomMetadata["adk_tool_call_cache_hit"] == true; got != (i == 1) {
			t.Errorf("call %d cache hit = %v", i, got)
		}
	}
	if calls != 1 {
		t.Errorf("tool executed %d times, want 1", calls)
	}
}
