# Voice Agent Example

A real-time voice agent using Gemini Live API with WebSocket. Supports multi-agent system with sequential workflow.

## Prerequisites

- Go 1.23+
- Google API Key from [Google AI Studio](https://aistudio.google.com/apikey)

## Run

### 1. Set API Key

```bash
export GOOGLE_API_KEY=<your-api-studio-key>
```

### 2a. Start Voice Server (WebSocket)

```bash
cd examples/voiceagent
go run .
```

Server starts at `ws://localhost:8080`.

Then open `client/index.html` in your browser.

### 2b. Start ADK Web UI

```bash
cd examples/voiceagent
go run . web api webui
```

Opens the ADK web UI at `http://localhost:8080`.

## Architecture

```
Browser (index.html)
    ↕ WebSocket (audio/text)
Server (main.go)
    ↕ Gemini Live API
Sequential Agent System:
    ├── flight_agent (present flights, user selects)
    ├── hotel_agent (present hotels, user selects)
    └── trip_summary_agent (summary + total cost)
```

## Endpoints

| Endpoint | Description |
|---|---|
| `ws://localhost:8080/ws/{user_id}/{session_id}` | WebSocket connection |
| `http://localhost:8080/health` | Health check |
