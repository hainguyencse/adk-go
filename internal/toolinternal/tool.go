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

// Package toolinternal defines internal-only interfaces and logic for tools.
package toolinternal

import (
	"context"

	"google.golang.org/genai"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

type FunctionTool interface {
	tool.Tool
	Declaration() *genai.FunctionDeclaration
	Run(ctx tool.Context, args any) (result map[string]any, err error)
}

type RequestProcessor interface {
	ProcessRequest(ctx tool.Context, req *model.LLMRequest) error
}

type ToolResponseCacheService struct {
	Service   tool.ResponseCacheService
	AppName   string
	UserID    string
	SessionID string
}

func (svc ToolResponseCacheService) Get(ctx context.Context, key string) (map[string]any, bool) {
	res, err := svc.Service.Get(ctx, &tool.GetRequest{
		AppName:   svc.AppName,
		UserID:    svc.UserID,
		SessionID: svc.SessionID,
		Key:       key,
	})
	if err != nil {
		return nil, false
	}

	return res.ToolResponse, res.Found
}

func (svc ToolResponseCacheService) Set(ctx context.Context, key string, value map[string]any) {
	_ = svc.Service.Set(ctx, &tool.SetRequest{
		AppName:      svc.AppName,
		UserID:       svc.UserID,
		SessionID:    svc.SessionID,
		Key:          key,
		ToolResponse: value,
	})
}

func (svc ToolResponseCacheService) Invalidate(ctx context.Context, key string) {
	_ = svc.Service.Invalidate(ctx, &tool.InvalidateRequest{
		AppName:   svc.AppName,
		UserID:    svc.UserID,
		SessionID: svc.SessionID,
		Key:       key,
	})
}
