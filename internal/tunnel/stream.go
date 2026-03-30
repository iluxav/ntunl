package tunnel

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
)

type RouteInfo struct {
	Name      string `json:"name"`
	Type      string `json:"type"` // "http" or "tcp"
	LocalPort int    `json:"local_port,omitempty"`
}

type StreamOpenPayload struct {
	Route string `json:"route"`
}

type Stream struct {
	ID     uint32
	Route  string
	DataCh chan []byte
	Done   chan struct{}
	closed atomic.Bool
}

func NewStream(id uint32, route string) *Stream {
	return &Stream{
		ID:     id,
		Route:  route,
		DataCh: make(chan []byte, 64),
		Done:   make(chan struct{}),
	}
}

func (s *Stream) Close() {
	if s.closed.CompareAndSwap(false, true) {
		close(s.Done)
	}
}

type Mux struct {
	mu               sync.RWMutex
	streams          map[uint32]*Stream
	nextID           uint32
	sendFrame        func(Frame) error
	onStream         func(*Stream)          // called when a new stream is opened by remote
	onRouteSync      func(payload []byte)   // called when RouteSync is received
}

func NewMux(sendFrame func(Frame) error, onStream func(*Stream)) *Mux {
	return &Mux{
		streams:   make(map[uint32]*Stream),
		nextID:    1,
		sendFrame: sendFrame,
		onStream:  onStream,
	}
}

func (m *Mux) OpenStream(route string) (*Stream, error) {
	m.mu.Lock()
	id := m.nextID
	m.nextID += 2 // odd IDs for local-initiated
	s := NewStream(id, route)
	m.streams[id] = s
	m.mu.Unlock()

	payload, _ := json.Marshal(StreamOpenPayload{Route: route})
	err := m.sendFrame(Frame{
		Type:     FrameStreamOpen,
		StreamID: id,
		Payload:  payload,
	})
	if err != nil {
		m.removeStream(id)
		return nil, fmt.Errorf("failed to send stream open: %w", err)
	}

	return s, nil
}

func (m *Mux) HandleFrame(f Frame) error {
	switch f.Type {
	case FrameStreamOpen:
		var payload StreamOpenPayload
		if err := json.Unmarshal(f.Payload, &payload); err != nil {
			return fmt.Errorf("invalid stream open payload: %w", err)
		}
		s := NewStream(f.StreamID, payload.Route)
		m.mu.Lock()
		m.streams[f.StreamID] = s
		m.mu.Unlock()
		if m.onStream != nil {
			go m.onStream(s)
		}

	case FrameStreamData:
		m.mu.RLock()
		s, ok := m.streams[f.StreamID]
		m.mu.RUnlock()
		if !ok {
			return nil // stream already closed, ignore
		}
		select {
		case s.DataCh <- f.Payload:
		case <-s.Done:
		}

	case FrameStreamClose:
		m.mu.RLock()
		s, ok := m.streams[f.StreamID]
		m.mu.RUnlock()
		if ok {
			s.Close()
			m.removeStream(f.StreamID)
		}

	case FrameRouteSync:
		if m.onRouteSync != nil {
			m.onRouteSync(f.Payload)
		}

	case FramePing:
		return m.sendFrame(Frame{Type: FramePong, StreamID: f.StreamID})

	case FramePong:
		// no-op

	default:
		// ignore unknown frame types for forward compatibility
	}

	return nil
}

func (m *Mux) SendData(streamID uint32, data []byte) error {
	return m.sendFrame(Frame{
		Type:     FrameStreamData,
		StreamID: streamID,
		Payload:  data,
	})
}

func (m *Mux) CloseStream(streamID uint32) {
	m.mu.RLock()
	s, ok := m.streams[streamID]
	m.mu.RUnlock()
	if ok {
		s.Close()
		m.removeStream(streamID)
		_ = m.sendFrame(Frame{
			Type:     FrameStreamClose,
			StreamID: streamID,
		})
	}
}

func (m *Mux) SetRouteSyncHandler(handler func(payload []byte)) {
	m.onRouteSync = handler
}

func (m *Mux) SendRouteSync(routes []RouteInfo) error {
	payload, err := json.Marshal(routes)
	if err != nil {
		return err
	}
	return m.sendFrame(Frame{
		Type:    FrameRouteSync,
		Payload: payload,
	})
}

func (m *Mux) CloseAll() {
	m.mu.Lock()
	for id, s := range m.streams {
		s.Close()
		delete(m.streams, id)
	}
	m.mu.Unlock()
}

func (m *Mux) removeStream(id uint32) {
	m.mu.Lock()
	delete(m.streams, id)
	m.mu.Unlock()
}
