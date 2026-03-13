package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/palmar/mikrotik-traffic-monitor/internal/ringbuf"
	"github.com/palmar/mikrotik-traffic-monitor/internal/snmp"
)

//go:embed all:static
var staticFiles embed.FS

// DeviceInfo describes a device and its discovered interfaces.
type DeviceInfo struct {
	Name       string   `json:"name"`
	Interfaces []string `json:"interfaces"`
}

// Server serves the dashboard API and frontend.
type Server struct {
	// devices is the ordered list of devices with their interfaces.
	devices []DeviceInfo
	// pollers maps device name to its SNMP poller (for rediscovery).
	pollers map[string]*snmp.Poller
	// buffers maps "device/interface" to its ring buffer.
	buffers map[string]*ringbuf.RingBuffer
	mu      sync.RWMutex
	clients map[chan snmp.InterfaceSample]struct{}
}

// New creates a new server.
func New() *Server {
	return &Server{
		pollers: make(map[string]*snmp.Poller),
		buffers: make(map[string]*ringbuf.RingBuffer),
		clients: make(map[chan snmp.InterfaceSample]struct{}),
	}
}

// BufKey returns the composite buffer key for a device/interface pair.
func BufKey(device, iface string) string {
	return device + "/" + iface
}

// RegisterDevice adds a device and its discovered interfaces to the server.
func (s *Server) RegisterDevice(name string, interfaces []string, buffers map[string]*ringbuf.RingBuffer, poller *snmp.Poller) {
	s.devices = append(s.devices, DeviceInfo{Name: name, Interfaces: interfaces})
	s.pollers[name] = poller
	for k, buf := range buffers {
		s.buffers[k] = buf
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
	mux.HandleFunc("/api/devices", s.handleDevices)
	mux.HandleFunc("/api/devices/rediscover", s.handleRediscover)

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
				"device":  sample.Device,
				"iface":   sample.Interface,
				"ts":      sample.Sample.Timestamp,
				"in_bps":  sample.Sample.InBps,
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

	// Structure: { "device1": { "ether1": [...samples], ... }, ... }
	result := make(map[string]map[string][]ringbuf.Sample)
	for _, dev := range s.devices {
		devMap := make(map[string][]ringbuf.Sample)
		for _, iface := range dev.Interfaces {
			key := BufKey(dev.Name, iface)
			if buf, ok := s.buffers[key]; ok {
				devMap[iface] = buf.Snapshot()
			}
		}
		result[dev.Name] = devMap
	}
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(s.devices)
}

// handleRediscover triggers interface rediscovery on a device.
// POST /api/devices/rediscover { "device": "name" }
func (s *Server) handleRediscover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Device string `json:"device"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	poller, ok := s.pollers[req.Device]
	if !ok {
		http.Error(w, "device not found", http.StatusNotFound)
		return
	}

	newIfaces, err := poller.Rediscover()
	if err != nil {
		http.Error(w, "rediscovery failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Register any newly discovered interfaces
	if len(newIfaces) > 0 {
		for i, dev := range s.devices {
			if dev.Name == req.Device {
				for _, di := range newIfaces {
					s.devices[i].Interfaces = append(s.devices[i].Interfaces, di.Name)
					s.buffers[BufKey(req.Device, di.Name)] = di.Buffer
				}
				sort.Strings(s.devices[i].Interfaces)
				break
			}
		}
	}

	// Return the full updated device info
	for _, dev := range s.devices {
		if dev.Name == req.Device {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(dev)
			return
		}
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"time":   time.Now().Unix(),
	})
}
