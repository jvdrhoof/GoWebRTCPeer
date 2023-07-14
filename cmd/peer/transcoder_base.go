package main

import "encoding/binary"

type Frame struct {
	ClientID uint32
	FrameLen uint32
	FrameNr  uint32
	Data     []byte
}

type Transcoder interface {
	UpdateBitrate(bitrate uint32)
	UpdateProjection()
	EncodeFrame() *Frame
	IsReady() bool
}

func (f *Frame) Bytes() []byte {
	offset := 12
	size := offset + len(f.Data)
	bufData := make([]byte, size)
	clientIDBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(clientIDBytes, f.ClientID)
	frameLenBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(frameLenBytes, f.FrameLen)
	frameNrBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(frameNrBytes, f.FrameNr)
	copy(bufData[:], clientIDBytes)
	copy(bufData[4:], frameLenBytes)
	copy(bufData[8:], frameNrBytes)
	copy(bufData[offset:], f.Data)
	return bufData
}
