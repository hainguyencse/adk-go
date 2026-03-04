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
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/a2aproject/a2a-go/log"
	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/internal/agent/parentmap"
	"google.golang.org/adk/internal/agent/runconfig"
	icontext "google.golang.org/adk/internal/context"
	"google.golang.org/adk/internal/plugininternal/plugincontext"
	"google.golang.org/adk/internal/telemetry"
	"google.golang.org/adk/internal/toolinternal"
	"google.golang.org/adk/internal/utils"
	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/toolconfirmation"
)

var ErrModelNotConfigured = errors.New("model not configured; ensure Model is set in llmagent.Config")

type BeforeModelCallback func(ctx agent.CallbackContext, llmRequest *model.LLMRequest) (*model.LLMResponse, error)

type AfterModelCallback func(ctx agent.CallbackContext, llmResponse *model.LLMResponse, llmResponseError error) (*model.LLMResponse, error)

type OnModelErrorCallback func(ctx agent.CallbackContext, llmRequest *model.LLMRequest, llmResponseError error) (*model.LLMResponse, error)

type BeforeToolCallback func(ctx tool.Context, tool tool.Tool, args map[string]any) (map[string]any, error)

type AfterToolCallback func(ctx tool.Context, tool tool.Tool, args, result map[string]any, err error) (map[string]any, error)

type OnToolErrorCallback func(ctx tool.Context, tool tool.Tool, args map[string]any, err error) (map[string]any, error)

type Flow struct {
	Model model.LLM

	Tools                 []tool.Tool
	RequestProcessors     []func(ctx agent.InvocationContext, req *model.LLMRequest, f *Flow) iter.Seq2[*session.Event, error]
	ResponseProcessors    []func(ctx agent.InvocationContext, req *model.LLMRequest, resp *model.LLMResponse) error
	BeforeModelCallbacks  []BeforeModelCallback
	AfterModelCallbacks   []AfterModelCallback
	OnModelErrorCallbacks []OnModelErrorCallback
	BeforeToolCallbacks   []BeforeToolCallback
	AfterToolCallbacks    []AfterToolCallback
	OnToolErrorCallbacks  []OnToolErrorCallback
	AudioCacheManager     *AudioCacheManager
}

var (
	DefaultRequestProcessors = []func(ctx agent.InvocationContext, req *model.LLMRequest, f *Flow) iter.Seq2[*session.Event, error]{
		basicRequestProcessor,
		toolProcessor,
		authPreprocessor,
		RequestConfirmationRequestProcessor,
		instructionsRequestProcessor,
		identityRequestProcessor,
		ContentsRequestProcessor,
		// Some implementations of NL Planning mark planning contents as thoughts in the post processor.
		// Since these need to be unmarked, NL Planning should be after contentsRequestProcessor.
		nlPlanningRequestProcessor,
		// Code execution should be after contentsRequestProcessor as it mutates the contents
		// to optimize data files.
		codeExecutionRequestProcessor,
		outputSchemaRequestProcessor,
		AgentTransferRequestProcessor,
		removeDisplayNameIfExists,
	}
	DefaultResponseProcessors = []func(ctx agent.InvocationContext, req *model.LLMRequest, resp *model.LLMResponse) error{
		nlPlanningResponseProcessor,
		codeExecutionResponseProcessor,
	}
)

func (f *Flow) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		for {
			var lastEvent *session.Event
			for ev, err := range f.runOneStep(ctx) {
				if err != nil {
					yield(nil, err)
					return
				}
				// forward the event first.
				if !yield(ev, nil) {
					return
				}
				lastEvent = ev
			}
			if lastEvent == nil || lastEvent.IsFinalResponse() {
				return
			}
			if lastEvent.LLMResponse.Partial {
				// We may have reached max token limit during streaming mode.
				// TODO: handle Partial response in model level. CL 781377328
				yield(nil, fmt.Errorf("TODO: last event is not final"))
				return
			}
		}
	}
}

func (f *Flow) RunLive(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		if f.Model == nil {
			yield(nil, fmt.Errorf("agent %q: %w", ctx.Agent().Name(), ErrModelNotConfigured))
			return
		}

		req := &model.LLMRequest{
			Model:             f.Model.Name(),
			LiveConnectConfig: runconfig.FromContext(ctx).LiveConnectConfig,
		}

		// Preprocess before calling the LLM.
		for ev, err := range f.preprocess(ctx, req) {
			if err != nil {
				yield(nil, err)
				return
			}
			if ev != nil {
				if !yield(ev, nil) {
					return
				}
			}
		}
		if ctx.Ended() {
			return
		}

		log.Info(ctx, "Flow.RunLive: start")
		attempt := 1
		for {
			// Handle resumption connection
			if handle := ctx.LiveSessionResumptionHandle(); handle != "" {
				log.Info(ctx, "Resuming live session with handle: %s (attempt %d)", handle, attempt)
				attempt += 1

				if req.LiveConnectConfig == nil {
					req.LiveConnectConfig = &genai.LiveConnectConfig{}
				}
				if req.LiveConnectConfig.SessionResumption == nil {
					req.LiveConnectConfig.SessionResumption = &genai.SessionResumptionConfig{}
				}
				req.LiveConnectConfig.SessionResumption.Handle = handle
				req.LiveConnectConfig.SessionResumption.Transparent = true
			}

			if len(req.Config.Tools) > 0 {
				req.LiveConnectConfig.Tools = req.Config.Tools
			}

			genaiSession, err := f.Model.Connect(ctx, req)
			if err != nil {
				yield(nil, err)
				return
			}

			var closeOnce sync.Once
			closeSession := func() {
				closeOnce.Do(func() {
					genaiSession.Close()
				})
			}

			// Ensure session is always closed when this iteration exits.
			defer closeSession()

			err = f.sendHistoryForLiveSession(genaiSession, req)
			if err != nil {
				yield(nil, err)
				return
			}

			// Main loop to receive messages from Gemini Live API session.
			//
			// The receive goroutine processes incoming messages and signals transfer
			// requests back to the main goroutine via transferCh. This ensures the
			// parent's iterator function stays alive during a transfer, so the caller
			// keeps producing messages for the sub-agent's liveRequestQueue.
			type recvResult struct {
				ev  *session.Event
				err error
			}

			recvCh := make(chan recvResult, 1)
			transferAgentCh := make(chan string, 1)
			goroutineDone := make(chan struct{})

			go func() {
				defer close(goroutineDone)
				for {
					select {
					case <-ctx.Done():
						return
					default:
					}

					msg, err := genaiSession.Receive()
					if err != nil {
						fmt.Println("err", err)
						recvCh <- recvResult{err: err}
						return
					}

					llmResponse := liveServerMessageToLLMResponse(msg)
					if llmResponse == nil {
						continue
					}

					if llmResponse.LiveSessionResumptionUpdate != nil {
						ctx.SetLiveSessionResumptionHandle(llmResponse.LiveSessionResumptionUpdate.NewHandle)
					}

					modelResponseEvent := session.NewEvent(ctx.InvocationID())
					modelResponseEvent.Content = llmResponse.Content
					modelResponseEvent.Author = f.getAuthorForEvent(ctx, llmResponse)
					modelResponseEvent.OutputTranscription = llmResponse.OutputTranscription
					modelResponseEvent.InputTranscription = llmResponse.InputTranscription

					for ev, err := range f.postprocessLive(ctx, req, llmResponse, modelResponseEvent) {
						if err != nil {
							recvCh <- recvResult{err: err}
							return
						}

						if ctx.RunConfig().SaveLiveBlob &&
							ev.Content != nil &&
							ev.Content.Parts != nil &&
							ev.Content.Parts[0].InlineData != nil &&
							strings.HasPrefix(ev.Content.Parts[0].InlineData.MIMEType, "audio/") {

							audioBlob := &genai.Blob{
								Data:     ev.Content.Parts[0].InlineData.Data,
								MIMEType: ev.Content.Parts[0].InlineData.MIMEType,
							}

							if err := f.AudioCacheManager.CacheAudio(ctx, audioBlob, "output"); err != nil {
								fmt.Println("Cached Audio Failed")
							}

							fmt.Println("Cached Audio Blob Length", len(audioBlob.Data))
						}

						recvCh <- recvResult{ev: ev}

						if ev != nil && ev.Actions.TransferToAgent != "" {
							fmt.Println("RunLive.TransferToAgent", ev.Actions.TransferToAgent)
							transferAgentCh <- ev.Actions.TransferToAgent
							return
						}
					}

					// TODO: Handle reconnect

					if ctx.Err() != nil {
						return
					}
				}
			}()

			// Main loop: multiplexes sends and receives on the main goroutine.
			// This ensures the iterator function stays alive during transfers,
			// so the caller keeps producing messages for sub-agents.
		sendLoop:
			for {
				liveRequestQueue := ctx.LiveRequestQueue()
				select {
				case <-ctx.Done():
					return

				case result := <-recvCh:
					if result.err != nil {
						yield(nil, result.err)
						return
					}
					if result.ev != nil {
						if !yield(result.ev, nil) {
							return
						}
						// Send tool responses back to Gemini Live API so the model
						// can continue the conversation based on tool results.
						if fnResps := extractFunctionResponses(result.ev); len(fnResps) > 0 {
							if err := genaiSession.SendToolResponse(genai.LiveToolResponseInput{
								FunctionResponses: fnResps,
							}); err != nil {
								yield(nil, err)
								return
							}
						}
					}

				case agentName := <-transferAgentCh:
					// Transfer requested by receive goroutine.
					// Close current session and wait for goroutine to exit.
					closeSession()
					<-goroutineDone

					// Clear the resumption handle: the session it belonged to is now
					// closed, and the next agent has different tools/instructions so
					// it must start a fresh Gemini Live session.
					ctx.SetLiveSessionResumptionHandle("")

					fmt.Printf("Transfer to agent: %s\n", agentName)
					nextAgent := f.agentToRun(ctx, agentName)
					if nextAgent == nil {
						yield(nil, fmt.Errorf("failed to find agent: %s", agentName))
						return
					}

					// Run sub-agent on the main goroutine. The iterator function
					// stays alive, so the caller keeps sending to liveRequestQueue.
					for ev, err := range nextAgent.RunLive(ctx) {
						if !yield(ev, err) || err != nil {
							return
						}
					}
					break sendLoop

				case liveReq, ok := <-liveRequestQueue.Chan():
					if !ok || liveReq.Close {
						return
					}

					if liveReq.Realtime != nil {
						// TODO: Check if need
						if ctx.RunConfig().SaveLiveBlob &&
							liveReq.Realtime.Audio != nil &&
							strings.HasPrefix(liveReq.Realtime.Audio.MIMEType, "audio/") {
							err := f.AudioCacheManager.CacheAudio(ctx, liveReq.Realtime.Audio, "input")
							if err != nil {
								fmt.Println("AudioCacheManager.CacheAudio input error", err)
							}
						}

						err = genaiSession.SendRealtimeInput(*liveReq.Realtime)
						if err != nil {
							yield(nil, err)
							return
						}
					}

					if liveReq.Content != nil {
						// Match Python's _send_to_model: persist user text content
						// to the session so that sub-agents see it after transfer.
						// Skip function responses - they are handled separately.
						isFuncResp := false
						for _, p := range liveReq.Content.Parts {
							if p.FunctionResponse != nil {
								isFuncResp = true
								break
							}
						}
						if !isFuncResp {
							if liveReq.Content.Role == "" {
								liveReq.Content.Role = "user"
							}
							userEvent := session.NewEvent(ctx.InvocationID())
							userEvent.Author = "user"
							userEvent.Branch = ctx.Branch()
							userEvent.LLMResponse = model.LLMResponse{
								Content: liveReq.Content,
							}
							if !yield(userEvent, nil) {
								return
							}
						}

						err := genaiSession.SendClientContent(genai.LiveClientContentInput{
							Turns:        []*genai.Content{liveReq.Content},
							TurnComplete: genai.Ptr(true),
						})
						if err != nil {
							yield(nil, err)
							return
						}
					}
				}
			}
		}
	}
}

func (f *Flow) runOneStep(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		if f.Model == nil {
			yield(nil, fmt.Errorf("agent %q: %w", ctx.Agent().Name(), ErrModelNotConfigured))
			return
		}

		req := &model.LLMRequest{
			Model: f.Model.Name(),
		}

		// Preprocess before calling the LLM.
		for ev, err := range f.preprocess(ctx, req) {
			if err != nil {
				yield(nil, err)
				return
			}
			if ev != nil {
				if !yield(ev, nil) {
					return
				}
			}
		}
		if ctx.Ended() {
			return
		}
		spans := telemetry.StartTrace(ctx, "call_llm")
		// Create event to pass to callback state delta
		stateDelta := make(map[string]any)
		// Calls the LLM.
		for resp, err := range f.callLLM(ctx, req, stateDelta) {
			if err != nil {
				yield(nil, err)
				return
			}
			if err := f.postprocess(ctx, req, resp); err != nil {
				yield(nil, err)
				return
			}
			// Skip the model response event if there is no content and no error code.
			// This is needed for the code executor to trigger another loop according to
			// adk-python src/google/adk/flows/llm_flows/base_llm_flow.py BaseLlmFlow._postprocess_async.
			if resp.Content == nil && resp.ErrorCode == "" && !resp.Interrupted {
				continue
			}

			// TODO: temporarily convert
			tools := make(map[string]tool.Tool)
			for k, v := range req.Tools {
				tool, ok := v.(tool.Tool)
				if !ok {
					if !yield(nil, fmt.Errorf("unexpected tool type %T for tool %v", v, k)) {
						return
					}
				}
				tools[k] = tool
			}

			// Build the event and yield.
			modelResponseEvent := f.finalizeModelResponseEvent(ctx, resp, tools, stateDelta)
			telemetry.TraceLLMCall(spans, ctx, req, modelResponseEvent)
			if !yield(modelResponseEvent, nil) {
				return
			}
			// TODO: generate and yield an auth event if needed.

			// Handle function calls.

			ev, err := f.handleFunctionCalls(ctx, tools, resp, nil)
			if err != nil {
				yield(nil, err)
				return
			}
			if ev == nil {
				// nothing to yield/process.
				continue
			}

			toolConfirmationEvent := generateRequestConfirmationEvent(ctx, modelResponseEvent, ev)
			if toolConfirmationEvent != nil {
				if !yield(toolConfirmationEvent, nil) {
					return
				}
			}

			if !yield(ev, nil) {
				return
			}

			// If the model response is structured, yield it as a final model response event.
			outputSchemaResponse, err := retrieveStructuredModelResponse(ev)
			if err != nil {
				yield(nil, err)
				return
			}
			if outputSchemaResponse != "" {
				if !yield(createFinalModelResponseEvent(ctx, outputSchemaResponse), nil) {
					return
				}
			}
			// Actually handle "transfer_to_agent" tool. The function call sets the ev.Actions.TransferToAgent field.
			// We are following python's execution flow which is
			//   BaseLlmFlow._postprocess_async
			//    -> _postprocess_handle_function_calls_async
			// TODO(hakim): figure out why this isn't handled by the runner.
			if ev.Actions.TransferToAgent == "" {
				return
			}
			nextAgent := f.agentToRun(ctx, ev.Actions.TransferToAgent)
			if nextAgent == nil {
				yield(nil, fmt.Errorf("failed to find agent: %s", ev.Actions.TransferToAgent))
				return
			}
			for ev, err := range nextAgent.Run(ctx) {
				if !yield(ev, err) || err != nil { // forward
					return
				}
			}
		}
	}
}

func (f *Flow) preprocess(ctx agent.InvocationContext, req *model.LLMRequest) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		// apply request processor functions to the request in the configured order.
		for _, processor := range f.RequestProcessors {
			for ev, err := range processor(ctx, req, f) {
				if err != nil {
					yield(nil, err)
					return
				}
				if ev != nil {
					yield(ev, nil)
				}
			}
		}

		if f.Tools != nil {
			if err := toolPreprocess(ctx, req, f.Tools); err != nil {
				yield(nil, err)
			}
		}
	}
}

func (f *Flow) postprocessLive(ctx agent.InvocationContext, llmRequest *model.LLMRequest, llmResponse *model.LLMResponse, modelResponseEvent *session.Event) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		if err := f.postprocess(ctx, llmRequest, llmResponse); err != nil {
			yield(nil, err)
			return
		}

		if llmResponse.Content == nil &&
			llmResponse.ErrorCode == "" &&
			!llmResponse.Interrupted &&
			!llmResponse.TurnComplete &&
			llmResponse.InputTranscription == nil &&
			llmResponse.OutputTranscription == nil &&
			llmResponse.UsageMetadata == nil {
			return
		}

		if llmResponse.InputTranscription != nil {
			modelResponseEvent.InputTranscription = llmResponse.InputTranscription
			modelResponseEvent.Partial = llmResponse.Partial
			yield(modelResponseEvent, nil)
			return
		}

		if llmResponse.OutputTranscription != nil {
			modelResponseEvent.OutputTranscription = llmResponse.OutputTranscription
			modelResponseEvent.Partial = llmResponse.Partial
			yield(modelResponseEvent, nil)
			return
		}

		// Flush audio caches based on control events using configurable settings
		if ctx.RunConfig().SaveLiveBlob {
			flushedEvents := f.handleControlEventFlush(ctx, llmResponse)
			for _, ev := range flushedEvents {
				if !yield(ev, nil) {
					return
				}
			}

			// if len(flushedEvents) > 0 {
			// 	// NOTE below return is O.K. for now, because currently we only flush
			// 	// events on interrupted or turn_complete. turn_complete is a pure
			// 	// control event and interrupted is not with content but those content
			// 	// is ignorable because model is already interrupted. If we have other
			// 	// case to flush events in the future that are not pure control events,
			// 	// we should not return here.
			// 	return
			// }
		}

		// Resolve tools
		tools := make(map[string]tool.Tool)
		for k, v := range llmRequest.Tools {
			tool, ok := v.(tool.Tool)
			if !ok {
				yield(nil, fmt.Errorf("unexpected tool type %T for tool %v", v, k))
				return
			}
			tools[k] = tool
		}

		stateDelta := make(map[string]any)
		newModelResponseEvent := f.finalizeModelResponseEvent(ctx, llmResponse, tools, stateDelta)
		if !yield(newModelResponseEvent, nil) {
			return
		}

		if len(newModelResponseEvent.FunctionCalls()) > 0 {
			functionResponseEvent, err := f.handleFunctionCalls(ctx, tools, llmResponse, nil)
			if err != nil {
				yield(nil, err)
				return
			}
			if functionResponseEvent != nil {
				if !yield(functionResponseEvent, nil) {
					return
				}
			}
		}
	}
}

// toolPreprocess runs tool preprocess on the given request
// If a tool set is encountered, it's expanded recursively in DFS fashion.
// TODO: check need/feasibility of running this concurrently.
func toolPreprocess(ctx agent.InvocationContext, req *model.LLMRequest, tools []tool.Tool) error {
	for _, t := range tools {
		requestProcessor, ok := t.(toolinternal.RequestProcessor)
		if !ok {
			return fmt.Errorf("tool %q does not implement RequestProcessor() method", t.Name())
		}
		// TODO: how to prevent mutation on this?
		toolCtx := toolinternal.NewToolContext(ctx, "", &session.EventActions{}, nil)
		if err := requestProcessor.ProcessRequest(toolCtx, req); err != nil {
			return err
		}
	}
	return nil
}

func (f *Flow) callLLM(ctx agent.InvocationContext, req *model.LLMRequest, stateDelta map[string]any) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		pluginManager := pluginManagerFromContext(ctx)
		if pluginManager != nil {
			cctx := icontext.NewCallbackContextWithDelta(ctx, stateDelta)
			callbackResponse, callbackErr := pluginManager.RunBeforeModelCallback(cctx, req)
			if callbackResponse != nil || callbackErr != nil {
				yield(callbackResponse, callbackErr)
				return
			}
		}

		for _, callback := range f.BeforeModelCallbacks {
			cctx := icontext.NewCallbackContextWithDelta(ctx, stateDelta)
			callbackResponse, callbackErr := callback(cctx, req)

			if callbackResponse != nil || callbackErr != nil {
				yield(callbackResponse, callbackErr)
				return
			}
		}

		// TODO: Set _ADK_AGENT_NAME_LABEL_KEY in req.GenerateConfig.Labels
		// to help with slicing the billing reports on a per-agent basis.

		// TODO: RunLive mode when invocation_context.run_config.support_cfc is true.
		streamingMode := runconfig.FromContext(ctx).StreamingMode
		useStream := streamingMode == runconfig.StreamingModeSSE

		for resp, err := range f.Model.GenerateContent(ctx, req, useStream) {
			if err != nil {
				cbResp, cbErr := f.runOnModelErrorCallbacks(ctx, req, stateDelta, err)
				if cbErr != nil {
					yield(nil, cbErr)
					return
				}
				if cbResp == nil {
					yield(nil, err)
					return
				}
				resp = cbResp
				err = cbErr
			}
			// Function call ID is optional in genai API and some models do not use the field.
			// Set it in case after model callbacks use it.
			utils.PopulateClientFunctionCallID(resp.Content)
			callbackResp, callbackErr := f.runAfterModelCallbacks(ctx, resp, stateDelta, err)
			// TODO: check if we should stop iterator on the first error from stream or continue yielding next results.
			if callbackErr != nil {
				yield(nil, callbackErr)
				return
			}

			if callbackResp != nil {
				if !yield(callbackResp, nil) {
					return
				}
				continue
			}

			// TODO: check if we should stop iterator on the first error from stream or continue yielding next results.
			if err != nil {
				yield(nil, err)
				return
			}

			if !yield(resp, nil) {
				return
			}
		}
	}
}

// liveServerMessageToLLMResponse converts a genai.LiveServerMessage to model.LLMResponse.
func liveServerMessageToLLMResponse(msg *genai.LiveServerMessage) *model.LLMResponse {
	if msg == nil {
		return nil
	}

	resp := &model.LLMResponse{}

	if msg.SessionResumptionUpdate != nil {
		resp.LiveSessionResumptionUpdate = msg.SessionResumptionUpdate
	}

	if msg.ServerContent != nil {
		if msg.ServerContent.ModelTurn != nil {
			resp.Content = msg.ServerContent.ModelTurn
		}
		resp.TurnComplete = msg.ServerContent.TurnComplete
		resp.Interrupted = msg.ServerContent.Interrupted
		resp.InputTranscription = msg.ServerContent.InputTranscription
		resp.OutputTranscription = msg.ServerContent.OutputTranscription
	}

	if msg.ToolCall != nil && len(msg.ToolCall.FunctionCalls) > 0 {
		parts := make([]*genai.Part, 0, len(msg.ToolCall.FunctionCalls))
		for _, fc := range msg.ToolCall.FunctionCalls {
			parts = append(parts, &genai.Part{FunctionCall: fc})
		}
		resp.Content = &genai.Content{
			Parts: parts,
			Role:  genai.RoleModel,
		}
	}

	if msg.UsageMetadata != nil {
		resp.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     msg.UsageMetadata.PromptTokenCount,
			CandidatesTokenCount: msg.UsageMetadata.ResponseTokenCount,
			TotalTokenCount:      msg.UsageMetadata.TotalTokenCount,
		}
	}

	// Skip if no meaningful content
	if resp.Content == nil &&
		!resp.TurnComplete &&
		!resp.Interrupted &&
		resp.UsageMetadata == nil &&
		resp.InputTranscription == nil &&
		resp.OutputTranscription == nil {
		return nil
	}

	return resp
}

// extractFunctionResponses returns FunctionResponse parts from an event, if any.
// Used to send tool results back to the Gemini Live API via SendToolResponse.
func extractFunctionResponses(ev *session.Event) []*genai.FunctionResponse {
	if ev == nil || ev.LLMResponse.Content == nil {
		return nil
	}
	var resps []*genai.FunctionResponse
	for _, part := range ev.LLMResponse.Content.Parts {
		if part.FunctionResponse != nil {
			resps = append(resps, part.FunctionResponse)
		}
	}
	return resps
}

// isAudioPart returns true if the part contains audio data (inline or file).
func isAudioPart(p *genai.Part) bool {
	if p.InlineData != nil && strings.HasPrefix(p.InlineData.MIMEType, "audio/") {
		return true
	}
	if p.FileData != nil && strings.HasPrefix(p.FileData.MIMEType, "audio/") {
		return true
	}
	return false
}

// filterAudioParts returns a copy of the content with audio parts removed.
// Returns nil if no non-audio parts remain.
// This matches Python's filter_audio_parts in content_utils.py.
func filterAudioParts(c *genai.Content) *genai.Content {
	if c == nil || len(c.Parts) == 0 {
		return nil
	}
	var filtered []*genai.Part
	for _, p := range c.Parts {
		if !isAudioPart(p) {
			filtered = append(filtered, p)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return &genai.Content{
		Role:  c.Role,
		Parts: filtered,
	}
}

// sendHistoryForLiveSession is send conversation history so the model has context from previous
// turns (especially important after agent transfer where the new
// agent's Gemini session starts fresh).
// Match Python's send_history: send all contents in one call and set
// turn_complete based on whether the last content is from the user.
// If the last content is a user message, the model responds immediately;
// otherwise it waits for new user input.
func (f *Flow) sendHistoryForLiveSession(genaiSession *genai.Session, req *model.LLMRequest) error {
	if len(req.Contents) == 0 {
		return nil
	}

	// Filter out audio parts from history because:
	// 1. Audio has already been transcribed.
	// 2. Sending audio via SendClientContent is not supported by
	//    the Live API (the session will be corrupted).
	// This matches Python's send_history → filter_audio_parts.
	var filteredContents []*genai.Content
	for _, c := range req.Contents {
		fc := filterAudioParts(c)
		if fc != nil {
			filteredContents = append(filteredContents, fc)
		}
	}

	// Convert FunctionCall/FunctionResponse parts to text summaries.
	// The Live API handles tool interactions via separate ToolCall /
	// SendToolResponse protocol messages, NOT through SendClientContent
	// turns.  Including raw FunctionCall or FunctionResponse parts in the
	// history causes the server to reject the request with "invalid
	// argument".  Converting them to text preserves the conversation
	// context while conforming to the Live API format.
	filteredContents = convertFunctionPartsToText(filteredContents)

	// Merge consecutive same-role contents to maintain the user/model
	// role alternation required by the Gemini Live API. After the
	// FunctionCall→text conversion above, former model FunctionCall
	// turns become user-role text, which may sit next to existing
	// user-role content. Merging keeps strict alternation.
	filteredContents = mergeConsecutiveSameRoleContents(filteredContents)

	if len(filteredContents) > 0 {
		lastRole := filteredContents[len(filteredContents)-1].Role
		turnComplete := lastRole == "user"
		if err := genaiSession.SendClientContent(genai.LiveClientContentInput{
			Turns:        filteredContents,
			TurnComplete: genai.Ptr(turnComplete),
		}); err != nil {
			return err
		}
	}

	return nil
}

// convertFunctionPartsToText rewrites FunctionCall and FunctionResponse parts
// as plain text so they can be sent via SendClientContent. The Live API
// handles tool interactions through dedicated ToolCall/SendToolResponse
// messages; including raw function parts in client content turns is invalid.
// All converted parts are given role "user" so the caller can merge them with
// adjacent user content afterwards.
func convertFunctionPartsToText(contents []*genai.Content) []*genai.Content {
	out := make([]*genai.Content, 0, len(contents))
	for _, c := range contents {
		hasFuncParts := false
		for _, p := range c.Parts {
			if p.FunctionCall != nil || p.FunctionResponse != nil {
				hasFuncParts = true
				break
			}
		}
		if !hasFuncParts {
			out = append(out, c)
			continue
		}
		// Convert: keep text parts as-is, rewrite function parts to text.
		converted := &genai.Content{
			Role: "user",
		}
		for _, p := range c.Parts {
			switch {
			case p.FunctionCall != nil:
				args, _ := json.Marshal(p.FunctionCall.Args)
				converted.Parts = append(converted.Parts, &genai.Part{
					Text: fmt.Sprintf("called tool %q with parameters: %s", p.FunctionCall.Name, string(args)),
				})
			case p.FunctionResponse != nil:
				resp, _ := json.Marshal(p.FunctionResponse.Response)
				converted.Parts = append(converted.Parts, &genai.Part{
					Text: fmt.Sprintf("tool %q returned result: %s", p.FunctionResponse.Name, string(resp)),
				})
			default:
				converted.Parts = append(converted.Parts, p)
			}
		}
		if len(converted.Parts) > 0 {
			out = append(out, converted)
		}
	}
	return out
}

// mergeConsecutiveSameRoleContents combines adjacent contents that share the
// same role into a single content with all their parts concatenated. This
// ensures the strict user/model role alternation required by the Gemini Live
// API's SendClientContent.
func mergeConsecutiveSameRoleContents(contents []*genai.Content) []*genai.Content {
	if len(contents) <= 1 {
		return contents
	}
	merged := make([]*genai.Content, 0, len(contents))
	current := &genai.Content{
		Role:  contents[0].Role,
		Parts: append([]*genai.Part(nil), contents[0].Parts...),
	}
	for _, c := range contents[1:] {
		if c.Role == current.Role {
			current.Parts = append(current.Parts, c.Parts...)
		} else {
			merged = append(merged, current)
			current = &genai.Content{
				Role:  c.Role,
				Parts: append([]*genai.Part(nil), c.Parts...),
			}
		}
	}
	merged = append(merged, current)
	return merged
}

func (f *Flow) runAfterModelCallbacks(ctx agent.InvocationContext, llmResp *model.LLMResponse, stateDelta map[string]any, llmErr error) (*model.LLMResponse, error) {
	pluginManager := pluginManagerFromContext(ctx)
	if pluginManager != nil {
		cctx := icontext.NewCallbackContextWithDelta(ctx, stateDelta)
		callbackResponse, callbackErr := pluginManager.RunAfterModelCallback(cctx, llmResp, llmErr)
		if callbackResponse != nil || callbackErr != nil {
			return callbackResponse, callbackErr
		}
	}

	for _, callback := range f.AfterModelCallbacks {
		cctx := icontext.NewCallbackContextWithDelta(ctx, stateDelta)
		callbackResponse, callbackErr := callback(cctx, llmResp, llmErr)

		if callbackResponse != nil || callbackErr != nil {
			return callbackResponse, callbackErr
		}
	}

	return nil, nil
}

func (f *Flow) runOnModelErrorCallbacks(ctx agent.InvocationContext, llmReq *model.LLMRequest, stateDelta map[string]any, llmErr error) (*model.LLMResponse, error) {
	pluginManager := pluginManagerFromContext(ctx)
	if pluginManager != nil {
		cctx := icontext.NewCallbackContextWithDelta(ctx, stateDelta)
		callbackResponse, callbackErr := pluginManager.RunOnModelErrorCallback(cctx, llmReq, llmErr)
		if callbackResponse != nil || callbackErr != nil {
			return callbackResponse, callbackErr
		}
	}

	for _, callback := range f.OnModelErrorCallbacks {
		cctx := icontext.NewCallbackContextWithDelta(ctx, stateDelta)
		callbackResponse, callbackErr := callback(cctx, llmReq, llmErr)

		if callbackResponse != nil || callbackErr != nil {
			return callbackResponse, callbackErr
		}
	}

	return nil, nil
}

func (f *Flow) postprocess(ctx agent.InvocationContext, req *model.LLMRequest, resp *model.LLMResponse) error {
	// apply response processor functions to the response in the configured order.
	for _, processor := range f.ResponseProcessors {
		if err := processor(ctx, req, resp); err != nil {
			return err
		}
	}
	return nil
}

func (f *Flow) agentToRun(ctx agent.InvocationContext, agentName string) agent.Agent {
	// NOTE: in python, BaseLlmFlow._get_agent_to_run searches the entire agent
	// tree from the root_agent when processing _postprocess_handle_function_calls_async.
	// I think that is strange. In our version, we check the agents included in transferTarget.
	parents := parentmap.FromContext(ctx)
	agents := transferTargets(ctx.Agent(), parents[ctx.Agent().Name()])
	for _, agent := range agents {
		if agent.Name() == agentName {
			return agent
		}
	}
	return nil
}

func (f *Flow) finalizeModelResponseEvent(ctx agent.InvocationContext, resp *model.LLMResponse, tools map[string]tool.Tool, stateDelta map[string]any) *session.Event {
	// FunctionCall & FunctionResponse matching algorithm assumes non-empty function call IDs
	// but function call ID is optional in genai API and some models do not use the field.
	// Generate function call ids. (see functions.populate_client_function_call_id in python SDK)
	utils.PopulateClientFunctionCallID(resp.Content)

	ev := session.NewEvent(ctx.InvocationID())
	ev.Author = ctx.Agent().Name()
	ev.Branch = ctx.Branch()
	ev.LLMResponse = *resp
	ev.Actions.StateDelta = stateDelta

	// Populate ev.LongRunningToolIDs
	ev.LongRunningToolIDs = findLongRunningFunctionCallIDs(resp.Content, tools)

	return ev
}

// findLongRunningFunctionCallIDs iterates over the FunctionCalls and
// returns the callIDs of the long running functions
func findLongRunningFunctionCallIDs(c *genai.Content, tools map[string]tool.Tool) []string {
	set := make(map[string]struct{})
	// Iterate over function calls.
	for _, fc := range utils.FunctionCalls(c) {
		if tool, ok := tools[fc.Name]; ok && fc.ID != "" && tool.IsLongRunning() {
			// If the tool exists and is long-running, add its ID to the set.
			set[fc.ID] = struct{}{}
		}
	}
	// Transform the set (map keys) into a slice.
	return slices.Collect(maps.Keys(set))
}

type fakeTool struct {
	name string
}

func (f *fakeTool) Name() string      { return f.name }
func (*fakeTool) Description() string { return "Tool not found" }
func (*fakeTool) IsLongRunning() bool { return false }

var _ tool.Tool = (*fakeTool)(nil)

// newToolNotFoundError creates an error matching the specific Python format
func newToolNotFoundError(toolName string, availableTools []string) error {
	joinedTools := strings.Join(availableTools, ", ")

	return fmt.Errorf(`tool '%s' not found.
Available tools: %s

Possible causes:
  1. LLM hallucinated the function name - review agent instruction clarity
  2. Tool not registered - verify agent.tools list
  3. Name mismatch - check for typos

Suggested fixes:
  - Review agent instruction to ensure tool usage is clear
  - Verify tool is included in agent.tools list
  - Check for typos in function name`, toolName, joinedTools)
}

// handleFunctionCalls calls the functions and returns the function response event.
//
// TODO: accept filters to include/exclude function calls.
// TODO: check feasibility of running tool.Run concurrently.
func (f *Flow) handleFunctionCalls(ctx agent.InvocationContext, toolsDict map[string]tool.Tool, resp *model.LLMResponse, toolConfirmations map[string]*toolconfirmation.ToolConfirmation) (*session.Event, error) {
	var fnResponseEvents []*session.Event

	fnCalls := utils.FunctionCalls(resp.Content)
	toolNames := slices.Collect(maps.Keys(toolsDict))

	var result map[string]any
	for _, fnCall := range fnCalls {
		var confirmation *toolconfirmation.ToolConfirmation
		if toolConfirmations != nil {
			confirmation = toolConfirmations[fnCall.ID]
		}
		toolCtx := toolinternal.NewToolContext(ctx, fnCall.ID, &session.EventActions{StateDelta: make(map[string]any)}, confirmation)

		spans := telemetry.StartTrace(ctx, "execute_tool "+fnCall.Name)
		curTool, found := toolsDict[fnCall.Name]
		if !found {
			err := newToolNotFoundError(fnCall.Name, toolNames)
			result, err = f.runOnToolErrorCallbacks(toolCtx, &fakeTool{name: fnCall.Name}, fnCall.Args, err)
			if err != nil {
				result = map[string]any{"error": err.Error()}
			}
		} else if funcTool, ok := curTool.(toolinternal.FunctionTool); !ok {
			err := newToolNotFoundError(fnCall.Name, toolNames)
			result, err = f.runOnToolErrorCallbacks(toolCtx, &fakeTool{name: fnCall.Name}, fnCall.Args, err)
			if err != nil {
				result = map[string]any{"error": err.Error()}
			}
		} else {
			result = f.callTool(toolCtx, funcTool, fnCall.Args)
		}

		// TODO: handle long-running tool.
		ev := session.NewEvent(ctx.InvocationID())
		ev.LLMResponse = model.LLMResponse{
			Content: &genai.Content{
				Role: "user",
				Parts: []*genai.Part{
					{
						FunctionResponse: &genai.FunctionResponse{
							ID:       fnCall.ID,
							Name:     fnCall.Name,
							Response: result,
						},
					},
				},
			},
		}
		ev.Author = ctx.Agent().Name()
		ev.Branch = ctx.Branch()
		ev.Actions = *toolCtx.Actions()

		traceTool := curTool
		if traceTool == nil {
			traceTool = &fakeTool{name: fnCall.Name}
		}
		telemetry.TraceToolCall(spans, traceTool, fnCall.Args, ev)

		fnResponseEvents = append(fnResponseEvents, ev)
	}
	mergedEvent, err := mergeParallelFunctionResponseEvents(fnResponseEvents)
	if err != nil {
		return mergedEvent, err
	}
	// this is needed for debug traces of parallel calls
	spans := telemetry.StartTrace(ctx, "execute_tool (merged)")
	telemetry.TraceMergedToolCalls(spans, mergedEvent)
	return mergedEvent, nil
}

func (f *Flow) runOnToolErrorCallbacks(toolCtx tool.Context, tool tool.Tool, fArgs map[string]any, err error) (map[string]any, error) {
	pluginManager := pluginManagerFromContext(toolCtx)
	if pluginManager != nil {
		result, err := pluginManager.RunOnToolErrorCallback(toolCtx, tool, fArgs, err)
		if result != nil || err != nil {
			return result, err
		}
	}
	return f.invokeOnToolErrorCallbacks(toolCtx, tool, fArgs, err)
}

func (f *Flow) callTool(toolCtx tool.Context, tool toolinternal.FunctionTool, fArgs map[string]any) map[string]any {
	var response map[string]any
	var err error
	pluginManager := pluginManagerFromContext(toolCtx)
	if pluginManager != nil {
		response, err = pluginManager.RunBeforeToolCallback(toolCtx, tool, fArgs)
	}
	if response == nil && err == nil {
		response, err = f.invokeBeforeToolCallbacks(toolCtx, tool, fArgs)
	}

	if response == nil && err == nil {
		response, err = tool.Run(toolCtx, fArgs)
	}

	var errorResponse map[string]any
	var cbErr error
	if err != nil && pluginManager != nil {
		errorResponse, cbErr = pluginManager.RunOnToolErrorCallback(toolCtx, tool, fArgs, err)
	}
	if err != nil && errorResponse == nil && cbErr == nil {
		errorResponse, cbErr = f.invokeOnToolErrorCallbacks(toolCtx, tool, fArgs, err)
	}
	if errorResponse != nil || cbErr != nil {
		response = errorResponse
		err = cbErr
	}

	var alteredResponse map[string]any
	var alteredErr error
	if pluginManager != nil {
		alteredResponse, alteredErr = pluginManager.RunAfterToolCallback(toolCtx, tool, fArgs, response, err)
	}
	if alteredResponse == nil && alteredErr == nil {
		alteredResponse, alteredErr = f.invokeAfterToolCallbacks(toolCtx, tool, fArgs, response, err)
	}
	if alteredResponse != nil || alteredErr != nil {
		response = alteredResponse
		err = alteredErr
	}

	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return response
}

func (f *Flow) invokeBeforeToolCallbacks(toolCtx tool.Context, tool tool.Tool, fArgs map[string]any) (map[string]any, error) {
	for _, callback := range f.BeforeToolCallbacks {
		result, err := callback(toolCtx, tool, fArgs)
		if err != nil {
			return nil, err
		}
		// When a list of callbacks is provided, the callbacks will be called in the
		// order they are listed while a callback returns nil.
		if result != nil {
			return result, nil
		}
	}
	return nil, nil
}

func (f *Flow) invokeAfterToolCallbacks(toolCtx tool.Context, tool toolinternal.FunctionTool, fArgs, fResult map[string]any, fErr error) (map[string]any, error) {
	for _, callback := range f.AfterToolCallbacks {
		result, err := callback(toolCtx, tool, fArgs, fResult, fErr)
		if err != nil {
			return nil, err
		}
		// When a list of callbacks is provided, the callbacks will be called in the
		// order they are listed while a callback returns nil.
		if result != nil {
			return result, nil
		}
	}
	// If no callback returned a result/error, return the original result/error.
	return fResult, fErr
}

func (f *Flow) invokeOnToolErrorCallbacks(toolCtx tool.Context, tool tool.Tool, fArgs map[string]any, fErr error) (map[string]any, error) {
	for _, callback := range f.OnToolErrorCallbacks {
		result, err := callback(toolCtx, tool, fArgs, fErr)
		if err != nil {
			return nil, err
		}
		// When a list of callbacks is provided, the callbacks will be called in the
		// order they are listed while a callback returns nil.
		if result != nil {
			return result, nil
		}
	}
	// If no callback returned a result/error, return the original result/error.
	return nil, fErr
}

func mergeParallelFunctionResponseEvents(events []*session.Event) (*session.Event, error) {
	switch len(events) {
	case 0:
		return nil, nil
	case 1:
		return events[0], nil
	}
	var parts []*genai.Part
	var actions *session.EventActions
	for _, ev := range events {
		if ev == nil || ev.LLMResponse.Content == nil {
			continue
		}
		parts = append(parts, ev.LLMResponse.Content.Parts...)
		actions = mergeEventActions(actions, &ev.Actions)
	}
	// reuse events[0]
	ev := events[0]
	ev.LLMResponse = model.LLMResponse{
		Content: &genai.Content{
			Role:  "user",
			Parts: parts,
		},
	}
	ev.Actions = *actions
	return ev, nil
}

func mergeEventActions(base, other *session.EventActions) *session.EventActions {
	// flows/llm_flows/functions.py merge_parallel_function_response_events
	if other == nil {
		return base
	}
	if base == nil {
		return other
	}
	if other.SkipSummarization {
		base.SkipSummarization = true
	}
	if other.TransferToAgent != "" {
		base.TransferToAgent = other.TransferToAgent
	}
	if other.Escalate {
		base.Escalate = true
	}
	if other.StateDelta != nil {
		base.StateDelta = deepMergeMap(base.StateDelta, other.StateDelta)
	}
	// TODO add similar logic for state
	if other.RequestedToolConfirmations != nil {
		if base.RequestedToolConfirmations == nil {
			base.RequestedToolConfirmations = make(map[string]toolconfirmation.ToolConfirmation)
		}
		maps.Copy(base.RequestedToolConfirmations, other.RequestedToolConfirmations)
	}
	return base
}

// handleControlEventFlush flushes audio caches based on control events using configurable settings
func (f *Flow) handleControlEventFlush(ctx agent.InvocationContext, llmResponse *model.LLMResponse) []*session.Event {
	if llmResponse.Interrupted {
		events, err := f.AudioCacheManager.FlushCaches(ctx, false, true)
		if err != nil {
			fmt.Println("failed to flush audio caches")
		}
		return events
	} else if llmResponse.TurnComplete {
		events, err := f.AudioCacheManager.FlushCaches(ctx, true, true)
		if err != nil {
			fmt.Println("failed to flush audio caches")
		}
		return events
	}

	// TODO: Once generation_complete is surfaced on LlmResponse, we can flush
	// model audio here (flush_user_audio=False, flush_model_audio=True).
	return nil
}

func (f *Flow) getAuthorForEvent(ctx agent.InvocationContext, llmResponse *model.LLMResponse) string {
	if llmResponse != nil && llmResponse.Content != nil && llmResponse.Content.Role == "user" {
		return "user"
	}

	return ctx.Agent().Name()
}

func deepMergeMap(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = make(map[string]any)
	}
	for key, value := range src {
		if srcMap, ok := value.(map[string]any); ok {
			if dstMap, ok := dst[key].(map[string]any); ok {
				dst[key] = deepMergeMap(dstMap, srcMap)
				continue
			}
		}
		dst[key] = value
	}
	return dst
}

func pluginManagerFromContext(ctx context.Context) pluginManager {
	m, ok := ctx.Value(plugincontext.PluginManagerCtxKey).(pluginManager)
	if !ok {
		return nil
	}
	return m
}

type pluginManager interface {
	RunBeforeModelCallback(cctx agent.CallbackContext, llmRequest *model.LLMRequest) (*model.LLMResponse, error)
	RunAfterModelCallback(cctx agent.CallbackContext, llmResponse *model.LLMResponse, llmResponseError error) (*model.LLMResponse, error)
	RunOnModelErrorCallback(ctx agent.CallbackContext, llmRequest *model.LLMRequest, llmResponseError error) (*model.LLMResponse, error)
	RunBeforeToolCallback(ctx tool.Context, t tool.Tool, args map[string]any) (map[string]any, error)
	RunAfterToolCallback(ctx tool.Context, t tool.Tool, args, result map[string]any, err error) (map[string]any, error)
	RunOnToolErrorCallback(ctx tool.Context, t tool.Tool, args map[string]any, err error) (map[string]any, error)
}
