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

import "strings"

type claudeModelCapabilities struct {
	adaptiveThinking    bool
	rejectSampling      bool
	rejectForcedToolUse bool
	thinkingOnByDefault bool
	canDisableThinking  bool
}

// capabilitiesForModel is deliberately explicit: a new model must be checked
// against its API contract before we translate generic GenAI options for it.
// Models with no special options can still be called without an entry here.
func capabilitiesForModel(name string) (claudeModelCapabilities, bool) {
	switch {
	case hasModelID(name, "claude-opus-5-5"),
		hasModelID(name, "claude-sonnet-5-5"),
		hasModelID(name, "claude-fable-5-1"):
		// All three default to adaptive thinking and reject disabled/manual
		// thinking. Sonnet 5.5 supports between_tools, but that is not the
		// same as GenAI's ThinkingBudget=0 (disabled).
		return claudeModelCapabilities{
			adaptiveThinking: true, rejectSampling: true,
			rejectForcedToolUse: true, thinkingOnByDefault: true,
		}, true
	case hasModelID(name, "claude-opus-4-7"),
		hasModelID(name, "claude-opus-4-8"):
		return claudeModelCapabilities{adaptiveThinking: true, rejectSampling: true, canDisableThinking: true}, true
	case hasModelID(name, "claude-opus-5"),
		hasModelID(name, "claude-sonnet-5"):
		return claudeModelCapabilities{adaptiveThinking: true, rejectSampling: true, thinkingOnByDefault: true, canDisableThinking: true}, true
	case hasModelID(name, "claude-fable-5"),
		hasModelID(name, "claude-mythos-5"):
		return claudeModelCapabilities{adaptiveThinking: true, rejectSampling: true, thinkingOnByDefault: true}, true
	case hasModelID(name, "claude-opus-4-6"),
		hasModelID(name, "claude-sonnet-4-6"),
		hasModelID(name, "claude-opus-4-5"),
		hasModelID(name, "claude-sonnet-4-5"),
		hasModelID(name, "claude-haiku-4-5"),
		hasModelID(name, "claude-opus-4-1"),
		strings.HasPrefix(name, "claude-opus-4-202"),
		strings.HasPrefix(name, "claude-sonnet-4-202"),
		strings.HasPrefix(name, "claude-3-"):
		// Claude 4.6 still accepts manual thinking, though it is deprecated.
		return claudeModelCapabilities{}, true
	default:
		return claudeModelCapabilities{}, false
	}
}

func hasModelID(name, id string) bool {
	// Only dated aliases inherit capabilities. A future version such as
	// claude-opus-5-6 must be reviewed separately from claude-opus-5-5.
	return name == id || strings.HasPrefix(name, id+"-20")
}
