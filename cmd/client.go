// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

// pion-to-pion is an example of two pion instances communicating directly!
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/nack"
	"github.com/pion/interceptor/pkg/twcc"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v3"
)

func signalCandidate(addr string, c *webrtc.ICECandidate) error {
	payload := []byte(c.ToJSON().Candidate)
	resp, err := http.Post(fmt.Sprintf("http://%s/candidate", addr),
		"application/json; charset=utf-8", bytes.NewReader(payload))
	if err != nil {
		return err
	}

	return resp.Body.Close()
}

func main() {

	// FUT: websockets for real-time signaling

	offerAddr := flag.String("offer-address", "127.0.0.1:7002", "Address that the Offer HTTP server is hosted on.")
	answerAddr := flag.String("answer-address", ":8001", "Address that the Answer HTTP server is hosted on.")
	useVirtualWall := flag.Bool("v", false, "Use virtual wall ip filter")
	useProxy := flag.Bool("p", false, "Forward packets to remote client")
	flag.Parse()
	var addr *net.UDPAddr
	var conn *net.UDPConn

	// If a proxy is used, incoming packets should be forwarded
	if *useProxy {
		// Get the current address
		address, err := net.ResolveUDPAddr("udp", ":8000")
		if err != nil {
			fmt.Println("Error resolving address:", err)
			return
		}

		// Create a UDP connection
		conn, err = net.ListenUDP("udp", address)
		if err != nil {
			fmt.Println("Error listening:", err)
			return
		}

		// Defer function, executed once the main is completed
		defer func() {
			println("Closing connection")
			conn.Close()
		}()

		// Create a buffer to read incoming messages
		buffer := make([]byte, 1500)

		// Wait for incoming messages
		fmt.Println("Waiting for a message...")
		var n int
		n, addr, err = conn.ReadFromUDP(buffer)
		if err != nil {
			fmt.Println("Error reading:", err)
			return
		}

		// Print the received message
		fmt.Println("Received message:", string(buffer[:n]))
	}

	// Set WebRTC engine
	settingEngine := webrtc.SettingEngine{}
	settingEngine.SetSCTPMaxReceiveBufferSize(160 * 1024 * 1024)
	if *useVirtualWall {
		settingEngine.SetIPFilter(VirtualWallFilter)
	}

	m := &webrtc.MediaEngine{}
	i := &interceptor.Registry{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		panic(err)
	}
	videoRTCPFeedback := []webrtc.RTCPFeedback{{"goog-remb", ""}, {"ccm", "fir"}, {"nack", ""}, {"nack", "pli"}}
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{"video/pcm", 90000, 0, "", videoRTCPFeedback},
		PayloadType:        5,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		panic(err)
	}

	m.RegisterFeedback(webrtc.RTCPFeedback{Type: webrtc.TypeRTCPFBTransportCC}, webrtc.RTPCodecTypeVideo)
	if err := m.RegisterHeaderExtension(webrtc.RTPHeaderExtensionCapability{URI: sdp.TransportCCURI}, webrtc.RTPCodecTypeVideo); err != nil {
		panic(err)
	}

	m.RegisterFeedback(webrtc.RTCPFeedback{Type: webrtc.TypeRTCPFBTransportCC}, webrtc.RTPCodecTypeAudio)
	if err := m.RegisterHeaderExtension(webrtc.RTPHeaderExtensionCapability{URI: sdp.TransportCCURI}, webrtc.RTPCodecTypeAudio); err != nil {
		panic(err)
	}

	generator, err := twcc.NewSenderInterceptor(twcc.SendInterval(10 * time.Millisecond))
	if err != nil {
		panic(err)
	}

	i.Add(generator)

	// TODO: find out difference between nack and nack/pli
	nackGenerator, _ := nack.NewGeneratorInterceptor()
	i.Add(nackGenerator)

	m.RegisterFeedback(webrtc.RTCPFeedback{Type: "nack"}, webrtc.RTPCodecTypeVideo)
	m.RegisterFeedback(webrtc.RTCPFeedback{Type: "nack", Parameter: "pli"}, webrtc.RTPCodecTypeVideo)

	var candidatesMux sync.Mutex
	pendingCandidates := make([]*webrtc.ICECandidate, 0)

	peerConnection, err := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine), webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(i)).NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				// URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	})
	if err != nil {
		panic(err)
	}

	defer func() {
		if err := peerConnection.Close(); err != nil {
			fmt.Printf("Cannot close peer connection: %v\n", err)
		}
	}()

	// When an ICE candidate is available send to the other Pion instance
	// the other Pion instance will add this candidate by calling AddICECandidate
	peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}

		candidatesMux.Lock()
		defer candidatesMux.Unlock()

		// If the other peer's remote description is not available, the list of pending local candidates is extended
		// If it is available, immediately signal local candidate to the other peer
		desc := peerConnection.RemoteDescription()
		if desc == nil {
			pendingCandidates = append(pendingCandidates, c)
		} else if onICECandidateErr := signalCandidate(*offerAddr, c); onICECandidateErr != nil {
			panic(onICECandidateErr)
		}
	})

	// A HTTP handler that allows the other Pion instance to send us ICE candidates
	// This allows us to add ICE candidates faster, we don't have to wait for STUN or TURN
	// candidates which may be slower

	// Handle incoming candidates
	http.HandleFunc("/candidate", func(w http.ResponseWriter, r *http.Request) {
		candidate, candidateErr := ioutil.ReadAll(r.Body)
		if candidateErr != nil {
			panic(candidateErr)
		}
		if candidateErr := peerConnection.AddICECandidate(webrtc.ICECandidateInit{Candidate: string(candidate)}); candidateErr != nil {
			panic(candidateErr)
		}
	})

	// A HTTP handler that processes a SessionDescription given to us from the other Pion process
	http.HandleFunc("/sdp", func(w http.ResponseWriter, r *http.Request) {
		sdp := webrtc.SessionDescription{}
		if err := json.NewDecoder(r.Body).Decode(&sdp); err != nil {
			panic(err)
		}

		if err := peerConnection.SetRemoteDescription(sdp); err != nil {
			panic(err)
		}

		// Create an answer to send to the other process
		answer, err := peerConnection.CreateAnswer(nil)
		if err != nil {
			panic(err)
		}

		// Send our answer to the HTTP server listening in the other process
		payload, err := json.Marshal(answer)
		if err != nil {
			panic(err)
		}
		resp, err := http.Post(fmt.Sprintf("http://%s/sdp", *offerAddr), "application/json; charset=utf-8", bytes.NewReader(payload)) // nolint:noctx
		if err != nil {
			panic(err)
		} else if closeErr := resp.Body.Close(); closeErr != nil {
			panic(closeErr)
		}

		// Sets the LocalDescription, and starts our UDP listeners
		err = peerConnection.SetLocalDescription(answer)
		if err != nil {
			panic(err)
		}

		candidatesMux.Lock()
		for _, c := range pendingCandidates {
			onICECandidateErr := signalCandidate(*offerAddr, c)
			if onICECandidateErr != nil {
				panic(onICECandidateErr)
			}
		}
		candidatesMux.Unlock()
	})

	// Set the handler for Peer connection state
	// This will notify you when the peer has connected/disconnected
	peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		fmt.Printf("Peer Connection State has changed: %s\n", s.String())

		if s == webrtc.PeerConnectionStateFailed {
			// Wait until PeerConnection has had no network activity for 30 seconds or another failure. It may be reconnected using an ICE Restart.
			// Use webrtc.PeerConnectionStateDisconnected if you are interested in detecting faster timeout.
			// Note that the PeerConnection may come back from PeerConnectionStateDisconnected.
			fmt.Println("Peer Connection has gone to failed exiting")
			os.Exit(0)
		}
	})

	// TODO: add support for custom media format
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		println("TRACK")
		println("Codec ", track.Codec().MimeType)
		println("TYPE ", track.PayloadType())

		codecName := strings.Split(track.Codec().RTPCodecCapability.MimeType, "/")
		fmt.Printf("Track has started, of type %d: %s \n", track.PayloadType(), codecName)

		// Create buffer to receive incoming track data, using 1300 bytes - header bytes
		buf := make([]byte, 1220)

		// Allows to check if frames are received completely
		// Frame number and corresponding length
		frames := make(map[uint32]uint32)

		/*
			oldEpochMilliseconds := time.Now().UnixNano() / int64(time.Millisecond)
			msCounter := int(0)
			bw := 0
		*/

		for {
			_, _, readErr := track.Read(buf)
			if readErr != nil {
				panic(err)
			}
			/*
				epochMilliseconds := time.Now().UnixNano() / int64(time.Millisecond)
			*/

			if *useProxy {
				// TODO: Use bufBinary and make plugin buffer size as parameter
				buffProxy := make([]byte, 1300)
				copy(buffProxy, buf[20:])
				_, err = conn.WriteToUDP(buffProxy, addr)
				if err != nil {
					panic(err)
				}
			}

			// Create a buffer from the byte array, skipping the first 20 WebRTC bytes
			// TODO: mention WebRTC header content explicitly
			bufBinary := bytes.NewBuffer(buf[20:])

			// Read the fields from the buffer into a struct
			var p PointCloudPacket
			err := binary.Read(bufBinary, binary.LittleEndian, &p)
			if err != nil {
				panic(err)
			}
			frames[p.FrameNr] += p.SeqLen
			if frames[p.FrameNr] == p.FrameLen && p.FrameNr%100 == 0 {
				println("FRAME COMPLETE ", p.FrameNr, p.FrameLen)
			}

			// FUT: provide a debug log level
			/*
				bw += int(p.SeqLen + 20)
				msCounter += int(epochMilliseconds - oldEpochMilliseconds)
				if uint64(msCounter/1000) > 0 {
					msCounter = msCounter - 1000
					bw = 0
				}
				oldEpochMilliseconds = epochMilliseconds
				// Print the struct
				//	println(p.FrameNr, p.FrameLen, p.SeqNr, p.SeqLen, p.SeqOffset)
			*/
		}
	})
	// nolint: gosec
	panic(http.ListenAndServe(*answerAddr, nil))

}

// Use local IP address to connect to other peer
func VirtualWallFilter(addr net.IP) bool {
	return addr.String() == "192.168.1.2"
}
