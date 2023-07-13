package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/cc"
	"github.com/pion/interceptor/pkg/gcc"
	"github.com/pion/interceptor/pkg/nack"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

const (
	Idle     int = 0
	Hello        = 1
	Offer        = 2
	Answer       = 3
	Ready        = 4
	Finished     = 5
)

func main() {
	offerAddr := flag.String("offer-address", ":7002", "Address that the Offer HTTP server is hosted on.")
	answerAddr := flag.String("r", "127.0.0.1:8001", "Address that the Answer HTTP server is hosted on.")
	useFiles := flag.Bool("f", false, "Use pre encoded files instead or remote input")
	useVirtualWall := flag.Bool("v", false, "Use virtual wall ip filter")

	println(offerAddr, answerAddr, useFiles, useVirtualWall)

	var transcoder Transcoder
	transcoder = NewTranscoderFile()
	for !transcoder.IsReady() {
		time.Sleep(10 * time.Millisecond)
	}

	settingEngine := webrtc.SettingEngine{}
	settingEngine.SetSCTPMaxReceiveBufferSize(16 * 1024 * 1024)

	i := &interceptor.Registry{}
	m := &webrtc.MediaEngine{}
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

	congestionController, err := cc.NewInterceptor(func() (cc.BandwidthEstimator, error) {
		return gcc.NewSendSideBWE(gcc.SendSideBWEMinBitrate(75_000*8), gcc.SendSideBWEInitialBitrate(75_000_000), gcc.SendSideBWEMaxBitrate(262_744_320))
	})
	if err != nil {
		panic(err)
	}

	estimatorChan := make(chan cc.BandwidthEstimator, 1)
	congestionController.OnNewPeerConnection(func(id string, estimator cc.BandwidthEstimator) {
		estimatorChan <- estimator
	})

	i.Add(congestionController)
	if err = webrtc.ConfigureTWCCHeaderExtensionSender(m, i); err != nil {
		panic(err)
	}

	responder, _ := nack.NewResponderInterceptor()
	m.RegisterFeedback(webrtc.RTCPFeedback{Type: "nack"}, webrtc.RTPCodecTypeVideo)
	m.RegisterFeedback(webrtc.RTCPFeedback{Type: "nack", Parameter: "pli"}, webrtc.RTPCodecTypeVideo)
	i.Add(responder)

	var candidatesMux sync.Mutex
	pendingCandidates := make([]*webrtc.ICECandidate, 0)
	peerConnection, err := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine), webrtc.WithInterceptorRegistry(i), webrtc.WithMediaEngine(m)).NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	})

	videoTrack, err := NewTrackLocalCloudRTP(webrtc.RTPCodecCapability{"video/pcm", 90000, 0, "", nil}, "video", "pion")
	if err != nil {
		panic(err)
	}

	// RTP Sender
	rtpSender, err := peerConnection.AddTrack(videoTrack)
	if err != nil {
		panic(err)
	}

	defer func() {
		if cErr := peerConnection.Close(); cErr != nil {
			fmt.Printf("Cannot close peer connection: %v\n", cErr)
		}
	}()

	estimator := <-estimatorChan
	go func() {
		rtcpBuf := make([]byte, 1500)
		for {
			if _, _, err := rtpSender.Read(rtcpBuf); err != nil {
				panic(err)
			}
		}
	}()

	wsHandler := NewWSHandler("193.190.127.152:8000")

	peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		candidatesMux.Lock()
		desc := peerConnection.RemoteDescription()
		if desc == nil {
			pendingCandidates = append(pendingCandidates, c)
		} else {
			payload := []byte(c.ToJSON().Candidate)
			wsHandler.SendMessage(WebsocketPacket{1, 4, string(payload)})
		}
		candidatesMux.Unlock()
	})

	peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		fmt.Printf("Peer Connection State has changed: %s\n", s.String())
		if s == webrtc.PeerConnectionStateFailed {
			fmt.Println("Peer Connection has gone to failed exiting")
			os.Exit(0)
		}
	})

	var state = Idle
	print(state)

	var handleMessageCallback = func(wsPacket WebsocketPacket) {
		switch wsPacket.MessageType {
		case 1: // hello
			println("Received hello")
			offer, err := peerConnection.CreateOffer(nil)
			if err != nil {
				panic(err)
			}
			if err = peerConnection.SetLocalDescription(offer); err != nil {
				panic(err)
			}
			payload, err := json.Marshal(offer)
			if err != nil {
				panic(err)
			}
			wsHandler.SendMessage(WebsocketPacket{1, 2, string(payload)})
			state = Hello
		case 2: // offer
			println("Received offer")
			offer := webrtc.SessionDescription{}
			err := json.Unmarshal([]byte(wsPacket.Message), &offer)
			if err != nil {
				panic(err)
			}
			peerConnection.SetRemoteDescription(offer)
			answer, err := peerConnection.CreateAnswer(nil)
			if err != nil {
				panic(err)
			}
			if err = peerConnection.SetLocalDescription(answer); err != nil {
				panic(err)
			}
			payload, err := json.Marshal(answer)
			if err != nil {
				panic(err)
			}
			wsHandler.SendMessage(WebsocketPacket{1, 3, string(payload)})
			state = Offer
		case 3: // answer
			println("Received answer")
			answer := webrtc.SessionDescription{}
			err := json.Unmarshal([]byte(wsPacket.Message), &answer)
			if err != nil {
				panic(err)
			}
			if err := peerConnection.SetRemoteDescription(answer); err != nil {
				panic(err)
			}
			candidatesMux.Lock()
			for _, c := range pendingCandidates {
				payload := []byte(c.ToJSON().Candidate)
				wsHandler.SendMessage(WebsocketPacket{1, 4, string(payload)})
			}
			candidatesMux.Unlock()
			state = Answer
		case 4: // candidate
			println("Received candidate")
			candidate := wsPacket.Message
			if candidateErr := peerConnection.AddICECandidate(webrtc.ICECandidateInit{Candidate: candidate}); candidateErr != nil {
				panic(candidateErr)
			}
		default:
			println(fmt.Sprintf("Received non-compliant message type %d", wsPacket.MessageType))
		}
	}

	wsHandler.StartListening(handleMessageCallback)
	wsHandler.SendMessage(WebsocketPacket{1, 1, "Hello"})

	select {}

	oldEpochMilliseconds := time.Now().UnixNano() / int64(time.Millisecond)
	println(estimator.GetTargetBitrate(), oldEpochMilliseconds)
	msCounter := int(0)
	tFrames := int(0)
	loss := float64(0)
	delayRate := int(0)
	lossRate := int(0)
	for ; true; <-time.NewTicker(20 * time.Millisecond).C {
		tFrames++
		epochMilliseconds := time.Now().UnixNano() / int64(time.Millisecond)
		targetBitrate := uint32(estimator.GetTargetBitrate())
		transcoder.UpdateBitrate(targetBitrate)
		if err = videoTrack.WritePointCloud(transcoder); err != nil {
			panic(err)
		}
		vLossRate, _ := estimator.GetStats()["lossTargetBitrate"]
		vDelayRate, _ := estimator.GetStats()["delayTargetBitrate"]
		vLoss, _ := estimator.GetStats()["averageLoss"]
		lossRate += vLossRate.(int)
		delayRate += vDelayRate.(int)
		loss += vLoss.(float64)
		msCounter += int(epochMilliseconds - oldEpochMilliseconds)
		if uint64(msCounter/1000) > 0 {
			println("MS ", msCounter)

			avgLossRate := lossRate / tFrames
			avgDelayRate := delayRate / tFrames
			avgLoss := loss / float64(tFrames)
			s := fmt.Sprintf("%.2f", avgLoss)
			println(msCounter/tFrames, avgLossRate, avgDelayRate, s)
			msCounter = msCounter - 1000
			delayRate = 0
			lossRate = 0
			loss = 0.0
			tFrames = 0
		}
		oldEpochMilliseconds = epochMilliseconds
	}
	// Block forever
	select {}
}

// TrackLocalStaticRTP  is a TrackLocal that has a pre-set codec and accepts RTP Packets.
// If you wish to send a media.Sample use TrackLocalStaticSample
type TrackLocalCloudRTP struct {
	packetizer rtp.Packetizer
	sequencer  rtp.Sequencer
	rtpTrack   *webrtc.TrackLocalStaticRTP
	clockRate  float64
}

// NewTrackLocalStaticSample returns a TrackLocalStaticSample
func NewTrackLocalCloudRTP(c webrtc.RTPCodecCapability, id, streamID string, options ...func(*webrtc.TrackLocalStaticRTP)) (*TrackLocalCloudRTP, error) {
	rtpTrack, err := webrtc.NewTrackLocalStaticRTP(c, id, streamID, options...)
	if err != nil {
		return nil, err
	}
	return &TrackLocalCloudRTP{
		rtpTrack: rtpTrack,
	}, nil
}

// Bind is called by the PeerConnection after negotiation is complete
// This asserts that the code requested is supported by the remote peer.
// If so it setups all the state (SSRC and PayloadType) to have a call
func (s *TrackLocalCloudRTP) Bind(t webrtc.TrackLocalContext) (webrtc.RTPCodecParameters, error) {
	codec, err := s.rtpTrack.Bind(t)
	if err != nil {
		return codec, err
	}
	//s.rtpTrack.mu.Lock()
	//defer s.rtpTrack.mu.Unlock()

	// We only need one packetizer
	if s.packetizer != nil {
		return codec, nil
	}

	s.sequencer = rtp.NewRandomSequencer()
	s.packetizer = rtp.NewPacketizer(
		1200, // Not MTU but ok
		0,    // Value is handled when writing
		0,    // Value is handled when writing
		NewPointCloudPayloader(),
		s.sequencer,
		codec.ClockRate,
	)

	s.clockRate = float64(codec.RTPCodecCapability.ClockRate)
	return codec, err
}

// Unbind implements the teardown logic when the track is no longer needed. This happens
// because a track has been stopped.
func (s *TrackLocalCloudRTP) Unbind(t webrtc.TrackLocalContext) error {
	return s.rtpTrack.Unbind(t)
}

// ID is the unique identifier for this Track. This should be unique for the
// stream, but doesn't have to globally unique. A common example would be 'audio' or 'video'
// and StreamID would be 'desktop' or 'webcam'
func (s *TrackLocalCloudRTP) ID() string { return s.rtpTrack.ID() }

// StreamID is the group this track belongs too. This must be unique
func (s *TrackLocalCloudRTP) StreamID() string { return s.rtpTrack.StreamID() }

// RID is the RTP stream identifier.
func (s *TrackLocalCloudRTP) RID() string { return s.rtpTrack.RID() }

// Kind controls if this TrackLocal is audio or video
func (s *TrackLocalCloudRTP) Kind() webrtc.RTPCodecType { return s.rtpTrack.Kind() }

// Codec gets the Codec of the track
func (s *TrackLocalCloudRTP) Codec() webrtc.RTPCodecCapability {
	return s.rtpTrack.Codec()
}
func (s *TrackLocalCloudRTP) WritePointCloud(t Transcoder) error {
	//s.rtpTrack.mu.RLock()
	p := s.packetizer
	clockRate := s.clockRate
	//s.rtpTrack.mu.RUnlock()

	if p == nil {
		return nil
	}

	samples := uint32(1 * clockRate)
	frameData := t.EncodeFrame()
	//emptyByteArray := make([]byte, 50_000)
	packets := p.Packetize(frameData.Data, samples)

	writeErrs := []error{}
	counter := 0
	for _, p := range packets {

		if err := s.rtpTrack.WriteRTP(p); err != nil {
			writeErrs = append(writeErrs, err)

		}
		counter += 1
	}
	return nil
}
func smallWait() {
	for start := time.Now().UnixNano(); time.Now().UnixNano() >= start+5000; {
	}
}

// AV1Payloader payloads AV1 packets
type PointCloudPayloader struct {
	frameCounter uint32
}

// Payload fragments a AV1 packet across one or more byte arrays
// See AV1Packet for description of AV1 Payload Header
func (p *PointCloudPayloader) Payload(mtu uint16, payload []byte) (payloads [][]byte) {
	payloadDataOffset := uint32(0)
	payloadLen := uint32(len(payload))
	payloadRemaining := payloadLen
	for payloadRemaining > 0 {
		currentFragmentSize := uint32(1180)
		if payloadRemaining < currentFragmentSize {
			currentFragmentSize = payloadRemaining
		}
		p := NewPointCloudPacket(p.frameCounter, payloadLen, currentFragmentSize, payloadDataOffset, payload)
		buf := new(bytes.Buffer)

		if err := binary.Write(buf, binary.LittleEndian, p); err != nil {
			panic(err)
		}
		payloads = append(payloads, buf.Bytes())
		payloadDataOffset += currentFragmentSize
		payloadRemaining -= currentFragmentSize
	}
	p.frameCounter++
	return payloads
}

func NewPointCloudPayloader() *PointCloudPayloader {
	return &PointCloudPayloader{0}
}
