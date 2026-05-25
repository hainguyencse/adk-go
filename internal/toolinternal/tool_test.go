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

package toolinternal

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"google.golang.org/adk/tool"
)

type fakeResponseCacheService struct {
	getReq *tool.GetRequest
	getRes *tool.GetResponse
	getErr error

	setReq *tool.SetRequest
	setErr error

	invalidateReq *tool.InvalidateRequest
	invalidateErr error
}

func (svc *fakeResponseCacheService) Get(ctx context.Context, req *tool.GetRequest) (*tool.GetResponse, error) {
	svc.getReq = req
	return svc.getRes, svc.getErr
}

func (svc *fakeResponseCacheService) Set(ctx context.Context, req *tool.SetRequest) error {
	svc.setReq = req
	return svc.setErr
}

func (svc *fakeResponseCacheService) Invalidate(ctx context.Context, req *tool.InvalidateRequest) error {
	svc.invalidateReq = req
	return svc.invalidateErr
}

func TestToolResponseCacheService_Get(t *testing.T) {
	errCache := errors.New("cache error")
	tests := []struct {
		name     string
		getRes   *tool.GetResponse
		getErr   error
		want     map[string]any
		wantBool bool
	}{
		{
			name: "found",
			getRes: &tool.GetResponse{
				Found:        true,
				ToolResponse: map[string]any{"result": "ok"},
			},
			want:     map[string]any{"result": "ok"},
			wantBool: true,
		},
		{
			name:     "not found",
			getRes:   &tool.GetResponse{},
			wantBool: false,
		},
		{
			name:     "error",
			getErr:   errCache,
			wantBool: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cacheSvc := &fakeResponseCacheService{
				getRes: tt.getRes,
				getErr: tt.getErr,
			}
			svc := ToolResponseCacheService{
				service:   cacheSvc,
				AppName:   "app",
				UserID:    "user",
				SessionID: "session",
			}

			got, ok := svc.Get(t.Context(), "cache-key")
			if ok != tt.wantBool {
				t.Fatalf("ToolResponseCacheService.Get() ok = %v, want %v", ok, tt.wantBool)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ToolResponseCacheService.Get() response mismatch (-want +got):\n%s", diff)
			}

			wantReq := &tool.GetRequest{
				AppName:   "app",
				UserID:    "user",
				SessionID: "session",
				Key:       "cache-key",
			}
			if diff := cmp.Diff(wantReq, cacheSvc.getReq); diff != "" {
				t.Errorf("ToolResponseCacheService.Get() request mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestToolResponseCacheService_SetAndInvalidate(t *testing.T) {
	cacheSvc := &fakeResponseCacheService{
		setErr:        errors.New("set error"),
		invalidateErr: errors.New("invalidate error"),
	}
	svc := ToolResponseCacheService{
		service:   cacheSvc,
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
	}
	value := map[string]any{"result": "ok"}

	svc.Set(t.Context(), "cache-key", value)
	wantSetReq := &tool.SetRequest{
		AppName:      "app",
		UserID:       "user",
		SessionID:    "session",
		Key:          "cache-key",
		ToolResponse: value,
	}
	if diff := cmp.Diff(wantSetReq, cacheSvc.setReq); diff != "" {
		t.Errorf("ToolResponseCacheService.Set() request mismatch (-want +got):\n%s", diff)
	}

	svc.Invalidate(t.Context(), "cache-key")
	wantInvalidateReq := &tool.InvalidateRequest{
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
		Key:       "cache-key",
	}
	if diff := cmp.Diff(wantInvalidateReq, cacheSvc.invalidateReq); diff != "" {
		t.Errorf("ToolResponseCacheService.Invalidate() request mismatch (-want +got):\n%s", diff)
	}
}
