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

package tool_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"google.golang.org/adk/tool"
)

func TestInMemoryResponseCacheService(t *testing.T) {
	svc := tool.InMemoryResponseCacheService()
	ctx := t.Context()

	req := &tool.GetRequest{
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
		Key:       "tool-call-1",
	}
	got, err := svc.Get(ctx, req)
	if err != nil {
		t.Fatalf("ResponseCacheService.Get() error = %v", err)
	}
	if got.Found {
		t.Fatalf("ResponseCacheService.Get() found = true, want false")
	}

	want := map[string]any{
		"result": "ok",
		"count":  1,
	}
	if err := svc.Set(ctx, &tool.SetRequest{
		AppName:      req.AppName,
		UserID:       req.UserID,
		SessionID:    req.SessionID,
		Key:          req.Key,
		ToolResponse: want,
	}); err != nil {
		t.Fatalf("ResponseCacheService.Set() error = %v", err)
	}

	got, err = svc.Get(ctx, req)
	if err != nil {
		t.Fatalf("ResponseCacheService.Get() error = %v", err)
	}
	if !got.Found {
		t.Fatalf("ResponseCacheService.Get() found = false, want true")
	}
	if diff := cmp.Diff(want, got.ToolResponse); diff != "" {
		t.Errorf("ResponseCacheService.Get() response mismatch (-want +got):\n%s", diff)
	}

	otherKeyResp := map[string]any{"result": "other"}
	if err := svc.Set(ctx, &tool.SetRequest{
		AppName:      req.AppName,
		UserID:       req.UserID,
		SessionID:    req.SessionID,
		Key:          "tool-call-2",
		ToolResponse: otherKeyResp,
	}); err != nil {
		t.Fatalf("ResponseCacheService.Set() error = %v", err)
	}

	got, err = svc.Get(ctx, req)
	if err != nil {
		t.Fatalf("ResponseCacheService.Get() error = %v", err)
	}
	if diff := cmp.Diff(want, got.ToolResponse); diff != "" {
		t.Errorf("ResponseCacheService.Get() response mismatch (-want +got):\n%s", diff)
	}

	if err := svc.Invalidate(ctx, &tool.InvalidateRequest{
		AppName:   req.AppName,
		UserID:    req.UserID,
		SessionID: req.SessionID,
		Key:       req.Key,
	}); err != nil {
		t.Fatalf("ResponseCacheService.Invalidate() error = %v", err)
	}

	got, err = svc.Get(ctx, req)
	if err != nil {
		t.Fatalf("ResponseCacheService.Get() error = %v", err)
	}
	if got.Found {
		t.Errorf("ResponseCacheService.Get() found = true after invalidate, want false")
	}

	got, err = svc.Get(ctx, &tool.GetRequest{
		AppName:   req.AppName,
		UserID:    req.UserID,
		SessionID: req.SessionID,
		Key:       "tool-call-2",
	})
	if err != nil {
		t.Fatalf("ResponseCacheService.Get() error = %v", err)
	}
	if !got.Found {
		t.Fatalf("ResponseCacheService.Get() found = false for other key, want true")
	}
	if diff := cmp.Diff(otherKeyResp, got.ToolResponse); diff != "" {
		t.Errorf("ResponseCacheService.Get() response mismatch (-want +got):\n%s", diff)
	}
}

func TestInMemoryResponseCacheService_NilRequest(t *testing.T) {
	svc := tool.InMemoryResponseCacheService()
	ctx := t.Context()

	if err := svc.Set(ctx, nil); !errors.Is(err, tool.ErrRequestRequired) {
		t.Errorf("ResponseCacheService.Set() error = %v, want %v", err, tool.ErrRequestRequired)
	}

	if _, err := svc.Get(ctx, nil); !errors.Is(err, tool.ErrRequestRequired) {
		t.Errorf("ResponseCacheService.Get() error = %v, want %v", err, tool.ErrRequestRequired)
	}

	if err := svc.Invalidate(ctx, nil); !errors.Is(err, tool.ErrRequestRequired) {
		t.Errorf("ResponseCacheService.Invalidate() error = %v, want %v", err, tool.ErrRequestRequired)
	}
}
