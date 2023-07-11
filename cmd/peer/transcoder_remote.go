package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
)

type TranscoderRemote struct {
	frames       map[uint32]RemoteFrame
	frameCounter uint32
	lEnc         *LayeredEncoder
	isReady      bool
	m            sync.RWMutex
}

type RemoteFrame struct {
	currentLen uint32
	frameLen   uint32
	frameData  []byte
}

type RemoteInputPacketHeader struct {
	Framenr     uint32
	Framelen    uint32
	Frameoffset uint32
	Packetlen   uint32
}

func NewTranscoderRemote() *TranscoderRemote {
	t := TranscoderRemote{make(map[uint32]RemoteFrame), 0, NewLayeredEncoder(), false, sync.RWMutex{}}
	t.StartListening()
	return &t
}

func (t *TranscoderRemote) UpdateBitrate(bitrate uint32) {
	t.lEnc.Bitrate = bitrate
}

func (t *TranscoderRemote) UpdateProjection() {
	// Do nothing
}

func (t *TranscoderRemote) EncodeFrame() *MultiFrame {
	var frames []RemoteInputReceivedFrame
	isNextFrameReady := false
	for !isNextFrameReady {
		t.m.Lock()
		value, exists := t.frames[t.frameCounter]
		if exists && value.currentLen == value.frameLen {
			isNextFrameReady = true
		}
		t.m.Unlock()
	}
	t.m.Lock()
	frame, _ := t.frames[t.frameCounter]
	rFrame := RemoteInputReceivedFrame{0, frame.frameLen, t.frameCounter, frame.frameData}
	delete(t.frames, t.frameCounter)
	t.m.Unlock()
	frames = append(frames, rFrame)
	mf := t.lEnc.EncodeMultiFrame(frames)
	if t.frameCounter%100 == 0 {
		println("SENDING FRAME ", t.frameCounter)
	}
	t.frameCounter = t.frameCounter + 1
	return mf
}

func (t *TranscoderRemote) StartListening() {
	go func() {
		address, err := net.ResolveUDPAddr("udp", ":8001")
		if err != nil {
			fmt.Println("Error resolving address:", err)
			return
		}

		// Create a UDP connection
		conn, err := net.ListenUDP("udp", address)
		if err != nil {
			fmt.Println("Error listening:", err)
			return
		}
		conn.SetReadBuffer(524288000)
		defer conn.Close()

		// Create a buffer to read incoming messages
		buffer := make([]byte, 1500)

		// Wait for incoming messages
		fmt.Println("Waiting for a message...")
		_, addr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			fmt.Println("Error reading:", err)
			return
		}
		str := "Hello!"
		byteArray := make([]byte, 1500)
		copy(byteArray[:], str)
		//byteArray[len(str)] = 0
		_, err = conn.WriteToUDP(byteArray, addr)
		if err != nil {
			fmt.Println("Error sending response:", err)
			return
		}
		t.isReady = true
		for {
			buffer := make([]byte, 1500)
			_, _, _ = conn.ReadFromUDP(buffer)
			bufBinary := bytes.NewBuffer(buffer[4:20])
			// Read the fields from the buffer into a struct
			var p RemoteInputPacketHeader
			err := binary.Read(bufBinary, binary.LittleEndian, &p)
			if err != nil {
				fmt.Println("Error:", err)
				return
			}
			t.m.Lock()
			_, exists := t.frames[p.Framenr]
			if !exists {
				r := RemoteFrame{
					0,
					p.Framelen,
					make([]byte, p.Framelen),
				}
				t.frames[p.Framenr] = r
			}
			value, _ := t.frames[p.Framenr]

			copy(value.frameData[p.Frameoffset:p.Frameoffset+p.Packetlen], buffer[20:20+p.Packetlen])
			value.currentLen = value.currentLen + p.Packetlen
			t.frames[p.Framenr] = value
			if value.currentLen == value.frameLen && p.Framenr%100 == 0 {
				println("FRAME ", p.Framenr, " COMPLETE")
			}
			//println(p.Frameoffset, p.Framenr, value.currentLen, p.Framelen)
			t.m.Unlock()

		}
	}()
}

func (t *TranscoderRemote) IsReady() bool {
	return t.isReady
}
