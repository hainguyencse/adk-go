package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	adkagent "google.golang.org/adk/agent"
	mapagent "google.golang.org/adk/examples/map/agent"
	adkrunner "google.golang.org/adk/runner"
	adksession "google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/gorilla/websocket"
)

const (
	AppName = "mapAgent"
	Port    = ":8080"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for development
	},
}

func main() {
	ctx := context.Background()

	runWebSocketServer(ctx)
}

// runWebSocketServer starts the custom WebSocket server for voice/audio streaming.
// Usage: go run .
func runWebSocketServer(ctx context.Context) {
	llmModel, err := mapagent.GetModel(ctx)
	if err != nil {
		log.Fatalf("Failed to get model: %v", err)
	}

	rootAgent, err := mapagent.NewMapAgent(ctx, llmModel)
	if err != nil {
		log.Fatalf("Failed to create agent: %v", err)
	}

	sessionService := adksession.InMemoryService()

	liveRunner, err := adkrunner.New(adkrunner.Config{
		AppName:        AppName,
		Agent:          rootAgent,
		SessionService: sessionService,
	})
	if err != nil {
		log.Fatalf("Failed to create live runner: %v", err)
	}

	server := &Server{
		appName:        AppName,
		agent:          rootAgent,
		sessionService: sessionService,
		liveRunner:     liveRunner,
	}

	http.HandleFunc("/ws/", server.handleWebSocket)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	log.Printf("Starting WebSocket server on %s", Port)
	if err := http.ListenAndServe(Port, nil); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

type Server struct {
	appName        string
	agent          adkagent.Agent
	sessionService adksession.Service
	liveRunner     *adkrunner.Runner
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/ws/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.Error(w, "Invalid path: expected /ws/{user_id}/{session_id}", http.StatusBadRequest)
		return
	}
	userID := parts[0]
	sessionID := parts[1]

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade connection: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("WebSocket connected: user=%s session=%s", userID, sessionID)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Create LiveRequestQueue
	liveRequestQueue := adkagent.NewLiveRequestQueue(0)
	defer liveRequestQueue.Close()

	// Run config
	runConfig := adkagent.RunConfig{}

	var wg sync.WaitGroup
	wg.Add(2)

	// Upstream task: WebSocket -> LiveRequestQueue
	go func() {
		defer wg.Done()
		defer cancel()
		s.upstreamTask(ctx, conn, liveRequestQueue)
	}()

	// Downstream task: LiveRunner -> WebSocket
	go func() {
		defer wg.Done()
		defer cancel()
		s.downstreamTask(ctx, conn, s.liveRunner, userID, sessionID, liveRequestQueue, runConfig)
	}()

	wg.Wait()
	log.Printf("WebSocket disconnected: user=%s session=%s", userID, sessionID)
}

func (s *Server) upstreamTask(ctx context.Context, conn *websocket.Conn, queue *adkagent.LiveRequestQueue) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		messageType, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Println("WebSocket closed normally")
			} else {
				log.Printf("WebSocket read error: %v", err)
			}
			return
		}

		switch messageType {
		case websocket.BinaryMessage:
			// Binary data = PCM16 audio @ 16kHz (don't log binary data - it's slow and useless)
			blob := &genai.Blob{
				MIMEType: "audio/pcm;rate=16000",
				Data:     data,
			}
			queue.SendRealtime(genai.LiveRealtimeInput{
				Media: blob,
			})

		case websocket.TextMessage:
			// Text message
			log.Printf("[Upstream] Text message: %s", string(data))
			content := &genai.Content{
				Parts: []*genai.Part{
					{Text: string(data)},
				},
				Role: "user",
			}
			queue.SendContent(content)
		}
	}
}

const maxReconnectAttempts = 3

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Gemini Live API intermittent websocket close errors
	return strings.Contains(msg, "websocket: close 1008") ||
		strings.Contains(msg, "websocket: close 1011")
}

func (s *Server) downstreamTask(
	ctx context.Context,
	conn *websocket.Conn,
	liveRunner *adkrunner.Runner,
	userID, sessionID string,
	queue *adkagent.LiveRequestQueue,
	runConfig adkagent.RunConfig,
) {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}

		var lastErr error

		for event, err := range liveRunner.RunLive(ctx, userID, sessionID, queue, runConfig) {
			if err != nil {
				lastErr = err
				log.Printf("RunLive error: %v", err)
				sendError(conn, err.Error())
				continue
			}

			// Reset attempt counter on successful events
			attempt = 0

			if event == nil {
				continue
			}

			// Process event parts
			if event.Content != nil && event.Content.Parts != nil {
				for _, part := range event.Content.Parts {
					// Audio -> send as binary (PCM16 @ 24kHz from Gemini)
					if part.InlineData != nil && part.InlineData.MIMEType != "" &&
						strings.Contains(part.InlineData.MIMEType, "audio") {
						if err := conn.WriteMessage(websocket.BinaryMessage, part.InlineData.Data); err != nil {
							log.Printf("Failed to send audio: %v", err)
							return
						}
					}

					// Function response -> send as JSON to chatbox
					if part.FunctionResponse != nil {
						if part.FunctionResponse.Name == "execute_map_query" {
							var output mapagent.MapQueryOutput
							if b, err := json.Marshal(part.FunctionResponse.Response); err == nil {
								json.Unmarshal(b, &output)
							}

							mapUpdateRequest := newMapUpdateRequestFromMapQueryOutput(&output)

							log.Printf("mapUpdateRequest: %v\n", mapUpdateRequest)
							mapUpdateRespose, _ := searchProjectsInMap(mapUpdateRequest)

							resp := EventResponse{
								Author:            event.Author,
								EventType:         EventTypeMapUpdate,
								MapUpdateRequest:  mapUpdateRequest,
								MapUpdateResponse: mapUpdateRespose,
							}

							data, _ := json.Marshal(resp)
							if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
								log.Printf("Failed to send event: %v", err)
								return
							}
						}

						if part.FunctionResponse.Name != "" {
							log.Printf("Tool call %v", part.FunctionResponse.Name)
							log.Printf("Tool Response: %v", part.FunctionResponse.Response)
						}
					}
				}
			}

			// Send turn_complete / interrupted signals
			if event.TurnComplete || event.Interrupted {
				resp := EventResponse{
					TurnComplete: event.TurnComplete,
					Interrupted:  event.Interrupted,
				}
				data, _ := json.Marshal(resp)
				if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
					log.Printf("Failed to send event: %v", err)
					return
				}
			}
		}

		// RunLive ended normally (flow completed) — restart for new interaction
		if lastErr == nil {
			log.Printf("RunLive completed, restarting for new interaction")
			continue
		}

		// Retryable error — reconnect with backoff
		if isRetryableError(lastErr) && attempt < maxReconnectAttempts {
			attempt++
			delay := time.Duration(attempt) * time.Second
			log.Printf("Gemini Live API disconnected (attempt %d/%d), reconnecting in %v...", attempt, maxReconnectAttempts, delay)
			sendError(conn, "Reconnecting...")
			time.Sleep(delay)
			continue
		}

		// Not retryable or max attempts reached — exit
		if attempt >= maxReconnectAttempts {
			log.Printf("Max reconnect attempts (%d) reached, giving up", maxReconnectAttempts)
			sendError(conn, "Connection lost after multiple retries. Please refresh to restart.")
		}
		return
	}
}

type EventResponse struct {
	Author       string `json:"author"`
	Text         string `json:"text,omitempty"`
	TurnComplete bool   `json:"turnComplete"`
	Interrupted  bool   `json:"interrupted"`
	Error        string `json:"error,omitempty"`

	EventType EventType `json:"eventType"`

	// MapUpdateRequest return when EventType = map_update
	// MapUpdateRequest return request payload base on user voice - Client use new request to call api - reload data in map
	MapUpdateRequest  *MapUpdateRequest  `json:"mapUpdateRequest,omitempty"`
	MapUpdateResponse *MapUpdateResponse `json:"mapUpdateResponse,omitempty"`
}

type EventType string

const (
	// map_update: change search nearby location, filter, zoom level
	EventTypeMapUpdate EventType = "map_update"

	// export_pdf: export PDF
	EventTypeExportPDf EventType = "export_pdf"
)

type MapUpdateRequest struct {
	LocationIDs          []string `json:"locationIDs"`
	LocationType         string   `json:"locationType"`
	Keyword              string   `json:"keyword"`
	Radius               int      `json:"radius"`
	IsNewLaunch          bool     `json:"isNewLaunch"`
	UnitBedroomTypes     []string `json:"unitBedroomTypes"`
	TransactionDateRange string   `json:"transactionDateRange"`
	ZoomLevel            int      `json:"zoomLevel"`
}

func newMapUpdateRequestFromMapQueryOutput(output *mapagent.MapQueryOutput) *MapUpdateRequest {
	req := &MapUpdateRequest{
		LocationType:         output.LocationType,
		Keyword:              output.Keyword,
		TransactionDateRange: output.TransactionDateRange,
		IsNewLaunch:          output.IsNewLaunch == "newLaunch",
	}

	if output.LocationIDs != "" {
		req.LocationIDs = strings.Split(output.LocationIDs, ",")
	}

	if output.UnitBedroomTypes != "" {
		req.UnitBedroomTypes = strings.Split(output.UnitBedroomTypes, ",")
	}

	if r, err := strconv.Atoi(output.Radius); err == nil {
		req.Radius = r
	}

	if z, err := strconv.Atoi(output.ZoomLevel); err == nil {
		req.ZoomLevel = z
	}

	return req
}

type MapUpdateResponse struct {
	Count int `json:"count"`
}

func sendError(conn *websocket.Conn, message string) {
	resp := EventResponse{Error: message}
	data, _ := json.Marshal(resp)
	conn.WriteMessage(websocket.TextMessage, data)
}

func searchProjectsInMap(mapRequest *MapUpdateRequest) (*MapUpdateResponse, error) {
	baseUrl := os.Getenv("BASE_URL")

	reqBody := map[string]any{
		"selectedLocation": map[string]any{
			"locationType": "anywhere",
		},
		"rentalTransactionFilter": map[string]any{
			"contractDateRangeType": mapRequest.TransactionDateRange,
		},
		"propertyFilter": map[string]any{
			"propertyType":       "Condo",
			"unitBedroomTypes":   mapRequest.UnitBedroomTypes,
			"isNewLaunchProject": mapRequest.IsNewLaunch,
		},
		"saleTransactionFilter": map[string]any{
			"contractDateRangeType": mapRequest.TransactionDateRange,
		},
		"expectedLocationType": "project",
		"pageSize":             20,
		"page":                 1,
		"sortOrder":            "desc",
	}

	fmt.Printf("searchProjectsInMap LocationIDs: %v", mapRequest.LocationIDs)
	fmt.Printf("searchProjectsInMap LocationType: %v", mapRequest.LocationType)

	if len(mapRequest.LocationIDs) > 0 && mapRequest.LocationType != "" {
		if mapRequest.LocationType == "primary_school" {
			// mapRequest.LocationIDs []string to []int
			locationIDs, _ := stringSliceToIntSlice(mapRequest.LocationIDs)

			reqBody["selectedLocation"] = map[string]any{
				"locationIDs":  locationIDs,
				"locationType": "school",
				"radius":       mapRequest.Radius,
			}
		}

	}

	if mapRequest.TransactionDateRange != "" {
		reqBody["rentalTransactionFilter"] = map[string]any{
			"contractDateRangeType": mapRequest.TransactionDateRange,
		}

		reqBody["saleTransactionFilter"] = map[string]any{
			"contractDateRangeType": mapRequest.TransactionDateRange,
		}
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	log.Printf("reqBody ==> %s", bodyBytes)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		baseUrl+"/api/property-analysis/search", bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+os.Getenv("API_TOKEN"))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call property-analysis API: %w", err)
	}
	defer resp.Body.Close()

	var rawResp struct {
		Data []struct {
			Location struct {
				Location struct {
					ID           int    `json:"id"`
					Name         string `json:"name"`
					PropertyType string `json:"propertyType"`
					District     string `json:"district"`
				} `json:"location"`
			} `json:"location"`
		} `json:"data"`
		TotalCount int `json:"totalCount"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rawResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	result := &MapUpdateResponse{
		Count: rawResp.TotalCount,
	}

	return result, nil
}

func stringSliceToIntSlice(input []string) ([]int, error) {
	result := make([]int, len(input))

	for i, v := range input {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, err
		}
		result[i] = n
	}

	return result, nil
}
