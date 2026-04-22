package server

import (
	"context"
	"fmt"
	"log"
	"net"
)

// syncTCPListeners starts listeners for TCP routes claimed by any connected
// client, and stops listeners for routes that no client owns anymore.
func (s *Server) syncTCPListeners() {
	s.mu.Lock()
	defer s.mu.Unlock()

	tcpRoutes := map[string]bool{}
	for _, entry := range s.clients {
		for _, r := range entry.routes {
			if r.Type == "tcp" {
				tcpRoutes[r.Name] = true
			}
		}
	}

	// Stop listeners for removed routes
	for name, tl := range s.tcpListeners {
		if !tcpRoutes[name] {
			log.Printf("stopping TCP listener for %s on port %d", name, tl.port)
			tl.cancel()
			tl.listener.Close()
			delete(s.tcpListeners, name)
		}
	}

	// Start listeners for new routes
	for name := range tcpRoutes {
		if _, exists := s.tcpListeners[name]; exists {
			continue
		}
		port, err := s.allocatePort(name)
		if err != nil {
			log.Printf("failed to allocate port for %s: %v", name, err)
			continue
		}
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			log.Printf("failed to start TCP listener for %s on port %d: %v", name, port, err)
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.tcpListeners[name] = &tcpListener{
			port:     port,
			listener: ln,
			cancel:   cancel,
		}
		log.Printf("TCP listener for route %q on port %d", name, port)
		go s.serveTCP(ctx, ln, name)
	}
}

func (s *Server) serveTCP(ctx context.Context, ln net.Listener, routeName string) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				log.Printf("TCP accept error for %s: %v", routeName, err)
				continue
			}
		}
		go s.handlePlainTCPConn(conn, routeName)
	}
}

func (s *Server) handlePlainTCPConn(c net.Conn, routeName string) {
	defer c.Close()

	route, tunnelConn := s.findRoute(routeName)
	if route == nil || tunnelConn == nil {
		log.Printf("no tunnel connection for TCP route %s", routeName)
		return
	}

	stream, err := tunnelConn.Mux().OpenStream(routeName)
	if err != nil {
		log.Printf("failed to open stream for TCP route %s: %v", routeName, err)
		return
	}
	defer tunnelConn.Mux().CloseStream(stream.ID)

	done := make(chan struct{})

	// TCP conn → tunnel
	go func() {
		defer close(done)
		buf := make([]byte, 32*1024)
		for {
			n, err := c.Read(buf)
			if n > 0 {
				if sendErr := tunnelConn.Mux().SendData(stream.ID, buf[:n]); sendErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Tunnel → TCP conn
	for {
		select {
		case data, ok := <-stream.DataCh:
			if !ok {
				return
			}
			if _, err := c.Write(data); err != nil {
				return
			}
		case <-stream.Done:
			return
		case <-done:
			return
		}
	}
}
