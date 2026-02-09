package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"

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

	// Create ACM Agent (single agent with all tools)
	// Note: Multi-agent transfer doesn't work well with Live API's persistent websocket
	// For Live API, use single agent. Multi-agent is better for text-based APIs.
	// Using single agent with all tools (multi-agent transfer not supported in Live API)
	acmAgent, err := vcagent.NewMultiAgentSystem(ctx)
	if err != nil {
		log.Fatalf("Failed to create ACM agent: %v", err)
	}

	// Create session service (in-memory)
	sessionService := adksession.InMemoryService()

	// Create LiveRunner (singleton)
	liveRunner, err := adkrunner.New(adkrunner.Config{
		AppName:        AppName,
		Agent:          acmAgent,
		SessionService: sessionService,
	})
	if err != nil {
		log.Fatalf("Failed to create live runner: %v", err)
	}

	// Create server
	server := &Server{
		appName:        AppName,
		agent:          acmAgent,
		sessionService: sessionService,
		liveRunner:     liveRunner,
	}

	// Setup routes
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
			content := genai.Content{
				Parts: []*genai.Part{
					{Text: string(data)},
				},
				Role: "user",
			}
			queue.SendContent(content)
		}
	}
}

func (s *Server) downstreamTask(
	ctx context.Context,
	conn *websocket.Conn,
	liveRunner *adkrunner.Runner,
	userID, sessionID string,
	queue *adkagent.LiveRequestQueue,
	runConfig adkagent.RunConfig,
) {
	for event, err := range liveRunner.RunLive(ctx, userID, sessionID, queue, runConfig) {
		// Only log text events, not audio (too verbose)
		if err != nil {
			log.Printf("RunLive error: %v", err)
			sendError(conn, err.Error())
			continue
		}

		if event == nil {
			continue
		}

		// Check if event contains audio data
		hasAudio := false
		if event.Content != nil && event.Content.Parts != nil {
			for _, part := range event.Content.Parts {
				if part.InlineData != nil && part.InlineData.MIMEType != "" &&
					strings.Contains(part.InlineData.MIMEType, "audio") {
					// Send audio as binary (PCM16 @ 24kHz from Gemini)
					if err := conn.WriteMessage(websocket.BinaryMessage, part.InlineData.Data); err != nil {
						log.Printf("Failed to send audio: %v", err)
						return
					}
					hasAudio = true
				}
			}
		}

		// Send JSON for non-audio events or events with metadata
		if !hasAudio || event.TurnComplete || event.Interrupted {
			resp := eventToJSON(event)
			if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
				log.Printf("Failed to send event: %v", err)
				return
			}
		}
	}
}

// EventResponse is the JSON response sent to WebSocket clients
type EventResponse struct {
	Author       string `json:"author,omitempty"`
	Text         string `json:"text,omitempty"`
	TurnComplete bool   `json:"turn_complete,omitempty"`
	Interrupted  bool   `json:"interrupted,omitempty"`
	Error        string `json:"error,omitempty"`
}

func eventToJSON(event *adksession.Event) []byte {
	resp := EventResponse{
		Author:       event.Author,
		TurnComplete: event.TurnComplete,
		Interrupted:  event.Interrupted,
	}

	// Extract text from content
	if event.Content != nil && event.Content.Parts != nil {
		for _, part := range event.Content.Parts {
			if part.Text != "" {
				resp.Text = part.Text
				break
			}
		}
	}

	data, _ := json.Marshal(resp)
	return data
}

func sendError(conn *websocket.Conn, message string) {
	resp := EventResponse{Error: message}
	data, _ := json.Marshal(resp)
	conn.WriteMessage(websocket.TextMessage, data)
}
