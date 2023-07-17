package main

import (
	"bytes"
	"encoding/binary"
)

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

type TranscoderFiles struct {
	frames       []FileData
	frameCounter uint32
	isReady      bool
	fileCounter  uint32
}

func (f *Frame) Bytes() []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, f.ClientID)
	binary.Write(buf, binary.BigEndian, f.FrameLen)
	binary.Write(buf, binary.BigEndian, f.FrameNr)
	binary.Write(buf, binary.BigEndian, f.Data)
	return buf.Bytes()
}

func NewTranscoderFile(contentDirectory string) *TranscoderFiles {
	fBytes, _ := ReadBinaryFiles(contentDirectory)
	return &TranscoderFiles{fBytes, 0, true, 0}
}

func (t *TranscoderFiles) UpdateBitrate(bitrate uint32) {
	// Do nothing
}

func (t *TranscoderFiles) UpdateProjection() {
	// Do nothing
}

func (t *TranscoderFiles) EncodeFrame() *Frame {
	rFrame := Frame{0, uint32(len(t.frames[t.fileCounter].Data)), t.frameCounter, t.frames[t.fileCounter].Data}
	t.frameCounter = (t.frameCounter + 1)
	t.fileCounter = (t.fileCounter + 1) % uint32(len(t.frames))
	return &rFrame
}

func (t *TranscoderFiles) IsReady() bool {
	return t.isReady
}
