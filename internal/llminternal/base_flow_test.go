// Copyright 2025 Google LLC
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
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	icontext "google.golang.org/adk/internal/context"
	"google.golang.org/adk/internal/toolinternal"
	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
)

type mockFunctionTool struct {
	name          string
	isLongRunning bool
	runFunc       func(tool.Context, map[string]any) (map[string]any, error)
}

func (m *mockFunctionTool) Name() string {
	return m.name
}

func (m *mockFunctionTool) Description() string {
	return "mock tool"
}

func (m *mockFunctionTool) InputSchema() *genai.Schema {
	return nil
}

func (m *mockFunctionTool) OutputSchema() *genai.Schema {
	return nil
}

func (m *mockFunctionTool) IsLongRunning() bool {
	return m.isLongRunning
}

func (m *mockFunctionTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	return nil
}

func (m *mockFunctionTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	if m.runFunc != nil {
		return m.runFunc(ctx, args.(map[string]any))
	}
	return nil, nil
}

func (m *mockFunctionTool) Declaration() *genai.FunctionDeclaration {
	return nil
}

type testCase struct {
	name                 string
	tool                 toolinternal.FunctionTool
	args                 map[string]any
	beforeToolCallbacks  []BeforeToolCallback
	afterToolCallbacks   []AfterToolCallback
	onToolErrorCallbacks []OnToolErrorCallback
	want                 map[string]any
}

func TestCallTool(t *testing.T) {
	testCases := []testCase{
		{
			name: "tool runs successfully",
			tool: &mockFunctionTool{
				name: "testTool",
				runFunc: func(ctx tool.Context, args map[string]any) (map[string]any, error) {
					return map[string]any{"result": "success"}, nil
				},
			},
			args: map[string]any{"key": "value"},
			want: map[string]any{"result": "success"},
		},
		{
			name: "tool error",
			tool: &mockFunctionTool{
				name: "testTool",
				runFunc: func(ctx tool.Context, args map[string]any) (map[string]any, error) {
					return nil, errors.New("tool error")
				},
			},
			args: map[string]any{"key": "value"},
			want: map[string]any{"error": "tool error"},
		},
		{
			name: "before callback returns result",
			tool: &mockFunctionTool{
				name: "testTool",
				runFunc: func(ctx tool.Context, args map[string]any) (map[string]any, error) {
					t.Error("tool should not be called")
					return nil, nil
				},
			},
			beforeToolCallbacks: []BeforeToolCallback{
				func(ctx tool.Context, tool tool.Tool, args map[string]any) (map[string]any, error) {
					return map[string]any{"result": "intercepted"}, nil
				},
				func(ctx tool.Context, tool tool.Tool, args map[string]any) (map[string]any, error) {
					return map[string]any{"result": "2nd callback should not be called"}, nil
				},
			},
			want: map[string]any{"result": "intercepted"},
		},
		{
			name: "before callback returns error",
			tool: &mockFunctionTool{
				name: "testTool",
				runFunc: func(ctx tool.Context, args map[string]any) (map[string]any, error) {
					t.Error("tool should not be called")
					return nil, nil
				},
			},
			beforeToolCallbacks: []BeforeToolCallback{
				func(ctx tool.Context, tool tool.Tool, args map[string]any) (map[string]any, error) {
					return nil, errors.New("before callback error")
				},
				func(ctx tool.Context, tool tool.Tool, args map[string]any) (map[string]any, error) {
					return nil, errors.New("unexpected error")
				},
			},
			want: map[string]any{"error": "before callback error"},
		},
		{
			name: "after callback modifies result",
			tool: &mockFunctionTool{
				name: "testTool",
				runFunc: func(ctx tool.Context, args map[string]any) (map[string]any, error) {
					return map[string]any{"result": "original"}, nil
				},
			},
			afterToolCallbacks: []AfterToolCallback{
				func(ctx tool.Context, tool tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
					return map[string]any{"result": "modified"}, nil
				},
			},
			want: map[string]any{"result": "modified"},
		},
		{
			name: "after callback handles error",
			tool: &mockFunctionTool{
				name: "testTool",
				runFunc: func(ctx tool.Context, args map[string]any) (map[string]any, error) {
					return nil, errors.New("tool error")
				},
			},
			afterToolCallbacks: []AfterToolCallback{
				func(ctx tool.Context, tool tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
					if err != nil {
						return map[string]any{"result": "error handled"}, nil
					}
					return nil, nil
				},
				func(ctx tool.Context, tool tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
					return map[string]any{"result": "unexpected output"}, nil
				},
			},
			want: map[string]any{"result": "error handled"},
		},
		{
			name: "after callback returns error",
			tool: &mockFunctionTool{
				name: "testTool",
				runFunc: func(ctx tool.Context, args map[string]any) (map[string]any, error) {
					return map[string]any{"result": "success"}, nil
				},
			},
			afterToolCallbacks: []AfterToolCallback{
				func(ctx tool.Context, tool tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
					return nil, errors.New("after callback error")
				},
				func(ctx tool.Context, tool tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
					return nil, errors.New("unexpected error")
				},
			},
			want: map[string]any{"error": "after callback error"},
		},
		{
			name: "no-op callbacks return func results",
			tool: &mockFunctionTool{
				name: "testTool",
				runFunc: func(ctx tool.Context, args map[string]any) (map[string]any, error) {
					return map[string]any{"result": "success"}, nil
				},
			},
			beforeToolCallbacks: []BeforeToolCallback{
				func(ctx tool.Context, tool tool.Tool, args map[string]any) (map[string]any, error) {
					return nil, nil
				},
			},
			afterToolCallbacks: []AfterToolCallback{
				func(ctx tool.Context, tool tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
					return nil, nil
				},
			},
			want: map[string]any{"result": "success"},
		},
		{
			name: "before callback result passed to after callback",
			tool: &mockFunctionTool{
				name: "testTool",
				runFunc: func(ctx tool.Context, args map[string]any) (map[string]any, error) {
					t.Error("tool should not be called")
					return nil, nil
				},
			},
			beforeToolCallbacks: []BeforeToolCallback{
				func(ctx tool.Context, tool tool.Tool, args map[string]any) (map[string]any, error) {
					return map[string]any{"result": "from_before"}, nil
				},
			},
			afterToolCallbacks: []AfterToolCallback{
				func(ctx tool.Context, tool tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
					if val, ok := result["result"]; !ok || val != "from_before" {
						return nil, errors.New("unexpected result in after callback")
					}
					return map[string]any{"result": "from_after"}, nil
				},
			},
			want: map[string]any{"result": "from_after"},
		},
		{
			name: "before callback error passed to after callback",
			tool: &mockFunctionTool{
				name: "testTool",
				runFunc: func(ctx tool.Context, args map[string]any) (map[string]any, error) {
					t.Error("tool should not be called")
					return nil, nil
				},
			},
			beforeToolCallbacks: []BeforeToolCallback{
				func(ctx tool.Context, tool tool.Tool, args map[string]any) (map[string]any, error) {
					return nil, errors.New("error_from_before")
				},
			},
			afterToolCallbacks: []AfterToolCallback{
				func(ctx tool.Context, tool tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
					if err == nil || err.Error() != "error_from_before" {
						return nil, errors.New("unexpected error in after callback")
					}
					return map[string]any{"result": "error_handled_in_after"}, nil
				},
			},
			want: map[string]any{"result": "error_handled_in_after"},
		},
		{
			name: "before callback error passed to on tool error callback",
			tool: &mockFunctionTool{
				name: "testTool",
				runFunc: func(ctx tool.Context, args map[string]any) (map[string]any, error) {
					t.Error("tool should not be called")
					return nil, nil
				},
			},
			beforeToolCallbacks: []BeforeToolCallback{
				func(ctx tool.Context, tool tool.Tool, args map[string]any) (map[string]any, error) {
					return nil, errors.New("error_from_before")
				},
			},
			onToolErrorCallbacks: []OnToolErrorCallback{
				func(ctx tool.Context, tool tool.Tool, args map[string]any, err error) (map[string]any, error) {
					if err == nil || err.Error() != "error_from_before" {
						t.Error("unexpected error in on tool error callback")
						return nil, errors.New("unexpected error in on tool error callback")
					}
					return map[string]any{"result": "error_handled_in_on_tool_error_callback"}, nil
				},
			},
			want: map[string]any{"result": "error_handled_in_on_tool_error_callback"},
		},
		{
			name: "before callback error passed to on tool error callback and after tool called",
			tool: &mockFunctionTool{
				name: "testTool",
				runFunc: func(ctx tool.Context, args map[string]any) (map[string]any, error) {
					t.Error("tool should not be called")
					return nil, nil
				},
			},
			beforeToolCallbacks: []BeforeToolCallback{
				func(ctx tool.Context, tool tool.Tool, args map[string]any) (map[string]any, error) {
					return nil, errors.New("error_from_before")
				},
			},
			onToolErrorCallbacks: []OnToolErrorCallback{
				func(ctx tool.Context, tool tool.Tool, args map[string]any, err error) (map[string]any, error) {
					if err == nil || err.Error() != "error_from_before" {
						t.Error("unexpected error in on tool error callback")
						return nil, errors.New("unexpected error in on tool error callback")
					}
					return map[string]any{"result": "error_handled_in_on_tool_error_callback"}, nil
				},
			},
			afterToolCallbacks: []AfterToolCallback{
				func(ctx tool.Context, tool tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
					if err != nil {
						return nil, errors.New("unexpected error in after callback")
					}
					return map[string]any{"result": "from_after"}, nil
				},
			},
			want: map[string]any{"result": "from_after"},
		},
		{
			name: "before callback error passed to on tool error callback and passed to after tool called",
			tool: &mockFunctionTool{
				name: "testTool",
				runFunc: func(ctx tool.Context, args map[string]any) (map[string]any, error) {
					t.Error("tool should not be called")
					return nil, nil
				},
			},
			beforeToolCallbacks: []BeforeToolCallback{
				func(ctx tool.Context, tool tool.Tool, args map[string]any) (map[string]any, error) {
					return nil, errors.New("error_from_before")
				},
			},
			onToolErrorCallbacks: []OnToolErrorCallback{
				func(ctx tool.Context, tool tool.Tool, args map[string]any, err error) (map[string]any, error) {
					if err == nil || err.Error() != "error_from_before" {
						t.Error("unexpected error in on tool error callback")
						return nil, errors.New("unexpected error in on tool error callback")
					}
					return nil, errors.New("error_from_on_tool_error")
				},
			},
			afterToolCallbacks: []AfterToolCallback{
				func(ctx tool.Context, tool tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
					if err == nil || err.Error() != "error_from_on_tool_error" {
						return nil, errors.New("unexpected error in after callback")
					}
					return nil, errors.New("error_from_after_tool")
				},
			},
			want: map[string]any{"error": "error_from_after_tool"},
		},
		{
			name: "before callback error passed to on tool error callback and passed to after tool called and handled",
			tool: &mockFunctionTool{
				name: "testTool",
				runFunc: func(ctx tool.Context, args map[string]any) (map[string]any, error) {
					t.Error("tool should not be called")
					return nil, nil
				},
			},
			beforeToolCallbacks: []BeforeToolCallback{
				func(ctx tool.Context, tool tool.Tool, args map[string]any) (map[string]any, error) {
					return nil, errors.New("error_from_before")
				},
			},
			onToolErrorCallbacks: []OnToolErrorCallback{
				func(ctx tool.Context, tool tool.Tool, args map[string]any, err error) (map[string]any, error) {
					if err == nil || err.Error() != "error_from_before" {
						t.Error("unexpected error in on tool error callback")
						return nil, errors.New("unexpected error in on tool error callback")
					}
					return nil, errors.New("error_from_on_tool_error")
				},
			},
			afterToolCallbacks: []AfterToolCallback{
				func(ctx tool.Context, tool tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
					if err == nil || err.Error() != "error_from_on_tool_error" {
						return nil, errors.New("unexpected error in after callback")
					}
					return map[string]any{"result": "error_handled_in_on_tool_error_callback"}, nil
				},
			},
			want: map[string]any{"result": "error_handled_in_on_tool_error_callback"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			f := &Flow{
				BeforeToolCallbacks:  tc.beforeToolCallbacks,
				AfterToolCallbacks:   tc.afterToolCallbacks,
				OnToolErrorCallbacks: tc.onToolErrorCallbacks,
			}
			ctx := icontext.NewInvocationContext(t.Context(), icontext.InvocationContextParams{})
			got, _ := f.callTool(ctx, toolinternal.NewToolContext(ctx, "", nil, nil), tc.tool, tc.args)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("callTool() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

type testToolResponseCache struct {
	responses map[string]map[string]any
	getKeys   []string
	setKeys   []string
}

func (c *testToolResponseCache) Get(ctx context.Context, key string) (map[string]any, bool) {
	c.getKeys = append(c.getKeys, key)
	resp, ok := c.responses[key]
	return resp, ok
}

func (c *testToolResponseCache) Set(ctx context.Context, key string, value map[string]any) {
	if c.responses == nil {
		c.responses = map[string]map[string]any{}
	}
	c.setKeys = append(c.setKeys, key)
	c.responses[key] = value
}

func (c *testToolResponseCache) Invalidate(ctx context.Context, key string) {
	delete(c.responses, key)
}

func TestCallToolDedup(t *testing.T) {
	tests := []struct {
		name          string
		runConfig     *agent.RunConfig
		isLongRunning bool
		wantDedup     bool
	}{
		{
			name:      "dedupe disabled",
			runConfig: &agent.RunConfig{},
		},
		{
			name: "dedupe enabled by run config",
			runConfig: &agent.RunConfig{
				DedupeToolCalls: true,
			},
			wantDedup: true,
		},
		{
			name:          "dedupe enabled for long running tool",
			isLongRunning: true,
			wantDedup:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := &testToolResponseCache{}
			ctx := icontext.NewInvocationContext(t.Context(), icontext.InvocationContextParams{
				Branch:            "branch",
				RunConfig:         tt.runConfig,
				ToolResponseCache: cache,
			})
			args := map[string]any{"key": "value"}
			callCount := 0
			fnTool := &mockFunctionTool{
				name:          "testTool",
				isLongRunning: tt.isLongRunning,
				runFunc: func(ctx tool.Context, args map[string]any) (map[string]any, error) {
					callCount++
					return map[string]any{"result": "success"}, nil
				},
			}
			f := &Flow{}

			first, firstHit := f.callTool(ctx, toolinternal.NewToolContext(ctx, "", nil, nil), fnTool, args)
			second, secondHit := f.callTool(ctx, toolinternal.NewToolContext(ctx, "", nil, nil), fnTool, args)

			want := map[string]any{"result": "success"}
			if diff := cmp.Diff(want, first); diff != "" {
				t.Errorf("first callTool() response mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(want, second); diff != "" {
				t.Errorf("second callTool() response mismatch (-want +got):\n%s", diff)
			}

			if tt.wantDedup {
				if firstHit {
					t.Errorf("first callTool() cache hit = true, want false")
				}
				if !secondHit {
					t.Errorf("second callTool() cache hit = false, want true")
				}
				if callCount != 1 {
					t.Errorf("tool run count = %d, want 1", callCount)
				}

				wantCacheKey := toolCallCacheKey("branch", "testTool", args)
				if diff := cmp.Diff([]string{wantCacheKey, wantCacheKey}, cache.getKeys); diff != "" {
					t.Errorf("cache get keys mismatch (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(want, cache.responses[wantCacheKey]); diff != "" {
					t.Errorf("cached response mismatch (-want +got):\n%s", diff)
				}
				return
			}

			if firstHit {
				t.Errorf("first callTool() cache hit = true, want false")
			}
			if secondHit {
				t.Errorf("second callTool() cache hit = true, want false")
			}
			if callCount != 2 {
				t.Errorf("tool run count = %d, want 2", callCount)
			}
			if len(cache.getKeys) != 0 {
				t.Errorf("cache get keys = %v, want empty", cache.getKeys)
			}
			if len(cache.setKeys) != 0 {
				t.Errorf("cache set keys = %v, want empty", cache.setKeys)
			}
		})
	}
}

func TestMergeEventActions(t *testing.T) {
	tests := []struct {
		name  string
		base  *session.EventActions
		other *session.EventActions
		want  *session.EventActions
	}{
		{
			name:  "both nil",
			base:  nil,
			other: nil,
			want:  nil,
		},
		{
			name: "other nil returns base",
			base: &session.EventActions{
				StateDelta: map[string]any{"key1": "value1"},
			},
			other: nil,
			want: &session.EventActions{
				StateDelta: map[string]any{"key1": "value1"},
			},
		},
		{
			name: "base nil returns other",
			base: nil,
			other: &session.EventActions{
				StateDelta: map[string]any{"key1": "value1"},
			},
			want: &session.EventActions{
				StateDelta: map[string]any{"key1": "value1"},
			},
		},
		{
			name: "state delta merged with non-overlapping keys",
			base: &session.EventActions{
				StateDelta: map[string]any{"key1": "value1"},
			},
			other: &session.EventActions{
				StateDelta: map[string]any{"key2": "value2"},
			},
			want: &session.EventActions{
				StateDelta: map[string]any{"key1": "value1", "key2": "value2"},
			},
		},
		{
			name: "state delta merged with overlapping keys - later wins",
			base: &session.EventActions{
				StateDelta: map[string]any{"key1": "original"},
			},
			other: &session.EventActions{
				StateDelta: map[string]any{"key1": "overwritten"},
			},
			want: &session.EventActions{
				StateDelta: map[string]any{"key1": "overwritten"},
			},
		},
		{
			name: "state delta merged with nested map values",
			base: &session.EventActions{
				StateDelta: map[string]any{
					"outer": map[string]any{"key1": "value1", "key2": "value2"},
				},
			},
			other: &session.EventActions{
				StateDelta: map[string]any{
					"outer": map[string]any{"key2": "updated", "key3": "value3"},
				},
			},
			want: &session.EventActions{
				StateDelta: map[string]any{
					"outer": map[string]any{"key1": "value1", "key2": "updated", "key3": "value3"},
				},
			},
		},
		{
			name: "state delta merged with multiple keys from multiple tools",
			base: &session.EventActions{
				StateDelta: map[string]any{"tool1_key": "tool1_value"},
			},
			other: &session.EventActions{
				StateDelta: map[string]any{"tool2_key": "tool2_value", "tool3_key": "tool3_value"},
			},
			want: &session.EventActions{
				StateDelta: map[string]any{
					"tool1_key": "tool1_value",
					"tool2_key": "tool2_value",
					"tool3_key": "tool3_value",
				},
			},
		},
		{
			name: "base has nil state delta, other has values",
			base: &session.EventActions{
				SkipSummarization: true,
			},
			other: &session.EventActions{
				StateDelta: map[string]any{"key1": "value1"},
			},
			want: &session.EventActions{
				SkipSummarization: true,
				StateDelta:        map[string]any{"key1": "value1"},
			},
		},
		{
			name: "skip summarization merging - any true wins",
			base: &session.EventActions{
				SkipSummarization: false,
			},
			other: &session.EventActions{
				SkipSummarization: true,
			},
			want: &session.EventActions{
				SkipSummarization: true,
			},
		},
		{
			name: "escalate merging - any true wins",
			base: &session.EventActions{
				Escalate: false,
			},
			other: &session.EventActions{
				Escalate: true,
			},
			want: &session.EventActions{
				Escalate: true,
			},
		},
		{
			name: "transfer to agent - last wins",
			base: &session.EventActions{
				TransferToAgent: "agent1",
			},
			other: &session.EventActions{
				TransferToAgent: "agent2",
			},
			want: &session.EventActions{
				TransferToAgent: "agent2",
			},
		},
		{
			name: "all fields merged correctly",
			base: &session.EventActions{
				StateDelta:        map[string]any{"key1": "value1"},
				SkipSummarization: false,
				TransferToAgent:   "agent1",
				Escalate:          false,
			},
			other: &session.EventActions{
				StateDelta:        map[string]any{"key2": "value2"},
				SkipSummarization: true,
				TransferToAgent:   "agent2",
				Escalate:          true,
			},
			want: &session.EventActions{
				StateDelta:        map[string]any{"key1": "value1", "key2": "value2"},
				SkipSummarization: true,
				TransferToAgent:   "agent2",
				Escalate:          true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeEventActions(tc.base, tc.other)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("mergeEventActions() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
