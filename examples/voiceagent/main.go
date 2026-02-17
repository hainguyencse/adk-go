package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/cmd/launcher"
	"google.golang.org/adk/cmd/launcher/full"
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

	// Check if running in ADK web mode (launcher mode)
	if len(os.Args) > 1 && (os.Args[1] == "web" || os.Args[1] == "console") {
		runWithLauncher(ctx)
		return
	}

	// Default: run custom WebSocket server for voice/audio
	runWebSocketServer(ctx)
}

// runWithLauncher starts the agent using ADK's built-in web UI launcher.
// Usage: go run . web api webui
func runWithLauncher(ctx context.Context) {
	rootAgent, err := vcagent.NewMultiAgentSystem(ctx)
	if err != nil {
		log.Fatalf("Failed to create travel agent: %v", err)
	}

	sessionService := adksession.InMemoryService()

	config := &launcher.Config{
		AgentLoader:    adkagent.NewSingleLoader(rootAgent),
		SessionService: sessionService,
	}

	l := full.NewLauncher()
	if err = l.Execute(ctx, config, os.Args[1:]); err != nil {
		log.Fatalf("Run failed: %v\n\n%s", err, l.CommandLineSyntax())
	}
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
					log.Printf("[Downstream] Function response: %s -> %v", part.FunctionResponse.Name, part.FunctionResponse.Response)

					// task_completed -> send friendly step completion message
					if part.FunctionResponse.Name == "task_completed" {
						label, ok := stepCompleteLabels[event.Author]
						if !ok {
							label = "Step completed"
						}
						resp := EventResponse{StepComplete: label}
						data, _ := json.Marshal(resp)
						if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
							log.Printf("Failed to send step complete: %v", err)
							return
						}
						continue
					}

					// transfer_to_<agent> -> send friendly transfer message
					if strings.HasPrefix(part.FunctionResponse.Name, "transfer_to_") {
						label, ok := transferLabels[part.FunctionResponse.Name]
						if !ok {
							label = strings.TrimPrefix(part.FunctionResponse.Name, "transfer_to_")
						}
						resp := EventResponse{Transfer: label}
						data, _ := json.Marshal(resp)
						if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
							log.Printf("Failed to send transfer: %v", err)
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
}

// FunctionResponseData holds MCP/tool function response data
type FunctionResponseData struct {
	Name     string      `json:"name"`
	Response interface{} `json:"response"`
}

// PdfResponseData holds PDF export data for the client to render
type PdfResponseData struct {
	Name     string `json:"name"`
	Data     string `json:"data,omitempty"`     // base64-encoded PDF
	URL      string `json:"url,omitempty"`      // URL to download PDF
	Filename string `json:"filename,omitempty"` // suggested filename
}

// EventResponse is the JSON response sent to WebSocket clients
type EventResponse struct {
	Author           string                `json:"author,omitempty"`
	Text             string                `json:"text,omitempty"`
	TurnComplete     bool                  `json:"turn_complete,omitempty"`
	Interrupted      bool                  `json:"interrupted,omitempty"`
	Error            string                `json:"error,omitempty"`
	FunctionResponse *FunctionResponseData `json:"function_response,omitempty"`
	StepComplete     string                `json:"step_complete,omitempty"`
	Transfer         string                `json:"transfer,omitempty"`
	Pdf              *PdfResponseData      `json:"pdf,omitempty"`
}

// stepCompleteLabels maps agent names to friendly step completion messages
var stepCompleteLabels = map[string]string{
	"search_location_agent":     "Select Project done",
	"date_range_agent":          "Select Date Range done",
	"transaction_summary_agent": "Transaction Summary done",
	"export_pdf_agent":          "Export PDF done",
}

// transferLabels maps transfer_to_<agent> to friendly names
var transferLabels = map[string]string{
	"transfer_to_pa_agent": "Property Analysis Agent",
}

func sendError(conn *websocket.Conn, message string) {
	resp := EventResponse{Error: message}
	data, _ := json.Marshal(resp)
	conn.WriteMessage(websocket.TextMessage, data)
}

// extractPdfData tries to find PDF data (base64 or URL) from MCP function response.
// MCP mcptoolset returns text content as {"output": "..."} or structured content as {"output": {...}}.
func extractPdfData(name string, response interface{}) *PdfResponseData {
	pdf := &PdfResponseData{
		Name:     name,
		Filename: "transaction_summary.pdf",
	}

	respMap, ok := response.(map[string]interface{})
	if !ok {
		// Response is not a map - try as raw string (could be base64 or URL)
		if s, ok := response.(string); ok {
			classifyPdfString(pdf, s)
		}
		return pdf
	}

	// Check "output" field (mcptoolset wraps MCP text/structured content here)
	output := respMap["output"]

	switch v := output.(type) {
	case string:
		classifyPdfString(pdf, v)
	case map[string]interface{}:
		// Structured content: look for url, data, filename fields
		if u, ok := v["url"].(string); ok {
			pdf.URL = u
		}
		if d, ok := v["data"].(string); ok {
			pdf.Data = d
		}
		if f, ok := v["filename"].(string); ok {
			pdf.Filename = f
		}
		// Also check nested "blob" field
		if b, ok := v["blob"].(string); ok && pdf.Data == "" {
			pdf.Data = b
		}
	}

	// Also check top-level fields as fallback
	if pdf.URL == "" {
		if u, ok := respMap["url"].(string); ok {
			pdf.URL = u
		}
	}
	if pdf.Data == "" {
		if d, ok := respMap["data"].(string); ok {
			pdf.Data = d
		}
	}

	return pdf
}

func classifyPdfString(pdf *PdfResponseData, s string) {
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		pdf.URL = s
	} else if len(s) > 100 {
		// Long string is likely base64-encoded PDF data
		pdf.Data = s
	}
}
