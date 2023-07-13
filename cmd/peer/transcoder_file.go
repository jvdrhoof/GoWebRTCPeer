package main

type TranscoderFile struct {
	frames       []FileData
	frameCounter uint32
	lEnc         *LayeredEncoder
	isReady      bool
}

func NewTranscoderFile() *TranscoderFile {
	fBytes, _ := ReadBinaryFiles("content", "frame_")
	println("NewTranscoderFile")
	return &TranscoderFile{fBytes, 0, NewLayeredEncoder(), true}
}

func (t *TranscoderFile) UpdateBitrate(bitrate uint32) {
	t.lEnc.Bitrate = bitrate
}

func (t *TranscoderFile) UpdateProjection() {
	// Do nothing
}

func (t *TranscoderFile) EncodeFrame() *MultiFrame {
	var frames []RemoteInputReceivedFrame
	rFrame := RemoteInputReceivedFrame{0, uint32(len(t.frames[t.frameCounter].Data)), t.frameCounter, t.frames[t.frameCounter].Data}
	//rFrame2 := RemoteInputReceivedFrame{1, uint32(len(t.frames[t.frameCounter].Data)), t.frameCounter, t.frames[t.frameCounter].Data}
	frames = append(frames, rFrame)
	mf := t.lEnc.EncodeMultiFrame(frames)
	t.frameCounter = (t.frameCounter + 1) % 900
	return mf
}

func (t *TranscoderFile) IsReady() bool {
	return t.isReady
}
