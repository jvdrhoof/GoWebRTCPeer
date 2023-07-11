package main

type WebsocketPacket struct {
	ClientID   uint32
	PacketType uint32
	Payload    []byte
}
