package main

type PointCloudPacket struct {
	FrameNr uint32
	FrameLen uint32
	SeqOffset uint32
	SeqLen uint32
	Data [1180]byte
}

func NewPointCloudPacket(frameNr, frameLen, seqLen, seqOffset uint32, dataSubArray []byte) (*PointCloudPacket) {
	packet := &PointCloudPacket{
		FrameNr:    frameNr,
		FrameLen:   frameLen,
		SeqOffset:  seqOffset,
		SeqLen:     seqLen,
	}
	copy(packet.Data[:], dataSubArray[seqOffset:(seqOffset+seqLen)])
	return packet
}
 