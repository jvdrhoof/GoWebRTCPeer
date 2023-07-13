package main

type WebsocketPacket struct {
	ClientID    uint64
	MessageType uint64
	Message     string
}
