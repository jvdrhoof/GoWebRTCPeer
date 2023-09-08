package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	TilePacketType    uint32 = 1
	ControlPacketType uint32 = 2
)

type RemoteInputPacketHeader struct {
	ClientNr    uint32
	FrameNr     uint32
	TileNr      uint32
	TileLen     uint32
	FrameOffset uint32
	PacketLen   uint32
}

type RemoteTile struct {
	currentLen uint32
	fileLen    uint32
	fileData   []byte
}

type ProxyConnection struct {
	addr             *net.UDPAddr
	conn             *net.UDPConn
	m                sync.RWMutex
	incomplete_tiles map[uint32]map[uint32]RemoteTile
	complete_tiles   map[uint32][]RemoteTile
	frame_counters   map[uint32]uint32
	send_mutex       sync.Mutex
}

type SetupCallback func(int)

func NewProxyConnection() *ProxyConnection {
	return &ProxyConnection{nil, nil, sync.RWMutex{},
		make(map[uint32]map[uint32]RemoteTile), make(map[uint32][]RemoteTile),
		make(map[uint32]uint32), sync.Mutex{}}
}

func (pc *ProxyConnection) sendPacket(b []byte, offset uint32, packet_type uint32) {
	buffProxy := make([]byte, 1300)
	binary.LittleEndian.PutUint32(buffProxy[0:], packet_type)
	copy(buffProxy[4:], b[offset:])
	pc.send_mutex.Lock()
	_, err := pc.conn.WriteToUDP(buffProxy, pc.addr)
	pc.send_mutex.Unlock()
	if err != nil {
		fmt.Printf("WebRTCPeer: ERROR: %s\n", err)
		panic(err)
	}
}

func (pc *ProxyConnection) SetupConnection(port string) {
	address, err := net.ResolveUDPAddr("udp", port)
	if err != nil {
		fmt.Printf("WebRTCPeer: ERROR: %s\n", err)
		return
	}

	// Create a UDP connection
	pc.conn, err = net.ListenUDP("udp", address)
	if err != nil {
		fmt.Printf("WebRTCPeer: ERROR: %s\n", err)
		return
	}

	// Create a buffer to read incoming messages
	buffer := make([]byte, 1500)

	// Wait for incoming messages
	fmt.Println("WebRTCPeer: Waiting for a message...")
	_, pc.addr, err = pc.conn.ReadFromUDP(buffer)
	if err != nil {
		fmt.Printf("WebRTCPeer: ERROR: %s\n", err)
		return
	}

	fmt.Println("WebRTCPeer: Connected to Unity DLL")
}

func (pc *ProxyConnection) StartListening() {
	fmt.Println("WebRTCPeer: Start listening for incoming data from DLL")
	go func() {
		for {
			buffer := make([]byte, 1500)
			_, _, _ = pc.conn.ReadFromUDP(buffer)
			bufBinary := bytes.NewBuffer(buffer[4:28])
			var p RemoteInputPacketHeader
			err := binary.Read(bufBinary, binary.LittleEndian, &p)
			if err != nil {
				fmt.Printf("WebRTCPeer: ERROR: %s\n", err)
				return
			}

			pc.m.Lock()
			_, exists := pc.incomplete_tiles[p.TileNr]
			if !exists {
				pc.incomplete_tiles[p.TileNr] = make(map[uint32]RemoteTile)
			}
			_, exists = pc.incomplete_tiles[p.TileNr][p.FrameNr]
			if !exists {
				r := RemoteTile{
					0,
					p.TileLen,
					make([]byte, p.TileLen),
				}
				pc.incomplete_tiles[p.TileNr][p.FrameNr] = r
			}
			value := pc.incomplete_tiles[p.TileNr][p.FrameNr]
			copy(value.fileData[p.FrameOffset:p.FrameOffset+p.PacketLen], buffer[28:28+p.PacketLen])
			value.currentLen = value.currentLen + p.PacketLen
			pc.incomplete_tiles[p.TileNr][p.FrameNr] = value
			if value.currentLen == value.fileLen {
				fmt.Printf("WebRTCPeer: DLL sent frame %d from tile %d with length %d\n",
					p.FrameNr, p.TileNr, p.TileLen)
				_, exists := pc.complete_tiles[p.TileNr]
				if !exists {
					pc.complete_tiles[p.TileNr] = make([]RemoteTile, 0)
				}
				pc.complete_tiles[p.TileNr] = append(pc.complete_tiles[p.TileNr], value)
				delete(pc.incomplete_tiles[p.TileNr], p.FrameNr)
			}
			pc.m.Unlock()
		}
	}()
}

func (pc *ProxyConnection) SendTilePacket(b []byte, offset uint32) {
	pc.sendPacket(b, offset, TilePacketType)
}

func (pc *ProxyConnection) SendControlPacket(b []byte) {
	pc.sendPacket(b, 0, ControlPacketType)
}

func (pc *ProxyConnection) NextTile(tile uint32) []byte {
	isNextFrameReady := false
	for !isNextFrameReady {
		pc.m.Lock()
		_, exists := pc.complete_tiles[tile]
		if !exists {
			pc.complete_tiles[tile] = make([]RemoteTile, 0)
		}
		if len(pc.complete_tiles[tile]) > 0 {
			isNextFrameReady = true
		} else {
			pc.m.Unlock()
			time.Sleep(time.Millisecond)
		}
	}
	data := pc.complete_tiles[tile][0].fileData
	fmt.Printf("WebRTCPeer: Sending out frame %d from tile %d\n", pc.frame_counters[tile], tile)
	pc.complete_tiles[tile] = pc.complete_tiles[tile][1:]
	pc.frame_counters[tile] += 1
	pc.m.Unlock()
	return data
}
