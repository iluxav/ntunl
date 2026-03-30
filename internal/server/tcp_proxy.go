package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"strings"
	"time"
)

func (s *Server) startTCPListener() {
	cert, err := generateSelfSignedCert()
	if err != nil {
		log.Printf("failed to generate TLS cert for TCP proxy: %v", err)
		return
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	ln, err := tls.Listen("tcp", s.cfg.ListenTCP, tlsConfig)
	if err != nil {
		log.Printf("TCP listener failed: %v", err)
		return
	}
	defer ln.Close()

	log.Printf("TCP/SNI listener on %s", s.cfg.ListenTCP)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("TCP accept error: %v", err)
			continue
		}
		go s.handleTCPConn(conn)
	}
}

func (s *Server) handleTCPConn(c net.Conn) {
	defer c.Close()

	tlsConn, ok := c.(*tls.Conn)
	if !ok {
		log.Printf("non-TLS connection on TCP listener")
		return
	}

	if err := tlsConn.Handshake(); err != nil {
		log.Printf("TLS handshake failed: %v", err)
		return
	}

	sni := tlsConn.ConnectionState().ServerName
	subdomain := extractSubdomainFromSNI(sni)
	if subdomain == "" {
		log.Printf("no SNI subdomain in TCP connection")
		return
	}

	route := s.findRoute(subdomain)
	if route == nil {
		log.Printf("TCP route not found: %s", subdomain)
		return
	}
	if route.Type != "tcp" {
		log.Printf("route %s is not TCP type", subdomain)
		return
	}

	tunnelConn := s.getConn()
	if tunnelConn == nil {
		log.Printf("no tunnel connection for TCP route %s", subdomain)
		return
	}

	stream, err := tunnelConn.Mux().OpenStream(subdomain)
	if err != nil {
		log.Printf("failed to open stream for TCP route %s: %v", subdomain, err)
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

func generateSelfSignedCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"etunl"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"*.etunl.com", "etunl.com"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return tls.X509KeyPair(certPEM, keyPEM)
}

func extractSubdomainFromSNI(sni string) string {
	parts := strings.SplitN(sni, ".", 2)
	if len(parts) < 2 {
		return ""
	}
	return parts[0]
}
