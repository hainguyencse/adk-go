package agent

import "google.golang.org/genai"

type LiveRequest struct {
	Realtime *genai.LiveRealtimeInput
	Content  *genai.Content
	Close    bool
}

type LiveRequestQueue struct {
	ch chan LiveRequest
}

func NewLiveRequestQueue(buffer int) *LiveRequestQueue {
	return &LiveRequestQueue{ch: make(chan LiveRequest, buffer)}
}

func (q *LiveRequestQueue) SendRealtime(in genai.LiveRealtimeInput) {
	q.ch <- LiveRequest{Realtime: &in}
}

func (q *LiveRequestQueue) SendContent(c *genai.Content) {
	q.ch <- LiveRequest{Content: c}
}

func (q *LiveRequestQueue) Close() {
	// safe close pattern: best effort
	select {
	case q.ch <- LiveRequest{Close: true}:
	default:
	}
	close(q.ch)
}

// Chan returns the underlying channel for consuming requests.
func (q *LiveRequestQueue) Chan() <-chan LiveRequest {
	return q.ch
}
