package main

import (
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
)

type WsCallback func(WebsocketPacket)

type WSHandler struct {
	conn *websocket.Conn
}

func NewWSHandler(addr string) *WSHandler {
	u := url.URL{Scheme: "ws", Host: addr, Path: "/"}
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	return &WSHandler{conn}
}

func (w *WSHandler) StartListening(cb WsCallback) {
	go func() {
		//defer close(done)
		for {
			_, message, err := w.conn.ReadMessage()
			if err != nil {
				panic(err)
			}
			v := strings.Split(string(message), "@")
			clientID, _ := strconv.ParseUint(v[0], 10, 64)
			messageType, _ := strconv.ParseUint(v[1], 10, 64)
			wsPacket := WebsocketPacket{clientID, messageType, v[2]}
			/* bufBinary := bytes.NewBuffer(message)
			err = binary.Read(bufBinary, binary.LittleEndian, &wsPacket)
			if err != nil {
				panic(err)
			}*/
			println("recv: ", wsPacket.Message)
			cb(wsPacket)
		}
	}()

}

// 32@5@test
func (w *WSHandler) SendMessage(wsPacket WebsocketPacket) {
	/*buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.LittleEndian, wsPacket); err != nil {
		panic(err)
	}*/
	s := fmt.Sprintf("%d@%d@%s", wsPacket.ClientID, wsPacket.MessageType, wsPacket.Message)
	err := w.conn.WriteMessage(websocket.TextMessage, []byte(s))
	if err != nil {
		panic(err)
	}
}
