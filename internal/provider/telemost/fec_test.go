package telemost

import (
	"bytes"
	"testing"
)

func TestXorInto(t *testing.T) {
	dst := []byte{0x01, 0x02, 0x03}
	src := []byte{0x10, 0x20, 0x30}
	xorInto(dst, src)
	want := []byte{0x11, 0x22, 0x33}
	if !bytes.Equal(dst, want) {
		t.Fatalf("got %x, want %x", dst, want)
	}
}

func TestFECEncoderGrouping(t *testing.T) {
	enc := NewFECEncoder()
	// Send fecK * fecStride packets. Each stride group should complete after fecK packets.
	completed := 0
	for i := 0; i < fecK*fecStride; i++ {
		payload := []byte{byte(i), byte(i + 1), byte(i + 2)}
		_, _, _, ok := enc.AddPacket(uint16(i), payload)
		if ok {
			completed++
		}
	}
	if completed != fecStride {
		t.Fatalf("expected %d FEC groups completed, got %d", fecStride, completed)
	}
}

func TestFECRecoverSingleLoss(t *testing.T) {
	enc := NewFECEncoder()
	dec := NewFECDecoder()

	// Generate fecK data packets for group 0 (stride offset 0).
	// pktSeqs: 0, 3, 6, 9, 12, 15, 18
	payloads := make(map[uint16][]byte)
	var fecPayload []byte
	var firstSeq uint16
	var count int

	for i := 0; i < fecK; i++ {
		seq := uint16(i * fecStride)
		payload := make([]byte, 10)
		for j := range payload {
			payload[j] = byte(seq + uint16(j))
		}
		payloads[seq] = payload

		xor, fs, c, ok := enc.AddPacket(seq, payload)
		if ok {
			fecPayload = xor
			firstSeq = fs
			count = c
		}
	}

	if fecPayload == nil {
		t.Fatal("FEC not generated")
	}

	// Simulate loss of pktSeq 6 (3rd member).
	lostSeq := uint16(6)
	for seq, payload := range payloads {
		if seq == lostSeq {
			continue // lost
		}
		dec.AddData(seq, payload)
	}

	// Feed FEC and try recovery.
	recoveredSeq, recoveredPayload, ok := dec.Recover(firstSeq, fecStride, count, fecPayload)
	if !ok {
		t.Fatal("FEC recovery failed")
	}
	if recoveredSeq != lostSeq {
		t.Fatalf("recovered wrong seq: got %d, want %d", recoveredSeq, lostSeq)
	}
	if !bytes.Equal(recoveredPayload, payloads[lostSeq]) {
		t.Fatalf("recovered payload mismatch: got %x, want %x", recoveredPayload, payloads[lostSeq])
	}
}

func TestFECNoRecoveryTwoLosses(t *testing.T) {
	enc := NewFECEncoder()
	dec := NewFECDecoder()

	var fecPayload []byte
	var firstSeq uint16
	var count int

	for i := 0; i < fecK; i++ {
		seq := uint16(i * fecStride)
		payload := []byte{byte(seq)}
		xor, fs, c, ok := enc.AddPacket(seq, payload)
		if ok {
			fecPayload = xor
			firstSeq = fs
			count = c
		}
	}

	// Only add 5 of 7 packets (2 missing).
	for i := 2; i < fecK; i++ {
		seq := uint16(i * fecStride)
		dec.AddData(seq, []byte{byte(seq)})
	}

	_, _, ok := dec.Recover(firstSeq, fecStride, count, fecPayload)
	if ok {
		t.Fatal("should not recover with 2 losses")
	}
}

func TestFECVariableLengthPayloads(t *testing.T) {
	enc := NewFECEncoder()
	dec := NewFECDecoder()

	// Payloads of different lengths.
	payloads := make(map[uint16][]byte)
	var fecPayload []byte
	var firstSeq uint16
	var count int

	for i := 0; i < fecK; i++ {
		seq := uint16(i * fecStride)
		// Variable length: 5 + i bytes.
		payload := make([]byte, 5+i)
		for j := range payload {
			payload[j] = byte(seq ^ uint16(j))
		}
		payloads[seq] = payload

		xor, fs, c, ok := enc.AddPacket(seq, payload)
		if ok {
			fecPayload = xor
			firstSeq = fs
			count = c
		}
	}

	// Lose the longest packet (last one).
	lostSeq := uint16((fecK - 1) * fecStride)
	for seq, payload := range payloads {
		if seq == lostSeq {
			continue
		}
		dec.AddData(seq, payload)
	}

	recoveredSeq, recoveredPayload, ok := dec.Recover(firstSeq, fecStride, count, fecPayload)
	if !ok {
		t.Fatal("FEC recovery failed for variable-length payloads")
	}
	if recoveredSeq != lostSeq {
		t.Fatalf("recovered wrong seq: got %d, want %d", recoveredSeq, lostSeq)
	}
	if !bytes.Equal(recoveredPayload, payloads[lostSeq]) {
		t.Fatalf("payload mismatch: got %x, want %x", recoveredPayload, payloads[lostSeq])
	}
}
