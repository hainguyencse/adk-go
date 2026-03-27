package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	adkagent "google.golang.org/adk/agent"
	vcagent "google.golang.org/adk/examples/voiceagent/agent"
	adkrunner "google.golang.org/adk/runner"
	adksession "google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/gorilla/websocket"
)

const (
	AppName = "voiceagent"
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
	acmAgent, err := vcagent.NewMultiAgentSystem(ctx)
	if err != nil {
		log.Fatalf("Failed to create agent: %v", err)
	}

	sessionService := adksession.InMemoryService()

	liveRunner, err := adkrunner.New(adkrunner.Config{
		AppName:        AppName,
		Agent:          acmAgent,
		SessionService: sessionService,
	})
	if err != nil {
		log.Fatalf("Failed to create live runner: %v", err)
	}

	server := &Server{
		appName:        AppName,
		agent:          acmAgent,
		sessionService: sessionService,
		liveRunner:     liveRunner,
	}

	http.HandleFunc("/ws/", server.handleWebSocket)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	log.Printf("Starting WebSocket server on %s", Port)
	log.Printf("WebSocket endpoint: ws://localhost%s/ws/{user_id}/{session_id}", Port)
	log.Printf("Open client/index.html in browser to connect")
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
	// Extract user_id and session_id from URL path: /ws/{user_id}/{session_id}
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
	runConfig := adkagent.RunConfig{
		ResponseModalities: []genai.Modality{genai.ModalityAudio},
		SpeechConfig: &genai.SpeechConfig{
			VoiceConfig: &genai.VoiceConfig{
				PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{
					VoiceName: "Aoede",
				},
			},
		},
	}

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
			queue.SendRealtime(&genai.LiveRealtimeInput{
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
		backToMenu := false

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
						// back_to_menu: break out of RunLive to restart with root agent
						if part.FunctionResponse.Name == "back_to_menu" {
							backToMenu = true
							continue
						}

						if part.FunctionResponse.Name == "task_completed" {
							resp := EventResponse{Text: "Task completed!"}
							data, _ := json.Marshal(resp)
							if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
								log.Printf("Failed to send step complete: %v", err)
								return
							}
							continue
						}

						resp := EventResponse{
							Author: event.Author,
							FunctionResponse: &FunctionResponseData{
								Name:     part.FunctionResponse.Name,
								Response: part.FunctionResponse.Response,
							},
						}
						data, _ := json.Marshal(resp)
						if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
							log.Printf("Failed to send function response: %v", err)
							return
						}
					}
				}

				// Break out of RunLive range loop — yield returns false,
				// unwinding the entire chain (sub-agent → sequential → root)
				if backToMenu {
					break
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

		// Back to menu — restart RunLive immediately with root agent
		if backToMenu {
			log.Printf("Back to menu requested, restarting RunLive")
			resp := EventResponse{Text: "Returned to main menu. What would you like to do?"}
			data, _ := json.Marshal(resp)
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				log.Printf("Failed to send back-to-menu message: %v", err)
				return
			}
			continue
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

// FunctionResponseData holds MCP/tool function response data
type FunctionResponseData struct {
	Name     string      `json:"name"`
	Response interface{} `json:"response"`
}

// EventResponse is the JSON response sent to WebSocket clients
type EventResponse struct {
	Author           string                `json:"author,omitempty"`
	Text             string                `json:"text,omitempty"`
	TurnComplete     bool                  `json:"turn_complete,omitempty"`
	Interrupted      bool                  `json:"interrupted,omitempty"`
	Error            string                `json:"error,omitempty"`
	FunctionResponse *FunctionResponseData `json:"function_response,omitempty"`
}

func sendError(conn *websocket.Conn, message string) {
	resp := EventResponse{Error: message}
	data, _ := json.Marshal(resp)
	conn.WriteMessage(websocket.TextMessage, data)
}
