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
	"github.com/palmar/mikrotik-traffic-monitor/internal/snmp"
)

//go:embed all:static
var staticFiles embed.FS

// Server serves the dashboard API and frontend.
type Server struct {
	buffers    map[string]*ringbuf.RingBuffer
	interfaces []string
	mu         sync.RWMutex
	clients    map[chan snmp.InterfaceSample]struct{}
}

// New creates a new server with per-interface ring buffers.
func New(buffers map[string]*ringbuf.RingBuffer, interfaces []string) *Server {
	return &Server{
		buffers:    buffers,
		interfaces: interfaces,
		clients:    make(map[chan snmp.InterfaceSample]struct{}),
	}
}

// Broadcast sends a sample to all connected SSE clients.
func (s *Server) Broadcast(sample snmp.InterfaceSample) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for ch := range s.clients {
		select {
		case ch <- sample:
		default:
		}
	}
}

func (s *Server) addClient(ch chan snmp.InterfaceSample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[ch] = struct{}{}
}

func (s *Server) removeClient(ch chan snmp.InterfaceSample) {
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
	mux.HandleFunc("/interfaces", s.handleInterfaces)

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

	ch := make(chan snmp.InterfaceSample, 8)
	s.addClient(ch)
	defer s.removeClient(ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case sample := <-ch:
			data, _ := json.Marshal(map[string]interface{}{
				"iface":  sample.Interface,
				"ts":     sample.Sample.Timestamp,
				"in_bps": sample.Sample.InBps,
				"out_bps": sample.Sample.OutBps,
			})
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")

	result := make(map[string][]ringbuf.Sample, len(s.buffers))
	for name, buf := range s.buffers {
		result[name] = buf.Snapshot()
	}
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleInterfaces(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(s.interfaces)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"time":   time.Now().Unix(),
	})
}
