package server

import (
	"bufio"
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
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		if err := r.Write(pw); err != nil {
			return
		}
	}()

	// Send request body through tunnel
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := pr.Read(buf)
			if n > 0 {
				if sendErr := conn.Mux().SendData(stream.ID, buf[:n]); sendErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Bridge stream data channel into an io.Reader via pipe
	respPR, respPW := io.Pipe()
	go func() {
		defer respPW.Close()
		for {
			select {
			case data, ok := <-stream.DataCh:
				if !ok {
					return
				}
				if _, err := respPW.Write(data); err != nil {
					return
				}
			case <-stream.Done:
				return
			}
		}
	}()

	// Parse HTTP response from the pipe reader
	resp, err := http.ReadResponse(bufio.NewReader(respPR), r)
	if err != nil {
		http.Error(w, "bad gateway: "+err.Error(), http.StatusBadGateway)
		return
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
}

func extractSubdomain(host string) string {
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	parts := strings.SplitN(host, ".", 2)
	if len(parts) < 2 {
		return ""
	}
	return parts[0]
}
