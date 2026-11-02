// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/internal/llminternal/googlellm"
	"google.golang.org/adk/model"
)

// responseTriggerText is a minimal placeholder text sent to Gemini 3.x Live to
// trigger a response to history that was just replayed. See SendHistory.
const responseTriggerText = "."

// Connect establishes a bidirectional streaming connection to the model.
func (m *geminiModel) Connect(ctx context.Context, req *model.LLMRequest) (model.LiveConnection, error) {
	session, err := m.client.Live.Connect(ctx, m.name, req.LiveConnectConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to live model: %w", err)
	}

	return &liveConnection{
		session:   session,
		modelName: m.name,
		backend:   m.GetGoogleLLMVariant(),
	}, nil
}

type liveConnection struct {
	session   *genai.Session
	modelName string
	backend   genai.Backend

	inputTranscriptionText  string
	outputTranscriptionText string
}

// isGemini38Live returns true if the model is a Gemini 3.8 Live model.
func isGemini38Live(modelName string) bool {
	if i := strings.LastIndex(modelName, "/"); i >= 0 {
		modelName = modelName[i+1:]
	}
	return strings.HasPrefix(modelName, "gemini-3.8-live")
}

func shouldConvertFileDataPartsToText(modelName string, backend genai.Backend) bool {
	return backend == genai.BackendVertexAI && isGemini38Live(modelName)
}

func (c *liveConnection) SendHistory(contents []*genai.Content) error {
	if len(contents) == 0 {
		return nil
	}

	// Filter out audio parts from history because:
	// 1. Audio has already been transcribed.
	// 2. Sending audio via SendClientContent is not supported by
	//    the Live API (the session will be corrupted).
	// This matches Python's send_history → filter_audio_parts.
	var filteredContents []*genai.Content
	for _, c := range contents {
		fc := filterAudioParts(c)
		if fc != nil {
			filteredContents = append(filteredContents, fc)
		}
	}

	// Python decides turn_complete from the media-filtered history. Decide it
	// here, before convertFunctionPartsToText below rewrites model
	// FunctionCall turns to role "user".
	turnComplete := len(filteredContents) > 0 &&
		filteredContents[len(filteredContents)-1].Role == "user"

	// Strip ThoughtSignature (and the Thought marker) from history parts.
	// A thought signature is only valid when replayed within the live
	// session that issued it (or via session resumption); echoing one
	// from an unrelated or already-closed connection into
	// SendClientContent causes the server to reject the request with
	// "invalid argument" (websocket close 1007).
	filteredContents = stripThoughtSignatures(filteredContents)

	// Vertex AI currently rejects FileData parts in history for Gemini 3.8
	// Live with an internal error. Convert only those parts to text while
	// preserving their metadata as a temporary compatibility workaround.
	// Gemini API accepts FileData history, so leave it unchanged there.
	if shouldConvertFileDataPartsToText(c.modelName, c.backend) {
		filteredContents = convertFileDataPartsToText(filteredContents)
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

	filteredContents = filterEmptyParts(filteredContents)

	if len(filteredContents) > 0 {
		err := c.session.SendClientContent(genai.LiveClientContentInput{
			Turns:        filteredContents,
			TurnComplete: genai.Ptr(turnComplete),
		})
		if err != nil {
			return err
		}

		isGemini3xLive := googlellm.IsGemini3xLive(c.modelName)
		if turnComplete && isGemini3xLive {
			return c.session.SendRealtimeInput(genai.LiveSendRealtimeInputParameters{
				Text: responseTriggerText,
			})
		}
	}

	return nil
}

func (c *liveConnection) SendContent(content *genai.Content) error {
	if content != nil {
		if len(content.Parts) == 0 {
			return fmt.Errorf("content has no parts")
		}
		if allFunctionResponses(content.Parts) {
			var functionResponses []*genai.FunctionResponse
			for _, part := range content.Parts {
				if part.FunctionResponse != nil {
					functionResponses = append(functionResponses, part.FunctionResponse)
				}
			}
			return c.session.SendToolResponse(genai.LiveToolResponseInput{
				FunctionResponses: functionResponses,
			})
		} else {
			if googlellm.IsGemini3xLive(c.modelName) && len(content.Parts) == 1 && content.Parts[0].Text != "" {
				return c.session.SendRealtimeInput(genai.LiveSendRealtimeInputParameters{
					Text: content.Parts[0].Text,
				})
			}
			turnComplete := true
			return c.session.SendClientContent(genai.LiveClientContentInput{
				Turns:        []*genai.Content{content},
				TurnComplete: &turnComplete,
			})
		}
	}

	return nil
}

// allFunctionResponses reports whether every part is a function response.
func allFunctionResponses(parts []*genai.Part) bool {
	for _, p := range parts {
		if p == nil || p.FunctionResponse == nil {
			return false
		}
	}
	return true
}

func (c *liveConnection) SendRealtime(input *genai.LiveRealtimeInput) error {
	if input == nil {
		return nil
	}

	if input.Media != nil {
		if googlellm.IsGemini3xLive(c.modelName) || googlellm.IsGemini35LiveTranslate(c.modelName) {
			if strings.HasPrefix(input.Media.MIMEType, "audio/") {
				return c.session.SendRealtimeInput(genai.LiveSendRealtimeInputParameters{
					Audio: input.Media,
				})
			} else if strings.HasPrefix(input.Media.MIMEType, "image/") {
				return c.session.SendRealtimeInput(genai.LiveSendRealtimeInputParameters{
					Video: input.Media,
				})
			}
			// Unknown or empty MIME type — skip (matches Python warning behavior).
			return nil
		}
		return c.session.SendRealtimeInput(genai.LiveSendRealtimeInputParameters{
			Media: input.Media,
		})
	}

	if input.Audio != nil {
		return c.session.SendRealtimeInput(genai.LiveSendRealtimeInputParameters{
			Audio: input.Audio,
		})
	}

	if input.Video != nil {
		return c.session.SendRealtimeInput(genai.LiveSendRealtimeInputParameters{
			Video: input.Video,
		})
	}

	if input.ActivityStart != nil {
		return c.session.SendRealtimeInput(genai.LiveSendRealtimeInputParameters{
			ActivityStart: input.ActivityStart,
		})
	}

	if input.ActivityEnd != nil {
		return c.session.SendRealtimeInput(genai.LiveSendRealtimeInputParameters{
			ActivityEnd: input.ActivityEnd,
		})
	}

	if input.AudioStreamEnd {
		return c.session.SendRealtimeInput(genai.LiveSendRealtimeInputParameters{
			AudioStreamEnd: true,
		})
	}

	return nil
}

func (c *liveConnection) receive(ctx context.Context) (<-chan *genai.LiveServerMessage, <-chan error) {
	out := make(chan *genai.LiveServerMessage, 100)
	errChan := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errChan)
		for {
			msg, err := c.session.Receive()
			if err != nil {
				// We don't use the helper for errChan since it's buffered(1) and we return immediately
				select {
				case errChan <- err:
				case <-ctx.Done():
				}
				return
			}
			select {
			case out <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, errChan
}

func (c *liveConnection) process(ctx context.Context, in <-chan *genai.LiveServerMessage) (<-chan *model.LLMResponse, <-chan error) {
	out := make(chan *model.LLMResponse, 100)
	errChan := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(errChan)

		send := func(resp *model.LLMResponse) bool {
			select {
			case out <- resp:
				return true
			case <-ctx.Done():
				return false
			}
		}

		isGemini3xLive := googlellm.IsGemini3xLive(c.modelName)

		var text string
		var isThought bool
		var toolCallParts []*genai.Part
		var lastGroundingMetadata *genai.GroundingMetadata
		var toolCallMetadata *genai.GroundingMetadata
		for {
			select {
			case msg, ok := <-in:
				if !ok {
					if len(toolCallParts) > 0 {
						send(&model.LLMResponse{
							Content: &genai.Content{Role: "model", Parts: toolCallParts},
						})
					}
					return
				}

				if msg.UsageMetadata != nil {
					// Remap live token usage to GenerateContentResponse usage metadata.
					// The live API names output tokens response_token_count /
					// response_tokens_details.
					if !send(&model.LLMResponse{
						UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
							CacheTokensDetails:         msg.UsageMetadata.CacheTokensDetails,
							CachedContentTokenCount:    msg.UsageMetadata.CachedContentTokenCount,
							CandidatesTokenCount:       msg.UsageMetadata.ResponseTokenCount,
							CandidatesTokensDetails:    msg.UsageMetadata.ResponseTokensDetails,
							PromptTokenCount:           msg.UsageMetadata.PromptTokenCount,
							PromptTokensDetails:        msg.UsageMetadata.PromptTokensDetails,
							ThoughtsTokenCount:         msg.UsageMetadata.ThoughtsTokenCount,
							ToolUsePromptTokenCount:    msg.UsageMetadata.ToolUsePromptTokenCount,
							ToolUsePromptTokensDetails: msg.UsageMetadata.ToolUsePromptTokensDetails,
							TotalTokenCount:            msg.UsageMetadata.TotalTokenCount,
							TrafficType:                msg.UsageMetadata.TrafficType,
						},
					}) {
						return
					}
				}

				if msg.ServerContent != nil {
					content := msg.ServerContent.ModelTurn
					groundingMetadata := msg.ServerContent.GroundingMetadata
					if groundingMetadata != nil {
						lastGroundingMetadata = mergeGroundingMetadata(lastGroundingMetadata, groundingMetadata)
					}

					hasParts := content != nil && len(content.Parts) > 0

					// Standalone grounding_metadata event (when content is empty)
					if !hasParts && groundingMetadata != nil && !msg.ServerContent.TurnComplete {
						if !send(&model.LLMResponse{
							GroundingMetadata:  groundingMetadata,
							Interrupted:        msg.ServerContent.Interrupted,
							TurnCompleteReason: msg.ServerContent.TurnCompleteReason,
						}) {
							return
						}
					}

					if hasParts {
						resp := &model.LLMResponse{
							Content:            content,
							Interrupted:        msg.ServerContent.Interrupted,
							TurnCompleteReason: msg.ServerContent.TurnCompleteReason,
						}
						// grounding_metadata is sent again at turn_complete, so avoid
						// duplicating it here if turn_complete is true.
						if !msg.ServerContent.TurnComplete && groundingMetadata != nil {
							resp.GroundingMetadata = groundingMetadata
						}
						willFlush := msg.ServerContent.TurnComplete ||
							msg.ServerContent.Interrupted ||
							msg.ToolCall != nil
						flushedParts := make(map[*genai.Part]bool)
						var accumulatedParts []*genai.Part
						markFlushed := func() {
							for _, p := range accumulatedParts {
								flushedParts[p] = true
							}
							accumulatedParts = nil
						}
						for _, part := range content.Parts {
							if part == nil {
								continue
							}
							if part.Text != "" {
								currentIsThought := part.Thought
								if text != "" && currentIsThought != isThought {
									if !send(c.buildFullTextResponse(text, isThought, nil, false)) {
										return
									}
									text = ""
									isThought = false
									markFlushed()
								}

								text += part.Text
								isThought = currentIsThought
								resp.Partial = true
								accumulatedParts = append(accumulatedParts, part)
							} else if text != "" && part.InlineData == nil {
								// don't send the merged text event when receiving audio data
								if !send(c.buildFullTextResponse(text, isThought, lastGroundingMetadata, false)) {
									return
								}
								text = ""
								isThought = false
								lastGroundingMetadata = nil
								markFlushed()
							}
						}
						if willFlush {
							markFlushed()
						}
						responseContent := content
						if len(flushedParts) > 0 {
							responseContent = &genai.Content{Role: content.Role}
							for _, p := range content.Parts {
								if !flushedParts[p] {
									responseContent.Parts = append(responseContent.Parts, p)
								}
							}
							resp.Content = responseContent
						}
						if len(responseContent.Parts) > 0 {
							if !send(resp) {
								return
							}
						}
					}

					// Note: in some cases, tool_call may arrive before
					// generation_complete, causing transcription to appear after
					// tool_call in the session log.
					if msg.ServerContent.InputTranscription != nil {
						if isGemini3xLive {
							// Gemini 3.x Live only sends a single final input
							// transcription.
							if msg.ServerContent.InputTranscription.Text != "" {
								if !send(&model.LLMResponse{
									InputTranscription: &genai.Transcription{
										Text:     msg.ServerContent.InputTranscription.Text,
										Finished: true,
									},
									Partial: false,
								}) {
									return
								}
							}
						} else {
							if msg.ServerContent.InputTranscription.Text != "" {
								c.inputTranscriptionText += msg.ServerContent.InputTranscription.Text
								if !send(&model.LLMResponse{
									InputTranscription: &genai.Transcription{
										Text:     msg.ServerContent.InputTranscription.Text,
										Finished: false,
									},
									Partial: true,
								}) {
									return
								}
							}

							// finished=True and partial transcription may happen in the same
							// message.
							if msg.ServerContent.InputTranscription.Finished {
								if !send(&model.LLMResponse{
									InputTranscription: &genai.Transcription{
										Text:     c.inputTranscriptionText,
										Finished: true,
									},
									Partial: false,
								}) {
									return
								}
								c.inputTranscriptionText = ""
							}
						}
					}
					if msg.ServerContent.OutputTranscription != nil {
						if msg.ServerContent.OutputTranscription.Text != "" {
							c.outputTranscriptionText += msg.ServerContent.OutputTranscription.Text
							if !send(&model.LLMResponse{
								OutputTranscription: &genai.Transcription{
									Text:     msg.ServerContent.OutputTranscription.Text,
									Finished: false,
								},
								Partial: true,
							}) {
								return
							}
						}

						// finished=True and partial transcription may happen in the same
						// message.
						if msg.ServerContent.OutputTranscription.Finished {
							if !send(&model.LLMResponse{
								OutputTranscription: &genai.Transcription{
									Text:     c.outputTranscriptionText,
									Finished: true,
								},
								Partial: false,
							}) {
								return
							}
							c.outputTranscriptionText = ""
						}
					}

					// The Gemini API might not send a transcription finished signal.
					// Instead, we rely on generation_complete, turn_complete or
					// interrupted signals to flush any pending transcriptions.
					if msg.ServerContent.Interrupted ||
						msg.ServerContent.TurnComplete ||
						msg.ServerContent.GenerationComplete {

						if c.inputTranscriptionText != "" {
							if !send(&model.LLMResponse{
								InputTranscription: &genai.Transcription{
									Text:     c.inputTranscriptionText,
									Finished: true,
								},
								Partial: false,
							}) {
								return
							}
							c.inputTranscriptionText = ""
						}

						if c.outputTranscriptionText != "" {
							if !send(&model.LLMResponse{
								OutputTranscription: &genai.Transcription{
									Text:     c.outputTranscriptionText,
									Finished: true,
								},
								Partial: false,
							}) {
								return
							}
							c.outputTranscriptionText = ""
						}

					}
					if msg.ServerContent.TurnComplete {
						if text != "" {
							if !send(c.buildFullTextResponse(text, isThought, lastGroundingMetadata, msg.ServerContent.Interrupted)) {
								return
							}
							text = ""
							isThought = false
							lastGroundingMetadata = nil
						}
						if len(toolCallParts) > 0 {
							if !send(&model.LLMResponse{
								Content:           &genai.Content{Role: "model", Parts: toolCallParts},
								GroundingMetadata: toolCallMetadata,
							}) {
								return
							}
							toolCallParts = nil
							if toolCallMetadata != nil {
								lastGroundingMetadata = nil
							}
							toolCallMetadata = nil
						}

						// If lastGroundingMetadata was cleared in the full text response,
						// avoid duplicating it here.
						finalGroundingMetadata := groundingMetadata
						if finalGroundingMetadata == nil {
							finalGroundingMetadata = lastGroundingMetadata
						}
						if finalGroundingMetadata == nil && isGemini3xLive {
							finalGroundingMetadata = &genai.GroundingMetadata{}
						}
						if !send(&model.LLMResponse{
							TurnComplete:       true,
							Interrupted:        msg.ServerContent.Interrupted,
							GroundingMetadata:  finalGroundingMetadata,
							TurnCompleteReason: msg.ServerContent.TurnCompleteReason,
							InteractionStatus:  msg.ServerContent.InteractionStatus,
						}) {
							return
						}
						lastGroundingMetadata = nil
						continue
					}
					// in case of empty content or parts, we sill surface it
					// in case it's an interrupted message, we merge the previous partial
					// text. Other we don't merge. because content can be none when model
					// safety threshold is triggered
					if msg.ServerContent.Interrupted {
						if text != "" {
							if !send(c.buildFullTextResponse(text, isThought, lastGroundingMetadata, true)) {
								return
							}
							text = ""
							isThought = false
							lastGroundingMetadata = nil
						} else {
							if !send(&model.LLMResponse{
								Interrupted:       msg.ServerContent.Interrupted,
								GroundingMetadata: lastGroundingMetadata,
							}) {
								return
							}
							lastGroundingMetadata = nil
						}
					}
				}

				if msg.ToolCall != nil {
					if text != "" {
						if !send(c.buildFullTextResponse(text, isThought, lastGroundingMetadata, false)) {
							return
						}
						text = ""
						isThought = false
						lastGroundingMetadata = nil
					}
					for _, fc := range msg.ToolCall.FunctionCalls {
						toolCallParts = append(toolCallParts, &genai.Part{FunctionCall: fc})
					}
					if !isGemini3xLive && toolCallMetadata == nil {
						toolCallMetadata = lastGroundingMetadata
					}
					// Gemini 3.x Live does not emit turn_complete until it receives the
					// tool response, so yield tool calls immediately to avoid
					// deadlocking the conversation. Other models (e.g. 2.5-pro,
					// native-audio) send turn_complete after tool calls, so buffer
					// and merge them into a single response at turn_complete.
					if isGemini3xLive && len(toolCallParts) > 0 {
						if !send(&model.LLMResponse{
							Content:           &genai.Content{Role: "model", Parts: toolCallParts},
							GroundingMetadata: lastGroundingMetadata,
						}) {
							return
						}
						toolCallParts = nil
						lastGroundingMetadata = nil
					}
				}

				if msg.SessionResumptionUpdate != nil {
					if !send(&model.LLMResponse{
						LiveSessionResumptionUpdate: msg.SessionResumptionUpdate,
					}) {
						return
					}
				}

				if msg.VoiceActivity != nil {
					if !send(&model.LLMResponse{
						VoiceActivity: msg.VoiceActivity,
					}) {
						return
					}
				}

				if msg.GoAway != nil {
					if !send(&model.LLMResponse{
						LiveGoAway: msg.GoAway,
					}) {
						return
					}
				}
			case <-ctx.Done():
				// errChan <- ctx.Err()
				return
			}
		}
	}()
	return out, errChan
}

func (c *liveConnection) buildFullTextResponse(text string, isThought bool, groundingMetadata *genai.GroundingMetadata, interrupted bool) *model.LLMResponse {
	part := genai.NewPartFromText(text)
	if isThought {
		part.Thought = true
	}
	return &model.LLMResponse{
		Content: &genai.Content{
			Role:  "model",
			Parts: []*genai.Part{part},
		},
		GroundingMetadata: groundingMetadata,
		Interrupted:       interrupted,
		Partial:           false,
	}
}

// mergeGroundingMetadata merges two GroundingMetadata instances, accumulating
// list fields. Mirrors Python's GeminiLlmConnection._merge_grounding_metadata,
// which works on the dumped (JSON) form of both values.
func mergeGroundingMetadata(existing, newMetadata *genai.GroundingMetadata) *genai.GroundingMetadata {
	if existing == nil {
		return newMetadata
	}
	if newMetadata == nil {
		return existing
	}
	existingData, err := groundingMetadataToMap(existing)
	if err != nil {
		return newMetadata
	}
	newData, err := groundingMetadataToMap(newMetadata)
	if err != nil {
		return existing
	}

	// Get offset from existing grounding chunks for shifting support indices
	existingChunks, _ := existingData["groundingChunks"].([]any)
	chunkOffset := len(existingChunks)

	for key, val := range newData {
		list, isList := val.([]any)
		switch {
		case isList && allStrings(list):
			existingList, _ := existingData[key].([]any)
			for _, item := range list {
				if !containsString(existingList, item.(string)) {
					existingList = append(existingList, item)
				}
			}
			existingData[key] = existingList
		case key == "groundingChunks":
			chunks, _ := existingData["groundingChunks"].([]any)
			existingData["groundingChunks"] = append(chunks, list...)
		case key == "groundingSupports":
			supports, _ := existingData["groundingSupports"].([]any)
			for _, s := range list {
				support, ok := s.(map[string]any)
				if ok {
					indices, _ := support["groundingChunkIndices"].([]any)
					for i, idx := range indices {
						n, isNumber := idx.(float64)
						if isNumber {
							indices[i] = n + float64(chunkOffset)
						}
					}
				}
				supports = append(supports, s)
			}
			existingData["groundingSupports"] = supports
		default:
			existingData[key] = val
		}
	}

	b, err := json.Marshal(existingData)
	if err != nil {
		return newMetadata
	}
	merged := &genai.GroundingMetadata{}
	err = json.Unmarshal(b, merged)
	if err != nil {
		return newMetadata
	}
	return merged
}

func groundingMetadataToMap(m *genai.GroundingMetadata) (map[string]any, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	data := map[string]any{}
	err = json.Unmarshal(b, &data)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func allStrings(list []any) bool {
	for _, v := range list {
		_, ok := v.(string)
		if !ok {
			return false
		}
	}
	return true
}

func containsString(list []any, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func (c *liveConnection) Receive(ctx context.Context) (<-chan *model.LLMResponse, <-chan error) {
	msgs, errs1 := c.receive(ctx)
	resps, errs2 := c.process(ctx, msgs)

	errChan := make(chan error, 1)
	go func() {
		defer close(errChan)
		for {
			select {
			case err, ok := <-errs1:
				if ok && err != nil {
					select {
					case errChan <- err:
					case <-ctx.Done():
					}
					return
				}
				if !ok {
					errs1 = nil
				}
			case err, ok := <-errs2:
				if ok && err != nil {
					select {
					case errChan <- err:
					case <-ctx.Done():
					}
					return
				}
				if !ok {
					errs2 = nil
				}
			case <-ctx.Done():
				return
			}

			if errs1 == nil && errs2 == nil {
				return
			}
		}
	}()

	return resps, errChan
}

func (c *liveConnection) Close() error {
	return c.session.Close()
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

// thought signature return from sse mode.
// live mode will return error when we send thought signature.
// for support case same session but switch from sse to live mode, we need strip thought signature from history before send to live mode.
func stripThoughtSignatures(contents []*genai.Content) []*genai.Content {
	out := make([]*genai.Content, len(contents))
	for i, c := range contents {
		if c == nil {
			continue
		}
		parts := make([]*genai.Part, len(c.Parts))
		for j, p := range c.Parts {
			if p == nil || (p.ThoughtSignature == nil && !p.Thought) {
				parts[j] = p
				continue
			}
			stripped := *p
			stripped.ThoughtSignature = nil
			stripped.Thought = false
			parts[j] = &stripped
		}
		out[i] = &genai.Content{Role: c.Role, Parts: parts}
	}
	return out
}

// filterEmptyParts removes nil parts and parts without data after signature
// stripping. Empty contents are omitted; the original history is untouched.
func filterEmptyParts(contents []*genai.Content) []*genai.Content {
	out := make([]*genai.Content, 0, len(contents))
	for _, c := range contents {
		if c == nil {
			continue
		}
		parts := make([]*genai.Part, 0, len(c.Parts))
		for _, p := range c.Parts {
			if p == nil {
				continue
			}
			if p.Text == "" && p.InlineData == nil && p.FileData == nil &&
				p.FunctionCall == nil && p.FunctionResponse == nil &&
				p.ExecutableCode == nil && p.CodeExecutionResult == nil &&
				p.ToolCall == nil && p.ToolResponse == nil {
				continue
			}
			parts = append(parts, p)
		}
		if len(parts) > 0 {
			out = append(out, &genai.Content{Role: c.Role, Parts: parts})
		}
	}
	return out
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
		if p != nil && !isAudioPart(p) {
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

// convertFileDataPartsToText rewrites FileData parts as plain text while
// retaining the file metadata. Original contents and parts are left untouched.
func convertFileDataPartsToText(contents []*genai.Content) []*genai.Content {
	out := make([]*genai.Content, 0, len(contents))
	for _, c := range contents {
		hasFileData := false
		for _, p := range c.Parts {
			if p != nil && p.FileData != nil {
				hasFileData = true
				break
			}
		}
		if !hasFileData {
			out = append(out, c)
			continue
		}

		converted := &genai.Content{Role: c.Role}
		for _, p := range c.Parts {
			if p == nil || p.FileData == nil {
				converted.Parts = append(converted.Parts, p)
				continue
			}

			fileData, _ := json.Marshal(p.FileData)
			converted.Parts = append(converted.Parts, &genai.Part{
				Text: fmt.Sprintf("file data: %s", string(fileData)),
			})
		}
		out = append(out, converted)
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
