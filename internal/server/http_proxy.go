package server

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"strings"
)

func (s *Server) handleHTTPProxy(w http.ResponseWriter, r *http.Request) {
	subdomain := extractSubdomain(r.Host)
	if subdomain == "" {
		http.Error(w, "no subdomain specified", http.StatusBadRequest)
		return
	}

	route := s.findRoute(subdomain)
	if route == nil {
		http.Error(w, "route not found: "+subdomain, http.StatusNotFound)
		return
	}
	if route.Type != "http" {
		http.Error(w, "route is not HTTP: "+subdomain, http.StatusBadRequest)
		return
	}

	conn := s.getConn()
	if conn == nil {
		http.Error(w, "tunnel not connected", http.StatusBadGateway)
		return
	}

	stream, err := conn.Mux().OpenStream(subdomain)
	if err != nil {
		http.Error(w, "failed to open tunnel stream", http.StatusBadGateway)
		return
	}
	defer conn.Mux().CloseStream(stream.ID)

	// Serialize the HTTP request and send through tunnel
	var reqBuf bytes.Buffer
	if err := r.Write(&reqBuf); err != nil {
		http.Error(w, "failed to serialize request", http.StatusInternalServerError)
		return
	}
	if err := conn.Mux().SendData(stream.ID, reqBuf.Bytes()); err != nil {
		http.Error(w, "failed to send request through tunnel", http.StatusBadGateway)
		return
	}

	// Read response from tunnel
	var respBuf bytes.Buffer
	for {
		select {
		case data, ok := <-stream.DataCh:
			if !ok {
				http.Error(w, "stream closed unexpectedly", http.StatusBadGateway)
				return
			}
			respBuf.Write(data)

			// Try to parse as HTTP response
			resp, err := http.ReadResponse(bufio.NewReader(&respBuf), r)
			if err != nil {
				continue // need more data
			}
			defer resp.Body.Close()

			// Copy response headers
			for k, vv := range resp.Header {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
			return

		case <-stream.Done:
			if respBuf.Len() > 0 {
				// Try one last parse
				resp, err := http.ReadResponse(bufio.NewReader(&respBuf), r)
				if err == nil {
					defer resp.Body.Close()
					for k, vv := range resp.Header {
						for _, v := range vv {
							w.Header().Add(k, v)
						}
					}
					w.WriteHeader(resp.StatusCode)
					io.Copy(w, resp.Body)
					return
				}
			}
			http.Error(w, "tunnel stream closed", http.StatusBadGateway)
			return
		}
	}
}

func extractSubdomain(host string) string {
	// Remove port if present
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	parts := strings.SplitN(host, ".", 2)
	if len(parts) < 2 {
		return ""
	}
	return parts[0]
}
