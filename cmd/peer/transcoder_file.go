package main

type TranscoderFile struct {
	frames       []FileData
	frameCounter uint32
	isReady      bool
	fileCounter  uint32
}

func NewTranscoderFile(contentDirectory string) *TranscoderFile {
	fBytes, _ := ReadBinaryFiles(contentDirectory)
	return &TranscoderFile{fBytes, 0, true, 0}
}

func (t *TranscoderFile) UpdateBitrate(bitrate uint32) {
	// fmt.Printf("A bitrate of %d would have been better\n", bitrate)
}

func (t *TranscoderFile) UpdateProjection() {
	// Do nothing
}

func (t *TranscoderFile) EncodeFrame() *Frame {
	rFrame := Frame{0, uint32(len(t.frames[t.fileCounter].Data)), t.frameCounter, t.frames[t.fileCounter].Data}
	t.frameCounter = (t.frameCounter + 1)
	t.fileCounter = (t.fileCounter + 1) % uint32(len(t.frames))
	return &rFrame
}

func (t *TranscoderFile) IsReady() bool {
	return t.isReady
}
