package telemost

import (
	"encoding/binary"
)

// FEC parameters: k data packets per group, stride interleaved groups.
// With k=7, stride=3: ~14% overhead, recovers 1 loss per group,
// burst-resilient up to 3 consecutive lost packets (each hits different group).
const (
	fecK      = 7
	fecStride = 3
)

// FECEncoder accumulates sent data packets and emits XOR parity packets.
type FECEncoder struct {
	groups [fecStride]fecEncGroup
}

type fecEncGroup struct {
	members   []fecEncMember
	maxPayLen int
	startSeq  uint16 // pktSeq of the first member
}

type fecEncMember struct {
	pktSeq  uint16
	payload []byte // chunkPayload (writeSeq + idx + total + data)
}

// NewFECEncoder creates a new FEC encoder.
func NewFECEncoder() *FECEncoder {
	return &FECEncoder{}
}

// AddPacket records a data packet. If this completes a FEC group,
// returns (xorPayload, firstMemberSeq, memberCount, true).
func (e *FECEncoder) AddPacket(pktSeq uint16, chunkPayload []byte) (xorPayload []byte, firstSeq uint16, count int, ok bool) {
	g := &e.groups[pktSeq%fecStride]

	if len(g.members) == 0 {
		g.startSeq = pktSeq
	}

	pay := make([]byte, len(chunkPayload))
	copy(pay, chunkPayload)
	g.members = append(g.members, fecEncMember{pktSeq: pktSeq, payload: pay})
	if len(pay) > g.maxPayLen {
		g.maxPayLen = len(pay)
	}

	if len(g.members) < fecK {
		return nil, 0, 0, false
	}

	// Group complete — compute XOR parity.
	// Each payload is length-prefixed (2 bytes) and padded to uniform size for XOR.
	paddedLen := g.maxPayLen + 2
	parity := make([]byte, paddedLen)
	for _, m := range g.members {
		padded := make([]byte, paddedLen)
		binary.BigEndian.PutUint16(padded[0:2], uint16(len(m.payload)))
		copy(padded[2:], m.payload)
		xorInto(parity, padded)
	}

	firstSeq = g.startSeq
	count = len(g.members)

	// Reset group for next round.
	g.members = g.members[:0]
	g.maxPayLen = 0

	return parity, firstSeq, count, true
}

// FECDecoder recovers lost data packets using XOR parity and a pool of received packets.
type FECDecoder struct {
	pool map[uint16][]byte // pktSeq → chunkPayload
}

// NewFECDecoder creates a new FEC decoder.
func NewFECDecoder() *FECDecoder {
	return &FECDecoder{pool: make(map[uint16][]byte)}
}

// AddData records a received data packet for potential FEC recovery.
func (d *FECDecoder) AddData(pktSeq uint16, chunkPayload []byte) {
	pay := make([]byte, len(chunkPayload))
	copy(pay, chunkPayload)
	d.pool[pktSeq] = pay
}

// Recover attempts to recover a missing data packet using FEC parity.
// firstSeq is the pktSeq of the first group member, stride is the interleaving stride,
// count is the number of data packets in the group.
// Returns (recoveredPktSeq, recoveredChunkPayload, true) if exactly one packet is missing.
func (d *FECDecoder) Recover(firstSeq uint16, stride uint8, count int, parity []byte) (uint16, []byte, bool) {
	if count < 1 || count > fecK {
		return 0, nil, false
	}

	// Find the missing member.
	missingIdx := -1
	for i := 0; i < count; i++ {
		seq := firstSeq + uint16(i)*uint16(stride)
		if _, ok := d.pool[seq]; !ok {
			if missingIdx >= 0 {
				return 0, nil, false // >1 missing
			}
			missingIdx = i
		}
	}
	if missingIdx < 0 {
		return 0, nil, false // all present
	}

	// XOR parity with all present payloads to recover the missing one.
	paddedLen := len(parity)
	if paddedLen < 2 {
		return 0, nil, false
	}

	result := make([]byte, paddedLen)
	copy(result, parity)

	for i := 0; i < count; i++ {
		if i == missingIdx {
			continue
		}
		seq := firstSeq + uint16(i)*uint16(stride)
		payload := d.pool[seq]
		padded := make([]byte, paddedLen)
		binary.BigEndian.PutUint16(padded[0:2], uint16(len(payload)))
		copy(padded[2:], payload)
		xorInto(result, padded)
	}

	payLen := int(binary.BigEndian.Uint16(result[0:2]))
	if payLen < 0 || payLen > paddedLen-2 {
		return 0, nil, false // corrupt
	}

	missingSeq := firstSeq + uint16(missingIdx)*uint16(stride)
	recovered := make([]byte, payLen)
	copy(recovered, result[2:2+payLen])

	return missingSeq, recovered, true
}

// Prune removes packets older than minSeq from the pool.
func (d *FECDecoder) Prune(minSeq uint16) {
	for seq := range d.pool {
		if seqAfter(minSeq, seq) {
			delete(d.pool, seq)
		}
	}
}

// xorInto XORs src into dst byte-by-byte.
func xorInto(dst, src []byte) {
	for i := range src {
		if i < len(dst) {
			dst[i] ^= src[i]
		}
	}
}
