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

package anthropic

import "testing"

func TestDocumentedClaudeMessageModelsHaveCapabilities(t *testing.T) {
	// Includes active and deprecated callable Messages models. A capability
	// entry describes request translation, not availability on Google Cloud.
	for _, name := range []string{
		"claude-fable-5-1",
		"claude-mythos-5-1",
		"claude-fable-5",
		"claude-mythos-5",
		"claude-mythos-preview",
		"claude-opus-5-5",
		"claude-opus-5",
		"claude-opus-4-8",
		"claude-opus-4-7",
		"claude-opus-4-6",
		"claude-opus-4-5-20251101",
		"claude-opus-4-5@20251101",
		"claude-sonnet-5-5",
		"claude-sonnet-5",
		"claude-sonnet-4-6",
		"claude-sonnet-4-5-20250929",
		"claude-sonnet-4-5@20250929",
		"claude-haiku-5-5",
		"claude-haiku-4-5-20251001",
		"claude-haiku-4-5@20251001",
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := capabilitiesForModel(name); !ok {
				t.Fatalf("capabilitiesForModel(%q) is unknown", name)
			}
		})
	}
}

func TestNewClaudeModelCapabilities(t *testing.T) {
	for _, tt := range []struct {
		name                string
		adaptive, manual    bool
		canDisable          bool
		rejectSampling      bool
		rejectForcedToolUse bool
	}{
		{"claude-haiku-5-5", true, false, true, true, false},
		{"claude-mythos-5-1", true, false, false, true, true},
		{"claude-mythos-preview", true, true, false, true, false},
		{"claude-opus-4-6", true, true, false, false, false},
		{"claude-sonnet-4-6", true, true, false, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			caps, ok := capabilitiesForModel(tt.name)
			if !ok {
				t.Fatal("unknown model")
			}
			if caps.adaptiveThinking != tt.adaptive || caps.manualThinking != tt.manual ||
				caps.canDisableThinking != tt.canDisable || caps.rejectSampling != tt.rejectSampling ||
				caps.rejectForcedToolUse != tt.rejectForcedToolUse {
				t.Fatalf("capabilitiesForModel(%q) = %+v", tt.name, caps)
			}
		})
	}
}

func TestNewerModelDoesNotInheritCapabilities(t *testing.T) {
	for _, name := range []string{"claude-haiku-5-6", "claude-mythos-5-2", "claude-opus-5-6"} {
		if _, ok := capabilitiesForModel(name); ok {
			t.Errorf("capabilitiesForModel(%q) should require a review", name)
		}
	}
}
