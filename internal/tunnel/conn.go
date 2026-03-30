package tunnel

import (
	"bytes"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	PingInterval = 15 * time.Second
	PongTimeout  = 30 * time.Second
)

type Conn struct {
	ws   *websocket.Conn
	mux  *Mux
	wmu  sync.Mutex // protects ws writes
	done chan struct{}
}

func NewConn(ws *websocket.Conn, onStream func(*Stream)) *Conn {
	c := &Conn{
		ws:   ws,
		done: make(chan struct{}),
	}
	c.mux = NewMux(c.sendFrame, onStream)
	return c
}

func (c *Conn) Mux() *Mux {
	return c.mux
}

func (c *Conn) sendFrame(f Frame) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.ws.WriteMessage(websocket.BinaryMessage, EncodeFrame(f))
}

func (c *Conn) ReadLoop() error {
	defer c.Close()

	for {
		msgType, msg, err := c.ws.ReadMessage()
		if err != nil {
			return fmt.Errorf("read error: %w", err)
		}
		if msgType != websocket.BinaryMessage {
			continue
		}

		reader := bytes.NewReader(msg)
		for reader.Len() > 0 {
			frame, err := DecodeFrame(reader)
			if err != nil {
				log.Printf("frame decode error: %v", err)
				break
			}
			if err := c.mux.HandleFrame(frame); err != nil {
				log.Printf("frame handle error: %v", err)
			}
		}
	}
}

func (c *Conn) StartPing() {
	go func() {
		ticker := time.NewTicker(PingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := c.sendFrame(Frame{Type: FramePing}); err != nil {
					return
				}
			case <-c.done:
				return
			}
		}
	}()
}

func (c *Conn) Close() {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
	c.mux.CloseAll()
	c.ws.Close()
}

func (c *Conn) Done() <-chan struct{} {
	return c.done
}
