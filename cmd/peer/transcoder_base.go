package main

type RemoteInputReceivedFrame struct {
	ClientID uint32
	FrameLen uint32
	FrameNr  uint32
	Data     []byte
}

type MultiFrame struct {
	Data []byte
}

type Transcoder interface {
	UpdateBitrate(bitrate uint32)
	UpdateProjection()
	EncodeFrame() *MultiFrame
	IsReady() bool
}
