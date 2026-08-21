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

package agent

import (
	"context"
	"fmt"
	"iter"
	"time"

	"github.com/google/uuid"
	"google.golang.org/genai"

	"google.golang.org/adk/artifact"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool/toolconfirmation"
)

// Context is the unified context passed to workflow nodes (see the
// google.golang.org/adk/workflow package) and, via the google.golang.org/adk/tool
// package's Context alias, to tools when they are called. It provides
// access to the originating function call, mutable event actions, memory
// search, the Human-in-the-Loop (HITL) confirmation flow, and the
// workflow-node scheduling primitives (Path, RunID, SubScheduler, ...).
//
// It is additive: existing code that only used [ReadonlyContext],
// [CallbackContext], or the (now-aliased) tool.Context keeps working
// unchanged, since Context is a strict superset of all of them.
type Context interface {
	ReadonlyContext
	InvocationContext

	// Callback context
	Artifacts() Artifacts
	State() session.State

	// Tool context section

	// FunctionCallID returns the unique identifier of the function call
	// that triggered this tool execution.
	FunctionCallID() string

	// Actions returns the EventActions for the current event. This can be
	// used by the tool to modify the agent's state, transfer to another
	// agent, or perform other actions.
	Actions() *session.EventActions

	// SearchMemory performs a semantic search on the agent's memory.
	SearchMemory(ctx context.Context, query string) (*memory.SearchResponse, error)

	// ToolConfirmation returns a handler for checking the Human-in-the-Loop
	// confirmation status for the current tool context.
	ToolConfirmation() *toolconfirmation.ToolConfirmation

	// RequestConfirmation initiates the Human-in-the-Loop (HITL) process to
	// ask the user for approval before the tool proceeds with a specific
	// action.
	RequestConfirmation(hint string, payload any) error

	// IsolationScope of the context. When set, a node's isolated view of
	// the session should include only events whose IsolationScope matches
	// exactly. Empty means unscoped.
	IsolationScope() string

	// ResumedInput returns the user-supplied response payload associated
	// with the given InterruptID for the current activation, or (nil,
	// false) if none.
	ResumedInput(interruptID string) (any, bool)

	// WithICDelta returns a copy of this context with changes applied to
	// the wrapped InvocationContext.
	WithICDelta(d *InvocationContextDelta) InvocationContext

	// Workflow node section

	// Path returns the composite path of the currently-executing node.
	// Empty for top-level static nodes; "<parent_path>/<child_name>@<run_id>"
	// for dynamic children.
	Path() string

	// RunID returns the per-invocation identifier. Empty for top-level
	// static nodes; auto-counter or user-supplied for dynamic children.
	RunID() string

	// SubScheduler is non-nil only when this context belongs to a
	// dynamic-node activation; RunNode uses it to schedule children.
	SubScheduler() DynamicSubScheduler

	// WithAgentContext creates a new context as a shallow copy setting the
	// internal Go context.Context to ctx.
	WithAgentContext(ctx context.Context) Context

	// WithAgentTimeout creates a new context as a shallow copy, adding a
	// timeout to the top of the underlying context.Context.
	WithAgentTimeout(timeout time.Duration) (Context, context.CancelFunc)

	// WithAgentCancel creates a new context as a shallow copy, adding
	// cancellation to the top of the underlying context.Context.
	WithAgentCancel() (Context, context.CancelFunc)

	// OutputForAncestors are the delegating-ancestor paths carried into
	// this activation; its dynamic sub-scheduler reads them to stamp
	// OutputFor.
	OutputForAncestors() []string

	// WithDelta returns a copy of this context with delta d applied.
	WithDelta(d *CommonContextDelta) Context
}

// Promote wraps parent as a Context if it is not one already.
func Promote(parent InvocationContext) Context {
	if c, ok := parent.(*commonContext); ok {
		return c
	}
	cc := &commonContext{
		Context:           parent,
		invocationContext: parent,
	}
	seedFromExtras(cc, parent)
	return cc
}

// PromoteWithDelta is a shortcut for Promote followed by WithDelta.
func PromoteWithDelta(ctx InvocationContext, delta *CommonContextDelta) Context {
	return Promote(ctx).WithDelta(delta)
}

// NewContext returns a full Context backed by parent, with no callback,
// tool, or node specialization.
func NewContext(parent InvocationContext) Context {
	if p, ok := parent.(*commonContext); ok {
		res := *p
		return &res
	}
	cc := &commonContext{
		Context:           parent,
		invocationContext: parent,
	}
	seedFromExtras(cc, parent)
	return cc
}

// seedFromExtras copies the shim-local IsolationScope from parent onto cc
// when parent happens to implement it (e.g. a hand-written test
// InvocationContext), so wrapping it in a fresh commonContext via
// Promote/NewContext doesn't silently drop it. ResumedInput falls back to
// parent directly at call time; see commonContext.ResumedInput.
func seedFromExtras(cc *commonContext, parent InvocationContext) {
	if scoped, ok := parent.(interface{ IsolationScope() string }); ok {
		cc.isolationScope = scoped.IsolationScope()
	}
}

// NewCallbackContext returns a Context initialized with the provided
// EventActions, suitable for passing to before/after agent and model
// callbacks. actions may be nil.
func NewCallbackContext(ic InvocationContext, actions *session.EventActions) Context {
	actions = prepareEventActions(actions)
	return &commonContext{
		Context:           ic,
		invocationContext: ic,
		actions:           actions,
		artifacts:         ic.Artifacts(),
	}
}

// NewToolContext constructs a Context for a tool execution. If
// functionCallID is empty a new UUID is generated. If actions is nil a
// fresh session.EventActions is allocated; missing sub-maps are populated.
func NewToolContext(ic InvocationContext, functionCallID string, actions *session.EventActions, confirmation *toolconfirmation.ToolConfirmation) Context {
	var res commonContext
	if c, ok := ic.(*commonContext); ok {
		res = *c
	} else {
		res = commonContext{
			Context:           ic,
			invocationContext: ic,
		}
	}

	if functionCallID == "" {
		functionCallID = uuid.NewString()
	}
	actions = prepareEventActions(actions)

	res.actions = actions
	res.functionCallID = functionCallID
	res.toolConfirmation = confirmation
	res.artifacts = &trackedArtifacts{Artifacts: ic.Artifacts(), actions: actions}

	return &res
}

func prepareEventActions(actions *session.EventActions) *session.EventActions {
	if actions == nil {
		return &session.EventActions{StateDelta: make(map[string]any), ArtifactDelta: make(map[string]int64)}
	}
	if actions.StateDelta == nil {
		actions.StateDelta = make(map[string]any)
	}
	if actions.ArtifactDelta == nil {
		actions.ArtifactDelta = make(map[string]int64)
	}
	return actions
}

// commonContext is the single concrete implementation of Context: it is
// used for callback contexts, tool contexts, and (static and dynamic)
// workflow node contexts alike.
type commonContext struct {
	context.Context
	invocationContext InvocationContext
	artifacts         Artifacts
	actions           *session.EventActions

	// Fields below are only populated by NewToolContext.
	functionCallID   string
	toolConfirmation *toolconfirmation.ToolConfirmation

	// isolationScope and resumeInputs are shim-local: the wrapped
	// InvocationContext has no notion of them, so they live only on this
	// struct and are not fed back into e.g. LLM prompt-history filtering.
	isolationScope string
	resumeInputs   map[string]any

	// path and runID are populated for dynamic children, empty for
	// top-level static activations.
	path  string
	runID string

	// subScheduler is non-nil only when this context belongs to a
	// dynamic-node activation; RunNode uses it to schedule children.
	subScheduler DynamicSubScheduler

	// outputForAncestors are the delegating-ancestor paths carried into
	// this activation.
	outputForAncestors []string

	// overrides applied via WithICDelta/WithDelta; nil means "delegate to
	// invocationContext".
	agentOverride       *Agent
	branchOverride      *string
	userContentOverride **genai.Content
}

var (
	_ Context           = (*commonContext)(nil)
	_ InvocationContext = (*commonContext)(nil)
	_ ReadonlyContext   = (*commonContext)(nil)
)

// --- Node/scheduling extensions ---------------------------------------------

func (c *commonContext) SubScheduler() DynamicSubScheduler { return c.subScheduler }
func (c *commonContext) Path() string                      { return c.path }
func (c *commonContext) RunID() string                     { return c.runID }

func (c *commonContext) OutputForAncestors() []string {
	if c.outputForAncestors != nil {
		return c.outputForAncestors
	}
	if p, ok := c.Context.(interface{ OutputForAncestors() []string }); ok {
		return p.OutputForAncestors()
	}
	return nil
}

// --- InvocationContext -------------------------------------------------------

func (c *commonContext) Agent() Agent {
	if c.agentOverride != nil {
		return *c.agentOverride
	}
	return c.invocationContext.Agent()
}

func (c *commonContext) Memory() Memory           { return c.invocationContext.Memory() }
func (c *commonContext) Session() session.Session { return c.invocationContext.Session() }
func (c *commonContext) InvocationID() string     { return c.invocationContext.InvocationID() }
func (c *commonContext) RunConfig() *RunConfig    { return c.invocationContext.RunConfig() }
func (c *commonContext) EndInvocation()           { c.invocationContext.EndInvocation() }
func (c *commonContext) Ended() bool              { return c.invocationContext.Ended() }

func (c *commonContext) Branch() string {
	if c.branchOverride != nil {
		return *c.branchOverride
	}
	return c.invocationContext.Branch()
}

func (c *commonContext) UserContent() *genai.Content {
	if c.userContentOverride != nil {
		return *c.userContentOverride
	}
	return c.invocationContext.UserContent()
}

func (c *commonContext) IsolationScope() string { return c.isolationScope }

func (c *commonContext) ResumedInput(interruptID string) (any, bool) {
	if c.resumeInputs != nil {
		if v, ok := c.resumeInputs[interruptID]; ok {
			return v, true
		}
	}
	if resumable, ok := c.invocationContext.(interface {
		ResumedInput(string) (any, bool)
	}); ok {
		return resumable.ResumedInput(interruptID)
	}
	return nil, false
}

func (c *commonContext) WithContext(ctx context.Context) InvocationContext {
	return c.WithAgentContext(ctx)
}

func (c *commonContext) WithAgentContext(ctx context.Context) Context {
	res := *c
	if ic, ok := ctx.(InvocationContext); ok {
		res.Context = ic
		res.invocationContext = ic
	} else {
		res.Context = ctx
	}
	return &res
}

func (c *commonContext) WithAgentTimeout(timeout time.Duration) (Context, context.CancelFunc) {
	res := *c
	newCtx, cancel := context.WithTimeout(res.Context, timeout)
	res.Context = newCtx
	return &res, cancel
}

func (c *commonContext) WithAgentCancel() (Context, context.CancelFunc) {
	res := *c
	newCtx, cancel := context.WithCancel(res.Context)
	res.Context = newCtx
	return &res, cancel
}

func (c *commonContext) LiveRequestQueue() *LiveRequestQueue {
	return c.invocationContext.LiveRequestQueue()
}

func (c *commonContext) TranscriptionCache() []TranscriptionEntry {
	return c.invocationContext.TranscriptionCache()
}

func (c *commonContext) InputRealtimeCache() []RealtimeCacheEntry {
	return c.invocationContext.InputRealtimeCache()
}

func (c *commonContext) OutputRealtimeCache() []RealtimeCacheEntry {
	return c.invocationContext.OutputRealtimeCache()
}

func (c *commonContext) ResumabilityConfig() *ResumabilityConfig {
	return c.invocationContext.ResumabilityConfig()
}

func (c *commonContext) AppendInputRealtimeCache(entry RealtimeCacheEntry) {
	c.invocationContext.AppendInputRealtimeCache(entry)
}

func (c *commonContext) AppendOutputRealtimeCache(entry RealtimeCacheEntry) {
	c.invocationContext.AppendOutputRealtimeCache(entry)
}

func (c *commonContext) ClearInputRealtimeCache() { c.invocationContext.ClearInputRealtimeCache() }
func (c *commonContext) ClearOutputRealtimeCache() {
	c.invocationContext.ClearOutputRealtimeCache()
}

func (c *commonContext) LiveSessionResumptionHandle() string {
	return c.invocationContext.LiveSessionResumptionHandle()
}

func (c *commonContext) SetLiveSessionResumptionHandle(h string) {
	c.invocationContext.SetLiveSessionResumptionHandle(h)
}

func (c *commonContext) ToolResponseCache() ToolResponseCache {
	return c.invocationContext.ToolResponseCache()
}

func (c *commonContext) GetCachedToolResponse(ctx context.Context, key string) (map[string]any, bool) {
	return c.invocationContext.GetCachedToolResponse(ctx, key)
}

func (c *commonContext) SetCachedToolResponse(ctx context.Context, key string, result map[string]any) {
	c.invocationContext.SetCachedToolResponse(ctx, key, result)
}

// --- ReadonlyContext ---------------------------------------------------------

func (c *commonContext) AgentName() string { return c.Agent().Name() }

func (c *commonContext) ReadonlyState() session.ReadonlyState {
	return c.invocationContext.Session().State()
}

func (c *commonContext) AppName() string   { return c.invocationContext.Session().AppName() }
func (c *commonContext) SessionID() string { return c.invocationContext.Session().ID() }
func (c *commonContext) UserID() string    { return c.invocationContext.Session().UserID() }

// --- CallbackContext ----------------------------------------------------------

func (c *commonContext) Artifacts() Artifacts {
	if c.artifacts != nil {
		return c.artifacts
	}
	return c.invocationContext.Artifacts()
}

func (c *commonContext) State() session.State {
	return &commonContextState{ctx: c}
}

// --- Tool-context extensions --------------------------------------------------
//
// Always present on *commonContext but only meaningful when constructed via
// NewToolContext (i.e. when functionCallID is set).

func (c *commonContext) FunctionCallID() string { return c.functionCallID }

func (c *commonContext) Actions() *session.EventActions { return c.actions }

func (c *commonContext) SearchMemory(ctx context.Context, query string) (*memory.SearchResponse, error) {
	if c.invocationContext.Memory() == nil {
		return nil, fmt.Errorf("memory service is not set")
	}
	return c.invocationContext.Memory().Search(ctx, query)
}

func (c *commonContext) ToolConfirmation() *toolconfirmation.ToolConfirmation {
	return c.toolConfirmation
}

func (c *commonContext) RequestConfirmation(hint string, payload any) error {
	if c.functionCallID == "" {
		return fmt.Errorf("error function call id not set when requesting confirmation for tool")
	}
	if c.actions.RequestedToolConfirmations == nil {
		c.actions.RequestedToolConfirmations = make(map[string]toolconfirmation.ToolConfirmation)
	}
	c.actions.RequestedToolConfirmations[c.functionCallID] = toolconfirmation.ToolConfirmation{
		Hint:      hint,
		Confirmed: false,
		Payload:   payload,
	}
	// SkipSummarization stops the agent loop after this tool call.
	c.actions.SkipSummarization = true
	return nil
}

func (c *commonContext) InvocationContext() InvocationContext { return c.invocationContext }

// commonContextState is a session.State implementation backed by the
// context's EventActions.StateDelta and the underlying session state.
type commonContextState struct {
	ctx *commonContext
}

func (s *commonContextState) Get(key string) (any, error) {
	if s.ctx.actions != nil && s.ctx.actions.StateDelta != nil {
		if val, ok := s.ctx.actions.StateDelta[key]; ok {
			return val, nil
		}
	}
	sess := s.ctx.invocationContext.Session()
	if sess == nil {
		return nil, fmt.Errorf("cannot get key %q from state: session is nil", key)
	}
	return sess.State().Get(key)
}

func (s *commonContextState) Set(key string, val any) error {
	if s.ctx.actions != nil {
		if s.ctx.actions.StateDelta == nil {
			s.ctx.actions.StateDelta = make(map[string]any)
		}
		s.ctx.actions.StateDelta[key] = val
	}
	sess := s.ctx.invocationContext.Session()
	if sess == nil {
		return fmt.Errorf("cannot set key %q to state: session is nil", key)
	}
	return sess.State().Set(key, val)
}

func (s *commonContextState) All() iter.Seq2[string, any] {
	return s.ctx.invocationContext.Session().State().All()
}

// trackedArtifacts wraps an Artifacts to record each successful Save into
// the supplied EventActions.ArtifactDelta.
type trackedArtifacts struct {
	Artifacts
	actions *session.EventActions
}

func (a *trackedArtifacts) Save(ctx context.Context, name string, data *genai.Part) (*artifact.SaveResponse, error) {
	resp, err := a.Artifacts.Save(ctx, name, data)
	if err != nil {
		return resp, err
	}
	if a.actions != nil {
		if a.actions.ArtifactDelta == nil {
			a.actions.ArtifactDelta = make(map[string]int64)
		}
		a.actions.ArtifactDelta[name] = resp.Version
	}
	return resp, nil
}
