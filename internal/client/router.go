package client

import (
	"bufio"
	"bytes"
	"io"
	"log"
	"net"
	"net/http"

	"github.com/iluxav/ntunl/internal/config"
	"github.com/iluxav/ntunl/internal/tunnel"
)

func (c *Client) handleHTTPStream(s *tunnel.Stream, route *config.Route) {
	defer c.conn.Mux().CloseStream(s.ID)

	// Collect request data from stream
	var reqBuf bytes.Buffer
	for {
		select {
		case data, ok := <-s.DataCh:
			if !ok {
				return
			}
			reqBuf.Write(data)

			// Try to parse as complete HTTP request
			req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(reqBuf.Bytes())))
			if err != nil {
				continue // need more data
			}

			// Forward to local target
			c.forwardHTTP(s, req, route)
			return

		case <-s.Done:
			return
		}
	}
}

func (c *Client) forwardHTTP(s *tunnel.Stream, req *http.Request, route *config.Route) {
	// Dial the local target
	targetConn, err := net.Dial("tcp", route.Target)
	if err != nil {
		log.Printf("failed to dial %s: %v", route.Target, err)
		// Send 502 back
		resp := &http.Response{
			StatusCode: http.StatusBadGateway,
			Status:     "502 Bad Gateway",
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString("Bad Gateway: target unreachable")),
		}
		var buf bytes.Buffer
		resp.Write(&buf)
		c.conn.Mux().SendData(s.ID, buf.Bytes())
		return
	}
	defer targetConn.Close()

	// Write request to target
	if err := req.Write(targetConn); err != nil {
		log.Printf("failed to write request to %s: %v", route.Target, err)
		return
	}

	// Read response from target and send back through tunnel
	buf := make([]byte, 32*1024)
	for {
		n, err := targetConn.Read(buf)
		if n > 0 {
			if sendErr := c.conn.Mux().SendData(s.ID, buf[:n]); sendErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (c *Client) handleTCPStream(s *tunnel.Stream, route *config.Route) {
	defer c.conn.Mux().CloseStream(s.ID)

	targetConn, err := net.Dial("tcp", route.Target)
	if err != nil {
		log.Printf("failed to dial TCP target %s: %v", route.Target, err)
		return
	}
	defer targetConn.Close()

	done := make(chan struct{})

	// Target → tunnel
	go func() {
		defer close(done)
		buf := make([]byte, 32*1024)
		for {
			n, err := targetConn.Read(buf)
			if n > 0 {
				if sendErr := c.conn.Mux().SendData(s.ID, buf[:n]); sendErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Tunnel → target
	for {
		select {
		case data, ok := <-s.DataCh:
			if !ok {
				return
			}
			if _, err := targetConn.Write(data); err != nil {
				return
			}
		case <-s.Done:
			return
		case <-done:
			return
		}
	}
}
