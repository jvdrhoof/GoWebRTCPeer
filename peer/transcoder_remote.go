package main

type TranscoderRemote struct {
	proxy_con    *ProxyConnection
	frameCounter uint32
	isReady      bool
}

func NewTranscoderRemote(proxy_con *ProxyConnection) *TranscoderRemote {
	return &TranscoderRemote{proxy_con, 0, true}
}

func (t *TranscoderRemote) UpdateBitrate(bitrate uint32) {
	// fmt.Printf("A bitrate of %d would have been better\n", bitrate)
}

func (t *TranscoderRemote) UpdateProjection() {
	// Do nothing
}

func (t *TranscoderRemote) EncodeFrame() *Frame {
	data := proxy_conn.NextFrame()
	rFrame := Frame{0, uint32(len(data)), t.frameCounter, data}
	t.frameCounter++
	return &rFrame
}

func (t *TranscoderRemote) IsReady() bool {
	return t.isReady
}
