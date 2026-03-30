package tunnel

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	FrameStreamOpen  byte = 0x01
	FrameStreamData  byte = 0x02
	FrameStreamClose byte = 0x03
	FrameRouteSync   byte = 0x04
	FramePing        byte = 0x05
	FramePong        byte = 0x06

	MaxFrameSize = 1024 * 1024 // 1MB
	HeaderSize   = 9           // 1 + 4 + 4
)

type Frame struct {
	Type     byte
	StreamID uint32
	Payload  []byte
}

func EncodeFrame(f Frame) []byte {
	buf := make([]byte, HeaderSize+len(f.Payload))
	buf[0] = f.Type
	binary.BigEndian.PutUint32(buf[1:5], f.StreamID)
	binary.BigEndian.PutUint32(buf[5:9], uint32(len(f.Payload)))
	copy(buf[9:], f.Payload)
	return buf
}

func DecodeFrame(r io.Reader) (Frame, error) {
	header := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return Frame{}, err
	}

	f := Frame{
		Type:     header[0],
		StreamID: binary.BigEndian.Uint32(header[1:5]),
	}
	length := binary.BigEndian.Uint32(header[5:9])

	if length > MaxFrameSize {
		return Frame{}, fmt.Errorf("frame too large: %d bytes", length)
	}

	if length > 0 {
		f.Payload = make([]byte, length)
		if _, err := io.ReadFull(r, f.Payload); err != nil {
			return Frame{}, err
		}
	}

	return f, nil
}
