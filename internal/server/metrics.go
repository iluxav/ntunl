package server

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	metricsBuckets        = 60
	metricsBucketDuration = time.Minute
)

type metricSample struct {
	BytesIn  uint64 `json:"bytes_in"`
	BytesOut uint64 `json:"bytes_out"`
	Requests uint64 `json:"requests"`
}

type routeMetrics struct {
	machine string
	route   string
	rType   string

	requests    atomic.Uint64
	bytesIn     atomic.Uint64
	bytesOut    atomic.Uint64
	totalConns  atomic.Uint64
	activeConns atomic.Int64

	latencyCount atomic.Uint64
	latencySumNs atomic.Uint64
	latencyMaxNs atomic.Uint64

	sampleMu    sync.Mutex
	samples     [metricsBuckets]metricSample
	head        int
	bucketStart time.Time
}

// Registry tracks per-route traffic counters, sparkline buckets, and HTTP
// latency stats. All methods are safe for concurrent use.
type Registry struct {
	mu     sync.RWMutex
	routes map[string]*routeMetrics
}

func NewRegistry() *Registry {
	return &Registry{routes: make(map[string]*routeMetrics)}
}

func (r *Registry) get(machine, route, rType string) *routeMetrics {
	r.mu.RLock()
	m, ok := r.routes[route]
	r.mu.RUnlock()
	if ok {
		return m
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.routes[route]; ok {
		return existing
	}
	m = &routeMetrics{
		machine:     machine,
		route:       route,
		rType:       rType,
		bucketStart: time.Now(),
	}
	r.routes[route] = m
	return m
}

// RecordHTTPRequest counts one HTTP request/response cycle and its duration.
func (r *Registry) RecordHTTPRequest(machine, route string, dur time.Duration) {
	m := r.get(machine, route, "http")
	m.requests.Add(1)
	recordLatency(m, dur)
	m.recordSample(0, 0, 1)
}

// RecordBytesIn counts bytes flowing from the public client toward the backend
// (server → tunnel direction).
func (r *Registry) RecordBytesIn(machine, route, rType string, n int) {
	if n <= 0 {
		return
	}
	m := r.get(machine, route, rType)
	m.bytesIn.Add(uint64(n))
	m.recordSample(uint64(n), 0, 0)
}

// RecordBytesOut counts bytes flowing from the backend toward the public client
// (tunnel → server direction).
func (r *Registry) RecordBytesOut(machine, route, rType string, n int) {
	if n <= 0 {
		return
	}
	m := r.get(machine, route, rType)
	m.bytesOut.Add(uint64(n))
	m.recordSample(0, uint64(n), 0)
}

// OpenConn marks a long-lived connection (WebSocket session or TCP stream)
// as opened. It also counts as one served request.
func (r *Registry) OpenConn(machine, route, rType string) {
	m := r.get(machine, route, rType)
	m.activeConns.Add(1)
	m.totalConns.Add(1)
	m.requests.Add(1)
	m.recordSample(0, 0, 1)
}

func (r *Registry) CloseConn(machine, route, rType string) {
	m := r.get(machine, route, rType)
	m.activeConns.Add(-1)
}

func recordLatency(m *routeMetrics, d time.Duration) {
	if d <= 0 {
		return
	}
	ns := uint64(d.Nanoseconds())
	m.latencyCount.Add(1)
	m.latencySumNs.Add(ns)
	for {
		old := m.latencyMaxNs.Load()
		if ns <= old {
			return
		}
		if m.latencyMaxNs.CompareAndSwap(old, ns) {
			return
		}
	}
}

func (m *routeMetrics) recordSample(bytesIn, bytesOut, requests uint64) {
	now := time.Now()
	m.sampleMu.Lock()
	defer m.sampleMu.Unlock()
	if m.bucketStart.IsZero() {
		m.bucketStart = now
	}
	elapsed := now.Sub(m.bucketStart)
	bucketsElapsed := int(elapsed / metricsBucketDuration)
	if bucketsElapsed >= metricsBuckets {
		for i := range m.samples {
			m.samples[i] = metricSample{}
		}
		m.head = 0
		m.bucketStart = now
		bucketsElapsed = 0
	}
	for i := 0; i < bucketsElapsed; i++ {
		m.head = (m.head + 1) % metricsBuckets
		m.samples[m.head] = metricSample{}
	}
	if bucketsElapsed > 0 {
		m.bucketStart = m.bucketStart.Add(time.Duration(bucketsElapsed) * metricsBucketDuration)
	}
	m.samples[m.head].BytesIn += bytesIn
	m.samples[m.head].BytesOut += bytesOut
	m.samples[m.head].Requests += requests
}

// RouteSnapshot is the JSON-friendly view of a route's metrics.
type RouteSnapshot struct {
	Machine      string         `json:"machine"`
	Route        string         `json:"route"`
	Type         string         `json:"type"`
	Requests     uint64         `json:"requests"`
	BytesIn      uint64         `json:"bytes_in"`
	BytesOut     uint64         `json:"bytes_out"`
	ActiveConns  int64          `json:"active_conns"`
	TotalConns   uint64         `json:"total_conns"`
	LatencyCount uint64         `json:"latency_count"`
	LatencyAvgMs float64        `json:"latency_avg_ms"`
	LatencyMaxMs float64        `json:"latency_max_ms"`
	Samples      []metricSample `json:"samples"`
}

// Snapshot returns a sorted (most active first) view of all known routes.
func (r *Registry) Snapshot() []RouteSnapshot {
	r.mu.RLock()
	out := make([]RouteSnapshot, 0, len(r.routes))
	for _, m := range r.routes {
		out = append(out, m.snapshot())
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		ai := out[i].BytesIn + out[i].BytesOut + out[i].Requests
		aj := out[j].BytesIn + out[j].BytesOut + out[j].Requests
		if ai != aj {
			return ai > aj
		}
		return out[i].Route < out[j].Route
	})
	return out
}

func (m *routeMetrics) snapshot() RouteSnapshot {
	count := m.latencyCount.Load()
	sumNs := m.latencySumNs.Load()
	maxNs := m.latencyMaxNs.Load()
	avgMs := 0.0
	if count > 0 {
		avgMs = float64(sumNs) / float64(count) / 1e6
	}
	maxMs := float64(maxNs) / 1e6

	m.sampleMu.Lock()
	out := make([]metricSample, metricsBuckets)
	for i := 0; i < metricsBuckets; i++ {
		idx := (m.head + 1 + i) % metricsBuckets
		out[i] = m.samples[idx]
	}
	m.sampleMu.Unlock()

	return RouteSnapshot{
		Machine:      m.machine,
		Route:        m.route,
		Type:         m.rType,
		Requests:     m.requests.Load(),
		BytesIn:      m.bytesIn.Load(),
		BytesOut:     m.bytesOut.Load(),
		ActiveConns:  m.activeConns.Load(),
		TotalConns:   m.totalConns.Load(),
		LatencyCount: count,
		LatencyAvgMs: avgMs,
		LatencyMaxMs: maxMs,
		Samples:      out,
	}
}
