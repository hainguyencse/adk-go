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

// Package skilltool provides the standard tools for the skill toolset.
package skilltool

import (
	"fmt"
	"slices"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/adk/tool/skilltoolset/skill"
)

// ActivatedSkillsStateKey returns the state key that holds the skills
// activated by the given agent.
func ActivatedSkillsStateKey(agentName string) string {
	return fmt.Sprintf("_adk_activated_skill_%s", agentName)
}

// recordActivation writes skillName into the per-agent activated-skills set in
// state so that SkillToolset.Tools() can later resolve adk_additional_tools.
// It silently no-ops when ctx is incomplete (e.g. in unit tests).
//
// The set is stored as map[string]any{skillName: true} rather than a list.
// Parallel tool calls each get their own state delta, and those deltas are
// deep-merged afterwards: maps are merged key by key, while a list from one
// call would overwrite the list from another, losing activations when several
// load_skill calls run in the same model response.
func recordActivation(ctx tool.Context, skillName string) {
	if ctx == nil {
		return
	}
	// AgentName() may panic when the context has no agent (test-only scenario).
	var agentName string
	func() {
		defer func() { recover() }() //nolint:errcheck
		agentName = ctx.AgentName()
	}()
	if agentName == "" {
		return
	}
	stateKey := ActivatedSkillsStateKey(agentName)
	existing, _ := ctx.State().Get(stateKey)
	activated := make(map[string]any)
	for _, name := range ActivatedSkillNames(existing) {
		activated[name] = true
	}
	// Always write the map form, even when skillName is already present, so
	// state saved in the legacy list form is migrated on the next load.
	activated[skillName] = true
	ctx.State().Set(stateKey, activated) //nolint:errcheck
}

// ActivatedSkillNames returns the skill names stored under
// ActivatedSkillsStateKey, sorted. It accepts the current map form and the
// legacy list form ([]string or []any).
func ActivatedSkillNames(value any) []string {
	var result []string
	switch activatedSkills := value.(type) {
	case map[string]any:
		for name, active := range activatedSkills {
			if ok, _ := active.(bool); ok && name != "" {
				result = append(result, name)
			}
		}
	case []string:
		for _, name := range activatedSkills {
			if name != "" {
				result = append(result, name)
			}
		}
	case []any:
		for _, activatedSkill := range activatedSkills {
			if name, ok := activatedSkill.(string); ok && name != "" {
				result = append(result, name)
			}
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

// LoadSkillArgs represents the input to load a skill.
type LoadSkillArgs struct {
	Name string `json:"name" jsonschema:"The name of the skill to load."`
}

type FrontmatterJSON struct {
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	License       string         `json:"license,omitempty"`
	Compatibility string         `json:"compatibility,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	AllowedTools  []string       `json:"allowed-tools,omitempty"`
}

// LoadSkillResult represents the output of a loaded skill.
type LoadSkillResult struct {
	SkillName    string           `json:"skill_name,omitempty"`
	Instructions string           `json:"instructions,omitempty"`
	Frontmatter  *FrontmatterJSON `json:"frontmatter,omitempty"`
}

// LoadSkill creates a tool.Tool to load a skill's instructions.
func LoadSkill(source skill.Source) (tool.Tool, error) {
	return functiontool.New(
		functiontool.Config{
			Name:        "load_skill",
			Description: "Loads the SKILL.md instructions for a given skill.",
		},
		func(ctx tool.Context, args LoadSkillArgs) (*LoadSkillResult, error) {
			return loadSkill(ctx, args, source)
		},
	)
}

func loadSkill(ctx tool.Context, args LoadSkillArgs, source skill.Source) (*LoadSkillResult, error) {
	if args.Name == "" {
		return nil, fmt.Errorf("skill name is required to load a skill")
	}
	frontmatter, err := source.LoadFrontmatter(ctx, args.Name)
	if err != nil {
		return nil, fmt.Errorf("load frontmatter for skill %q: %w", args.Name, err)
	}
	instructions, err := source.LoadInstructions(ctx, args.Name)
	if err != nil {
		return nil, fmt.Errorf("load instructions for skill %q: %w", args.Name, err)
	}

	// Record skill activation in agent state so Tools() can resolve additional tools.
	recordActivation(ctx, args.Name)

	return &LoadSkillResult{
		SkillName:    args.Name,
		Instructions: instructions,
		Frontmatter: &FrontmatterJSON{
			Name:          frontmatter.Name,
			Description:   frontmatter.Description,
			License:       frontmatter.License,
			Compatibility: frontmatter.Compatibility,
			Metadata:      frontmatter.Metadata,
			AllowedTools:  frontmatter.AllowedTools,
		},
	}, nil
}
