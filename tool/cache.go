package tool

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrRequestRequired = errors.New("request required")
)

type ResponseCacheService interface {
	Set(ctx context.Context, req *SetRequest) error
	Get(ctx context.Context, req *GetRequest) (*GetResponse, error)
	Invalidate(ctx context.Context, req *InvalidateRequest) error
}

type SetRequest struct {
	AppName, UserID, SessionID string
	Key                        string
	ToolResponse               map[string]any
}

type GetRequest struct {
	AppName, UserID, SessionID string
	Key                        string
}

type GetResponse struct {
	Found        bool
	ToolResponse map[string]any
}

type InvalidateRequest struct {
	AppName, UserID, SessionID string
	Key                        string
}

type responseCacheKey struct {
	appName, userID, sessionID, key string
}

type responseCacheValue map[string]any

type inMemoryResponseCacheService struct {
	mu    sync.RWMutex
	store map[responseCacheKey]responseCacheValue
}

func InMemoryResponseCacheService() ResponseCacheService {
	return &inMemoryResponseCacheService{
		mu:    sync.RWMutex{},
		store: map[responseCacheKey]responseCacheValue{},
	}
}

func (svc *inMemoryResponseCacheService) Set(ctx context.Context, req *SetRequest) error {
	if req == nil {
		return ErrRequestRequired
	}

	svc.mu.Lock()
	defer svc.mu.Unlock()

	key := responseCacheKey{
		appName:   req.AppName,
		userID:    req.UserID,
		sessionID: req.SessionID,
		key:       req.Key,
	}
	svc.store[key] = req.ToolResponse
	return nil
}

func (svc *inMemoryResponseCacheService) Get(ctx context.Context, req *GetRequest) (*GetResponse, error) {
	if req == nil {
		return nil, ErrRequestRequired
	}

	svc.mu.RLock()
	defer svc.mu.RUnlock()

	key := responseCacheKey{
		appName:   req.AppName,
		userID:    req.UserID,
		sessionID: req.SessionID,
		key:       req.Key,
	}
	value, found := svc.store[key]
	return &GetResponse{
		Found:        found,
		ToolResponse: value,
	}, nil
}

func (svc *inMemoryResponseCacheService) Invalidate(ctx context.Context, req *InvalidateRequest) error {
	if req == nil {
		return ErrRequestRequired
	}

	svc.mu.Lock()
	defer svc.mu.Unlock()

	key := responseCacheKey{
		appName:   req.AppName,
		userID:    req.UserID,
		sessionID: req.SessionID,
		key:       req.Key,
	}
	delete(svc.store, key)

	return nil
}
