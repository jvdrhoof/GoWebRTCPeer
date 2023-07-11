package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"unsafe"

	"github.com/Workiva/go-datastructures/queue"
)

type MultiFrameMainHeader struct {
	NFrames uint32
}

type MultiFrameSideHeader struct {
	FrameLen uint32
	ClientID uint32
}

type MultiLayerMainHeader struct {
	NLayers uint32
	MinX    float32
	MinY    float32
	MinZ    float32
	MaxX    float32
	MaxY    float32
	MaxZ    float32
}

type MultiLayerSideHeader struct {
	LayerID  uint32
	FrameLen uint32
}

type FrameCategory struct {
	Dst            float32
	FrameID        uint32
	CurrentBitrate uint32
	CurrentCat     uint8
	Combo          uint8
}

type LayeredEncoder struct {
	Bitrate uint32
}

type DstAndFrameID struct {
	Dst     float32
	FrameID uint32
}

type PanZoom struct {
	XPos  float32
	YPos  float32
	ZPos  float32
	Yaw   float32
	Pitch float32
	Roll  float32
}

func NewLayeredEncoder() *LayeredEncoder {
	return &LayeredEncoder{}
}

func (lhs FrameCategory) Compare(other queue.Item) int {
	rhs := other.(FrameCategory)
	if lhs.Combo == rhs.Combo && lhs.Dst == rhs.Dst {
		return 0
	}
	if lhs.Combo < rhs.Combo || lhs.Dst < rhs.Dst {
		return -1
	}
	return 1
}

func (l *LayeredEncoder) EncodeMultiFrame(frames []RemoteInputReceivedFrame) *MultiFrame {
	nClients := len(frames)
	var offsets []uint32
	//var distanceToUser []float32
	var distanceIDs []uint8
	var mainLHeaders []MultiLayerMainHeader
	var frameHeaders []MultiFrameSideHeader
	var allLOffsets [][]uint32
	var allLLen [][]uint32
	var framesForCat [3][]DstAndFrameID
	var distanceToCategory []*queue.PriorityQueue
	for i := 0; i < 3; i++ {
		q := queue.NewPriorityQueue(10, false)
		distanceToCategory = append(distanceToCategory, q)
	}
	pz := PanZoom{0.0, 0.0, 0.0, 0.0, 0.0, 0.0}
	// Combos
	cs := [][][]uint8{
		[][]uint8{
			[]uint8{0},
			[]uint8{0, 2},
			[]uint8{0, 1},
			[]uint8{0, 1, 2},
		},
		[][]uint8{
			[]uint8{1},
			[]uint8{1, 2},
		},
		[][]uint8{
			[]uint8{2},
		},
	}
	csFrame := [][][]uint32{}
	//categorySizesForFrames := [][]uint32{}
	totalBandwidthForCategoryForCombos := [][]uint32{
		[]uint32{0, 0, 0, 0},
		[]uint32{0, 0},
		[]uint32{0},
	}
	// Drop marking
	dropFrameMarking := make([]bool, len(frames))

	for i := 0; i < nClients; i++ {
		var mh MultiLayerMainHeader

		x := frames[i]
		buf := bytes.NewBuffer(x.Data[:unsafe.Sizeof(mh)])
		if err := binary.Read(buf, binary.LittleEndian, &mh); err != nil {
			panic(err)
		}
		mainLHeaders = append(mainLHeaders, mh)
		offsets = append(offsets, uint32(unsafe.Sizeof(mh)))

		mx := mh.MinX + (mh.MaxX-mh.MinX)/2
		my := mh.MinY + (mh.MaxY-mh.MinY)/2
		mz := mh.MinZ + (mh.MaxZ-mh.MinZ)/2
		dst := math.Sqrt(math.Pow(float64(pz.XPos-mx), 2) + math.Pow(float64(pz.YPos-my), 2) + math.Pow(float64(pz.ZPos-mz), 2))
		dstID := uint8(dst / 50)
		distanceIDs = append(distanceIDs, dstID)

		if dstID < 3 {
			framesForCat[dstID] = append(framesForCat[dstID], DstAndFrameID{float32(dst), uint32(i)})
		}

		sh := MultiFrameSideHeader{
			FrameLen: x.FrameLen,
			ClientID: x.ClientID,
		}
		frameHeaders = append(frameHeaders, sh)

		sHeaders := make([]MultiLayerSideHeader, mh.NLayers)
		lOffsets := make([]uint32, mh.NLayers)
		lLen := make([]uint32, mh.NLayers)

		currentOffset := uint32(unsafe.Sizeof(mh))

		for j := 0; j < int(mh.NLayers); j++ {
			var shTemp MultiLayerSideHeader
			buf := bytes.NewBuffer(x.Data[currentOffset:(currentOffset + uint32(unsafe.Sizeof(mh)))])
			if err := binary.Read(buf, binary.LittleEndian, &shTemp); err != nil {
				panic(err)
			}
			sHeaders = append(sHeaders, shTemp)
			lOffsets[shTemp.LayerID] = uint32(currentOffset)
			currentOffset += uint32(unsafe.Sizeof(shTemp)) + shTemp.FrameLen
			lLen[shTemp.LayerID] = uint32(unsafe.Sizeof(shTemp)) + shTemp.FrameLen
		}

		allLLen = append(allLLen, lLen)
		allLOffsets = append(allLOffsets, lOffsets)

		if dstID < 3 {
			dropFrameMarking[i] = false
		} else {
			dropFrameMarking[i] = true
			continue
		}

		var categorySizes [][]uint32
		for j := 0; j < len(cs); j++ {
			var sizes []uint32
			for k := 0; k < len(cs[j]); k++ {
				lc := cs[j][k]
				lcSize := uint32(0)
				for _, l := range lc {
					lcSize += lLen[l]
				}
				if j == int(dstID) {
					totalBandwidthForCategoryForCombos[j][k] += lcSize
				}
				sizes = append(sizes, lcSize)
			}
			categorySizes = append(categorySizes, sizes)
		}
		csFrame = append(csFrame, categorySizes)
	}

	tempBitrate := int(l.Bitrate / 8 / 30)

	for i := 2; i >= 0; i-- {
		tbc := totalBandwidthForCategoryForCombos[i]
		for j := len(tbc) - 1; j >= 0; j-- {
			if int(tbc[j]) <= tempBitrate || j == 0 {
				tempBitrate -= int(tbc[j])
				for k := 0; k < len(framesForCat[i]); k++ {
					fc := framesForCat[i][k]
					bitrateForFrame := csFrame[k][i][j]
					fcc := FrameCategory{
						Dst:            fc.Dst,
						FrameID:        fc.FrameID,
						CurrentBitrate: bitrateForFrame,
						CurrentCat:     uint8(i),
						Combo:          uint8(j),
					}
					//println("CAT ", i, " COMBO ", j)
					distanceToCategory[i].Put(fcc)
				}
				break
			}
		}
	}
	//println("TEMP BITRATE ", tempBitrate)
	for i := 0; i < 3; i++ {
		for i+1 < 3 && tempBitrate < 0 && !distanceToCategory[i+1].Empty() {
			fs, _ := distanceToCategory[i+1].Get(1)
			furthestF := fs[0].(FrameCategory)

			CurrentBitrate := furthestF.CurrentBitrate
			newBitrate := uint32(0)

			if furthestF.Combo == 0 {
				if i+2 == 3 {
					newBitrate = 0
					dropFrameMarking[furthestF.FrameID] = true
				} else {
					furthestF.Combo = uint8(len(csFrame[furthestF.FrameID][i+2])) - 1
					newBitrate = csFrame[furthestF.FrameID][i+2][furthestF.Combo]
					furthestF.CurrentBitrate = newBitrate
					furthestF.CurrentCat++
					distanceToCategory[i+2].Put(furthestF)
				}
			} else {
				furthestF.Combo--
				newBitrate = csFrame[furthestF.FrameID][i+1][furthestF.Combo]
				furthestF.CurrentBitrate = newBitrate
				distanceToCategory[i+1].Put(furthestF)
			}

			regained_bitrate := CurrentBitrate - newBitrate
			tempBitrate += int(regained_bitrate)
		}

		for tempBitrate < 0 && !distanceToCategory[i].Empty() {
			fs, _ := distanceToCategory[i].Get(1)
			furthestF := fs[0].(FrameCategory)
			CurrentBitrate := furthestF.CurrentBitrate
			newBitrate := uint32(0)

			if furthestF.Combo == 0 {
				if i+1 == 3 {
					newBitrate = 0
					dropFrameMarking[furthestF.FrameID] = true
				} else {

					furthestF.Combo = uint8(len(csFrame[furthestF.FrameID][i+1])) - 1
					newBitrate = csFrame[furthestF.FrameID][i+1][furthestF.Combo]
					furthestF.CurrentBitrate = newBitrate
					furthestF.CurrentCat++
					distanceToCategory[i+1].Put(furthestF)
				}
			} else {
				furthestF.Combo--
				newBitrate = csFrame[furthestF.FrameID][i][furthestF.Combo]
				furthestF.CurrentBitrate = newBitrate
				distanceToCategory[i].Put(furthestF)
			}

			regained_bitrate := CurrentBitrate - newBitrate
			tempBitrate += int(regained_bitrate)
		}
	}
	validFrames := uint32(0)
	combos := make([][]uint8, len(distanceToCategory))
	//var finalSizes []uint32
	size := uint32(unsafe.Sizeof(MultiFrameMainHeader{}))
	for _, cat := range distanceToCategory {
		for !cat.Empty() {
			fs, _ := cat.Get(1)
			f := fs[0].(FrameCategory)
			size += uint32(unsafe.Sizeof(MultiFrameSideHeader{})) + uint32(unsafe.Sizeof(MultiLayerMainHeader{})) + f.CurrentBitrate
			mainLHeaders[f.FrameID].NLayers = uint32(len(cs[f.CurrentCat][f.Combo]))
			combos[f.FrameID] = cs[f.CurrentCat][f.Combo]
			frameHeaders[f.FrameID].FrameLen = f.CurrentBitrate
			//println(l.Bitrate/30/8, f.CurrentCat, f.Combo, f.CurrentBitrate)
			validFrames++
		}
	}
	mainHeader := MultiFrameMainHeader{validFrames}
	multiFrame := MultiFrame{}
	multiFrame.Data = make([]byte, size)
	buf := new(bytes.Buffer)

	if err := binary.Write(buf, binary.LittleEndian, mainHeader); err != nil {
		panic(err)
	}
	copy(multiFrame.Data[:], buf.Bytes()[:])
	offset := uint32(len(buf.Bytes()))
	for k := 0; k < nClients; k++ {
		if dropFrameMarking[k] {
			continue
		}

		buf := new(bytes.Buffer)
		if err := binary.Write(buf, binary.LittleEndian, frameHeaders[k]); err != nil {
			panic(err)
		}
		copy(multiFrame.Data[offset:], buf.Bytes()[:])
		offset += uint32(len(buf.Bytes()))
		buf = new(bytes.Buffer)
		if err := binary.Write(buf, binary.LittleEndian, mainLHeaders[k]); err != nil {
			panic(err)
		}
		copy(multiFrame.Data[offset:], buf.Bytes()[:])
		offset += uint32(len(buf.Bytes()))
		for i := 0; i < len(allLLen[k]); i++ {
			for j := 0; j < len(combos[k]); j++ {
				if uint8(i) == combos[k][j] {
					copy(multiFrame.Data[offset:], frames[k].Data[allLOffsets[k][i]:allLOffsets[k][i]+allLLen[k][i]])
					offset += allLLen[k][i]
				}
			}
		}
	}
	return &multiFrame
}
