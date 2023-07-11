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
	"sync"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/cc"
	"github.com/pion/interceptor/pkg/gcc"
	"github.com/pion/interceptor/pkg/nack"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

func signalCandidate(addr string, c *webrtc.ICECandidate) error {
	payload := []byte(c.ToJSON().Candidate)
	resp, err := http.Post(fmt.Sprintf("http://%s/candidate", addr), "application/json; charset=utf-8", bytes.NewReader(payload)) //nolint:noctx
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func main() {
	offerAddr := flag.String("offer-address", ":7002", "Address that the Offer HTTP server is hosted on.")
	answerAddr := flag.String("r", "127.0.0.1:8001", "Address that the Answer HTTP server is hosted on.")
	useFiles := flag.Bool("f", false, "Use pre encoded files instead or remote input")
	useVirtualWall := flag.Bool("v", false, "Use virtual wall ip filter")
	flag.Parse()
	var transcoder Transcoder
	if *useFiles {
		transcoder = NewTranscoderFile()
	} else {
		transcoder = NewTranscoderRemote()
	}

	for !transcoder.IsReady() {
		println("Waiting")
		time.Sleep(10 * time.Millisecond)
	}
	settingEngine := webrtc.SettingEngine{}
	settingEngine.SetSCTPMaxReceiveBufferSize(16 * 1024 * 1024)
	if *useVirtualWall {
		settingEngine.SetIPFilter(VirtualWallFilter)
	}
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

	// Create a Congestion Controller. This analyzes inbound and outbound data and provides
	// suggestions on how much we should be sending.
	//
	// Passing `nil` means we use the default Estimation Algorithm which is Google Congestion Control.
	// You can use the other ones that Pion provides, or write your own!

	// TODO: ask why these values are initially used
	congestionController, err := cc.NewInterceptor(func() (cc.BandwidthEstimator, error) {
		return gcc.NewSendSideBWE(gcc.SendSideBWEMinBitrate(75_000*8), gcc.SendSideBWEInitialBitrate(75_000_000), gcc.SendSideBWEMaxBitrate(262_744_320))
	})

	if err != nil {
		panic(err)
	}

	estimatorChan := make(chan cc.BandwidthEstimator, 1)
	congestionController.OnNewPeerConnection(func(id string, estimator cc.BandwidthEstimator) {
		estimatorChan <- estimator
		print("test")
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

	// When an ICE candidate is available send to the other Pion instance
	// the other Pion instance will add this candidate by calling AddICECandidate
	peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}

		candidatesMux.Lock()
		defer candidatesMux.Unlock()

		desc := peerConnection.RemoteDescription()
		if desc == nil {
			pendingCandidates = append(pendingCandidates, c)
		} else if onICECandidateErr := signalCandidate(*answerAddr, c); onICECandidateErr != nil {
			panic(onICECandidateErr)
		}
	})

	// An HTTP handler that allows the other Pion instance to send us ICE candidates
	// This allows us to add ICE candidates faster, we don't have to wait for STUN or TURN
	// candidates which may be slower
	http.HandleFunc("/candidate", func(w http.ResponseWriter, r *http.Request) {
		candidate, candidateErr := ioutil.ReadAll(r.Body)
		if candidateErr != nil {
			panic(candidateErr)
		}
		if candidateErr := peerConnection.AddICECandidate(webrtc.ICECandidateInit{Candidate: string(candidate)}); candidateErr != nil {
			panic(candidateErr)
		}
	})

	// An HTTP handler that processes a SessionDescription given to us from the other Pion process
	http.HandleFunc("/sdp", func(w http.ResponseWriter, r *http.Request) {
		sdp := webrtc.SessionDescription{}
		if sdpErr := json.NewDecoder(r.Body).Decode(&sdp); sdpErr != nil {
			panic(sdpErr)
		}

		if sdpErr := peerConnection.SetRemoteDescription(sdp); sdpErr != nil {
			panic(sdpErr)
		}

		candidatesMux.Lock()
		defer candidatesMux.Unlock()

		for _, c := range pendingCandidates {
			if onICECandidateErr := signalCandidate(*answerAddr, c); onICECandidateErr != nil {
				panic(onICECandidateErr)
			}
		}
	})

	// Start HTTP server that accepts requests from the answer process
	// nolint: gosec
	go func() { panic(http.ListenAndServe(*offerAddr, nil)) }()

	// Create a datachannel with label 'data'
	/*dataChannel, err := peerConnection.CreateDataChannel("data", nil)
	if err != nil {
		panic(err)
	}*/

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

	// Register channel opening handling
	/*dataChannel.OnOpen(func() {
		fmt.Printf("Data channel '%s'-'%d' open. Random messages will now be sent to any connected DataChannels every 5 seconds\n", dataChannel.Label(), dataChannel.ID())
		for range time.NewTicker(5 * time.Second).C {
			message := "SERVER"
			fmt.Printf("Sending '%s'\n", message)
			print(estimator.GetStats())
			// Send the message as text
			sendTextErr := dataChannel.SendText(message)
			if sendTextErr != nil {
				panic(sendTextErr)
			}
		}
	})
	// Register text message handling
	dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		fmt.Printf("Message from DataChannel '%s': '%s'\n", dataChannel.Label(), string(msg.Data))
	})*/

	// Create an offer to send to the other process
	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		panic(err)
	}

	// Sets the LocalDescription, and starts our UDP listeners
	// Note: this will start tFaddhe gathering of ICE candidates
	if err = peerConnection.SetLocalDescription(offer); err != nil {
		panic(err)
	}

	// Send our offer to the HTTP server listening in the other process
	payload, err := json.Marshal(offer)
	if err != nil {
		panic(err)
	}

	resp, err := http.Post(fmt.Sprintf("http://%s/sdp", *answerAddr), "application/json; charset=utf-8", bytes.NewReader(payload)) // nolint:noctx
	if err != nil {
		panic(err)
	} else if err := resp.Body.Close(); err != nil {
		panic(err)
	}
	oldEpochMilliseconds := time.Now().UnixNano() / int64(time.Millisecond)
	println(estimator.GetTargetBitrate(), oldEpochMilliseconds)
	msCounter := int(0)
	tFrames := int(0)
	loss := float64(0)
	delayRate := int(0)
	lossRate := int(0)
	for ; true; <-time.NewTicker(20 * time.Millisecond).C {
		//currentTime := time.Now()
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
		/*for key, value := range estimator.GetStats() {
			fmt.Println(key, value)
		}*/
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
			// lossTargetBitrate
			// averageLoss
			// delayTargetBitrate
		}
		//println("TARGET RATE ", epochMilliseconds-oldEpochMilliseconds);
		oldEpochMilliseconds = epochMilliseconds
		//runtime.GC()
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

func VirtualWallFilter(addr net.IP) bool {
	if addr.String() == "192.168.0.1" {
		return true
	}
	return false
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
		/*if counter % 25 == 0 {
			oldEpochMilliseconds := time.Now().UnixNano()
			//C.wait(C.int(100))
			time.Sleep(1 * time.Nanosecond)
			//smallWait()
			currentTime := time.Now()
			epochMilliseconds := currentTime.UnixNano()
			println("WAIT ", epochMilliseconds-oldEpochMilliseconds)
		}*/
	}
	/*if (t.GetFrameNr() - 1) % 5 == 0 {
		timestamp := time.Now().UnixNano() / int64(time.Millisecond)
		data := fmt.Sprintf("%d;%d;%d\n", timestamp, t.GetFrameNr()-1, len(frameData.Data))
		file.WriteString(data)
	}*/
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

	//maxFragmentSize := mtu // TODO subtract point cloud header
	//	payloadDataRemaining := len(payload)
	//payloadDataIndex := uint32(0)
	payloadDataOffset := uint32(0)
	payloadLen := uint32(len(payload))
	payloadRemaining := payloadLen
	// Make sure the fragment/payload size is correct
	/*if math.Min(maxFragmentSize, payloadDataRemaining) <= 0 {
		return payloads
	}
	*/
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
		//payloadDataIndex++
		payloadDataOffset += currentFragmentSize
		payloadRemaining -= currentFragmentSize
	}
	p.frameCounter++
	return payloads
}

func NewPointCloudPayloader() *PointCloudPayloader {
	return &PointCloudPayloader{0}
}
