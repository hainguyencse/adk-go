# Voice Agent Example

A real-time voice agent using Gemini Live API with WebSocket. Supports multi-agent system (Root → HR, Accountant).

## Prerequisites

- Go 1.23+
- Google API Key from [Google AI Studio](https://aistudio.google.com/apikey)

## Run

### 1. Set API Key

```bash
export GOOGLE_API_KEY=<your-api-studio-key>
```

### 2. Start Server

```bash
cd examples/voiceagent
go run main.go
```

Server starts at `ws://localhost:8080`.

### 3. Start Client
In examples/voiceagent
Open `client/index.html` in your browser.

## Architecture

```
Browser (index.html)
    ↕ WebSocket (audio/text)
Server (main.go)
    ↕ Gemini Live API
Multi-Agent System:
    ├── acm_root_agent (receptionist, company info)a# Voice Agent Example

A real-time voice agent using Gemini Live API with WebSocket. Supports multi-agent system (Root → HR, Accountant).

## Prerequisites

- Go 1.23+
- Google API Key from [Google AI Studio](https://aistudio.google.com/apikey)

## Run

### 1. Set API Key

```bash
export GOOGLE_API_KEY=<your-api-studio-key>
```

### 2. Start Server

```bash
cd examples/voiceagent
go run main.go
```

Server starts at `ws://localhost:8080`.

### 3. Start Client
In examples/voiceagent. Open `client/index.html` in your browser.

## Architecture

```
Browser (index.html)
    ↕ WebSocket (audio/text)
Server (main.go)
    ↕ Gemini Live API
Multi-Agent System:
    ├── acm_root_agent (receptionist, company info)
    ├── hr_acm_agent (employee lookup)
    └── accountant_acm_agent (salary inquiry)
```

## Endpoints

| Endpoint | Description |
|---|---|
| `ws://localhost:8080/ws/{user_id}/{session_id}` | WebSocket connection |
| `http://localhost:8080/health` | Health check |

    ├── hr_acm_agent (employee lookup)
    └── accountant_acm_agent (salary inquiry)
```

## Endpoints

| Endpoint | Description |
|---|---|
| `ws://localhost:8080/ws/{user_id}/{session_id}` | WebSocket connection |
| `http://localhost:8080/health` | Health check |
