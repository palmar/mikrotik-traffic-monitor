package ringbuf

import "sync"

// Sample represents a single throughput measurement.
type Sample struct {
	Timestamp int64   `json:"ts"`
	InBps     float64 `json:"in_bps"`
	OutBps    float64 `json:"out_bps"`
}

// RingBuffer is a thread-safe fixed-size circular buffer of samples.
type RingBuffer struct {
	mu      sync.RWMutex
	buf     []Sample
	size    int
	pos     int
	full    bool
}

// New creates a ring buffer that holds up to size samples.
func New(size int) *RingBuffer {
	return &RingBuffer{
		buf:  make([]Sample, size),
		size: size,
	}
}

// Push adds a sample to the buffer.
func (r *RingBuffer) Push(s Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.pos] = s
	r.pos = (r.pos + 1) % r.size
	if !r.full && r.pos == 0 {
		r.full = true
	}
}

// Snapshot returns all samples in chronological order.
func (r *RingBuffer) Snapshot() []Sample {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if !r.full {
		out := make([]Sample, r.pos)
		copy(out, r.buf[:r.pos])
		return out
	}

	out := make([]Sample, r.size)
	copy(out, r.buf[r.pos:])
	copy(out[r.size-r.pos:], r.buf[:r.pos])
	return out
}

// Latest returns the most recent sample, if any.
func (r *RingBuffer) Latest() (Sample, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.full && r.pos == 0 {
		return Sample{}, false
	}
	idx := r.pos - 1
	if idx < 0 {
		idx = r.size - 1
	}
	return r.buf[idx], true
}
