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

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// Connect establishes a bidirectional streaming connection to the model.
func (m *geminiModel) Connect(ctx context.Context, req *model.LLMRequest) (model.LiveConnection, error) {
	session, err := m.client.Live.Connect(ctx, m.name, req.LiveConnectConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to live model: %w", err)
	}

	return &liveConnection{
		session: session,
	}, nil
}

type liveConnection struct {
	session *genai.Session

	inputTranscriptionText  string
	outputTranscriptionText string
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
		if err := c.session.SendClientContent(genai.LiveClientContentInput{
			Turns:        filteredContents,
			TurnComplete: genai.Ptr(turnComplete),
		}); err != nil {
			return err
		}
	}

	return nil
}

func (c *liveConnection) SendContent(content *genai.Content) error {
	if content != nil {
		if content.Parts != nil && content.Parts[0].FunctionResponse != nil {
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
			turnComplete := true
			return c.session.SendClientContent(genai.LiveClientContentInput{
				Turns:        []*genai.Content{content},
				TurnComplete: &turnComplete,
			})
		}
	}

	return nil
}

func (c *liveConnection) SendRealtime(input *genai.LiveRealtimeInput) error {
	if input == nil {
		return nil
	}

	if input.Media != nil {
		return c.session.SendRealtimeInput(genai.LiveSendRealtimeInputParameters{
			Media: input.Media,
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

		var text string
		for {
			select {
			case msg, ok := <-in:
				if !ok {
					return
				}

				if msg.UsageMetadata != nil {
					if !send(&model.LLMResponse{
						UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
							CacheTokensDetails:         msg.UsageMetadata.CacheTokensDetails,
							CachedContentTokenCount:    msg.UsageMetadata.CachedContentTokenCount,
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
					if content != nil && len(content.Parts) > 0 {
						resp := &model.LLMResponse{
							Content:     content,
							Interrupted: msg.ServerContent.Interrupted,
						}
						if content.Parts[0].Text != "" {
							text += content.Parts[0].Text
							resp.Partial = true
						} else if text != "" && content.Parts[0].InlineData == nil {
							if !send(c.buildFullTextResponse(text)) {
								return
							}
							text = ""
						}
						if !send(resp) {
							return
						}
					}

					// Note: in some cases, tool_call may arrive before
					// generation_complete, causing transcription to appear after
					// tool_call in the session log.
					if msg.ServerContent.InputTranscription != nil {
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
							if !send(c.buildFullTextResponse(text)) {
								return
							}
							text = ""
						}
						if !send(&model.LLMResponse{
							TurnComplete: true,
							Interrupted:  msg.ServerContent.Interrupted,
						}) {
							return
						}
						continue
					}
					// in case of empty content or parts, we sill surface it
					// in case it's an interrupted message, we merge the previous partial
					// text. Other we don't merge. because content can be none when model
					// safety threshold is triggered
					if msg.ServerContent.Interrupted {
						if text != "" {
							if !send(c.buildFullTextResponse(text)) {
								return
							}
							text = ""
						} else {
							if !send(&model.LLMResponse{
								Interrupted: msg.ServerContent.Interrupted,
							}) {
								return
							}
						}
					}
				}

				if msg.ToolCall != nil {
					resp := &model.LLMResponse{}
					// Map ToolCall to model.LLMResponse content parts
					parts := make([]*genai.Part, 0)
					for _, fc := range msg.ToolCall.FunctionCalls {
						parts = append(parts, &genai.Part{
							FunctionCall: fc,
						})
					}
					if resp.Content == nil {
						resp.Content = &genai.Content{Role: "model"}
					}
					resp.Content.Parts = append(resp.Content.Parts, parts...)
					if !send(resp) {
						return
					}
				}

				if msg.SessionResumptionUpdate != nil && msg.SessionResumptionUpdate.NewHandle != "" {
					if !send(&model.LLMResponse{
						LiveSessionResumptionUpdate: msg.SessionResumptionUpdate,
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

func (c *liveConnection) buildFullTextResponse(text string) *model.LLMResponse {
	return &model.LLMResponse{
		Content: &genai.Content{
			Role: "model",
			Parts: []*genai.Part{
				genai.NewPartFromText(text),
			},
		},
	}
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
