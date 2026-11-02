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
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/model"
)

func runProcess(t *testing.T, modelName string, msgs ...*genai.LiveServerMessage) []*model.LLMResponse {
	t.Helper()
	in := make(chan *genai.LiveServerMessage, len(msgs))
	for _, m := range msgs {
		in <- m
	}
	close(in)

	c := &liveConnection{modelName: modelName}
	out, _ := c.process(context.Background(), in)
	var resps []*model.LLMResponse
	for r := range out {
		resps = append(resps, r)
	}
	return resps
}

func TestProcess_ThoughtTextIsFlushedSeparately(t *testing.T) {
	resps := runProcess(t, "gemini-3.8-live",
		&genai.LiveServerMessage{ServerContent: &genai.LiveServerContent{
			ModelTurn: &genai.Content{Role: "model", Parts: []*genai.Part{
				{Text: "thinking", Thought: true},
				{Text: "answer"},
			}},
		}},
		&genai.LiveServerMessage{ServerContent: &genai.LiveServerContent{TurnComplete: true}},
	)

	var full []*genai.Part
	for _, r := range resps {
		if r.Content != nil && !r.Partial {
			full = append(full, r.Content.Parts...)
		}
	}
	if len(full) != 2 {
		t.Fatalf("got %d full text parts, want 2", len(full))
	}
	if full[0].Text != "thinking" || !full[0].Thought {
		t.Errorf("first full part = %+v, want thought text", full[0])
	}
	if full[1].Text != "answer" || full[1].Thought {
		t.Errorf("second full part = %+v, want non-thought text", full[1])
	}
}

func TestProcess_Gemini3xInputTranscriptionIsFinal(t *testing.T) {
	resps := runProcess(t, "gemini-3.8-live",
		&genai.LiveServerMessage{ServerContent: &genai.LiveServerContent{
			InputTranscription: &genai.Transcription{Text: "hello"},
		}},
	)
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want 1", len(resps))
	}
	tr := resps[0].InputTranscription
	if tr == nil || tr.Text != "hello" || !tr.Finished || resps[0].Partial {
		t.Errorf("got %+v, want single finished transcription", resps[0])
	}
}

func TestProcess_PendingToolCallsAreSentOnClose(t *testing.T) {
	resps := runProcess(t, "gemini-2.5-flash-native-audio",
		&genai.LiveServerMessage{ToolCall: &genai.LiveServerToolCall{
			FunctionCalls: []*genai.FunctionCall{{Name: "f"}},
		}},
	)
	if len(resps) != 1 || resps[0].Content == nil || resps[0].Content.Parts[0].FunctionCall.Name != "f" {
		t.Fatalf("got %+v, want pending tool call", resps)
	}
}

func TestProcess_UsageMetadataMapsResponseTokens(t *testing.T) {
	resps := runProcess(t, "gemini-3.8-live",
		&genai.LiveServerMessage{UsageMetadata: &genai.UsageMetadata{ResponseTokenCount: 7}},
	)
	if len(resps) != 1 || resps[0].UsageMetadata.CandidatesTokenCount != 7 {
		t.Fatalf("got %+v, want CandidatesTokenCount 7", resps)
	}
}

func TestProcess_TurnCompleteCarriesStatus(t *testing.T) {
	resps := runProcess(t, "gemini-3.8-live",
		&genai.LiveServerMessage{ServerContent: &genai.LiveServerContent{
			TurnComplete:       true,
			TurnCompleteReason: genai.TurnCompleteReasonNeedMoreInput,
			InteractionStatus:  genai.InteractionStatus("IN_PROGRESS"),
		}},
	)
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want 1", len(resps))
	}
	r := resps[0]
	if !r.TurnComplete || r.TurnCompleteReason != genai.TurnCompleteReasonNeedMoreInput ||
		r.InteractionStatus != "IN_PROGRESS" || r.GroundingMetadata == nil {
		t.Errorf("got %+v, want turn complete with reason, status and empty grounding", r)
	}
}

func TestMergeGroundingMetadata(t *testing.T) {
	existing := &genai.GroundingMetadata{
		WebSearchQueries: []string{"a"},
		GroundingChunks:  []*genai.GroundingChunk{{Web: &genai.GroundingChunkWeb{URI: "u1"}}},
	}
	newMetadata := &genai.GroundingMetadata{
		WebSearchQueries:  []string{"a", "b"},
		GroundingChunks:   []*genai.GroundingChunk{{Web: &genai.GroundingChunkWeb{URI: "u2"}}},
		GroundingSupports: []*genai.GroundingSupport{{GroundingChunkIndices: []int32{0}}},
	}

	merged := mergeGroundingMetadata(existing, newMetadata)

	if len(merged.WebSearchQueries) != 2 {
		t.Errorf("WebSearchQueries = %v, want [a b]", merged.WebSearchQueries)
	}
	if len(merged.GroundingChunks) != 2 {
		t.Errorf("got %d chunks, want 2", len(merged.GroundingChunks))
	}
	if len(merged.GroundingSupports) != 1 || merged.GroundingSupports[0].GroundingChunkIndices[0] != 1 {
		t.Errorf("GroundingSupports = %+v, want chunk index shifted to 1", merged.GroundingSupports)
	}
}
