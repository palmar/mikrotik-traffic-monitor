package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/palmar/mikrotik-traffic-monitor/internal/ringbuf"
)

//go:embed all:static
var staticFiles embed.FS

// Server serves the dashboard API and frontend.
type Server struct {
	buf       *ringbuf.RingBuffer
	mu        sync.RWMutex
	clients   map[chan ringbuf.Sample]struct{}
}

// New creates a new server.
func New(buf *ringbuf.RingBuffer) *Server {
	return &Server{
		buf:     buf,
		clients: make(map[chan ringbuf.Sample]struct{}),
	}
}

// Broadcast sends a sample to all connected SSE clients.
func (s *Server) Broadcast(sample ringbuf.Sample) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.clients {
		select {
		case ch <- sample:
		default:
			// slow client, skip
		}
	}
}

func (s *Server) addClient(ch chan ringbuf.Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[ch] = struct{}{}
}

func (s *Server) removeClient(ch chan ringbuf.Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clients, ch)
	close(ch)
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/stream", s.handleStream)
	mux.HandleFunc("/history", s.handleHistory)
	mux.HandleFunc("/health", s.handleHealth)

	// Serve embedded static files at root
	staticSub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("embed sub: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(staticSub)))

	return mux
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := make(chan ringbuf.Sample, 8)
	s.addClient(ch)
	defer s.removeClient(ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case sample := <-ch:
			data, _ := json.Marshal(sample)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(s.buf.Snapshot())
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"time":   time.Now().Unix(),
	})
}
