package mux

import (
	"bytes"
	"testing"
	"time"
)

func TestMarshalUnmarshalFrame(t *testing.T) {
	f := Frame{
		StreamID: 42,
		Type:     FrameData,
		Sequence: 100,
		Payload:  []byte("hello world"),
	}

	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	if len(data) != 13+len(f.Payload) {
		t.Fatalf("expected %d bytes, got %d", 13+len(f.Payload), len(data))
	}

	decoded, err := ReadFrame(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}

	if decoded.StreamID != f.StreamID {
		t.Fatalf("StreamID: got %d, want %d", decoded.StreamID, f.StreamID)
	}
	if decoded.Type != f.Type {
		t.Fatalf("Type: got %d, want %d", decoded.Type, f.Type)
	}
	if decoded.Sequence != f.Sequence {
		t.Fatalf("Sequence: got %d, want %d", decoded.Sequence, f.Sequence)
	}
	if !bytes.Equal(decoded.Payload, f.Payload) {
		t.Fatalf("Payload mismatch")
	}
}

func TestFrameAllTypes(t *testing.T) {
	for _, ft := range []uint8{FrameData, FrameOpen, FrameClose, FramePing, FramePong} {
		f := Frame{StreamID: 1, Type: ft, Payload: []byte{0x01}}
		data, err := f.MarshalBinary()
		if err != nil {
			t.Fatalf("MarshalBinary type 0x%02x: %v", ft, err)
		}
		decoded, err := ReadFrame(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("ReadFrame type 0x%02x: %v", ft, err)
		}
		if decoded.Type != ft {
			t.Fatalf("type mismatch: got 0x%02x, want 0x%02x", decoded.Type, ft)
		}
	}
}

func TestFrameEmptyPayload(t *testing.T) {
	f := Frame{StreamID: 1, Type: FrameOpen, Payload: nil}
	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	decoded, err := ReadFrame(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if len(decoded.Payload) != 0 {
		t.Fatalf("expected empty payload, got %d bytes", len(decoded.Payload))
	}
}

func TestFrameLargePayload(t *testing.T) {
	payload := make([]byte, 65535) // max payload
	for i := range payload {
		payload[i] = byte(i)
	}
	f := Frame{StreamID: 99, Type: FrameData, Payload: payload}
	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary max payload: %v", err)
	}
	decoded, err := ReadFrame(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadFrame max payload: %v", err)
	}
	if !bytes.Equal(decoded.Payload, payload) {
		t.Fatal("max payload round-trip mismatch")
	}
}

func TestFrameSequenceNumbers(t *testing.T) {
	for _, seq := range []uint32{0, 1, 0xFFFFFFFF} {
		f := Frame{StreamID: 1, Type: FrameData, Sequence: seq, Payload: []byte{1}}
		data, _ := f.MarshalBinary()
		decoded, err := ReadFrame(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("ReadFrame seq %d: %v", seq, err)
		}
		if decoded.Sequence != seq {
			t.Fatalf("seq: got %d, want %d", decoded.Sequence, seq)
		}
	}
}

func TestRetransmitRing_PushAndDrain(t *testing.T) {
	var r retransmitRing
	r.mu.Lock()
	r.Push([]byte("frame1"))
	r.Push([]byte("frame2"))
	r.Push([]byte("frame3"))
	r.mu.Unlock()

	got := r.Drain()
	if len(got) != 3 {
		t.Fatalf("expected 3 frames, got %d", len(got))
	}
	if string(got[0]) != "frame1" || string(got[1]) != "frame2" || string(got[2]) != "frame3" {
		t.Fatalf("unexpected order: %v", got)
	}
	// Drain again should be empty
	if len(r.Drain()) != 0 {
		t.Fatal("expected empty after drain")
	}
}

func TestRetransmitRing_Wraparound(t *testing.T) {
	var r retransmitRing
	// Fill beyond capacity (128) — oldest should be evicted
	for i := 0; i < 200; i++ {
		r.mu.Lock()
		r.Push([]byte{byte(i)})
		r.mu.Unlock()
	}
	got := r.Drain()
	if len(got) != retransmitRingSize {
		t.Fatalf("expected %d frames, got %d", retransmitRingSize, len(got))
	}
	// First element should be frame 72 (200 - 128)
	if got[0][0] != 72 {
		t.Fatalf("expected first frame 72, got %d", got[0][0])
	}
}

func TestReorderBuffer_InOrder(t *testing.T) {
	rb := newReorderBuffer()
	// seq 0 == nextSeq → immediate delivery
	flushed := rb.Insert(0, []byte("a"))
	if len(flushed) != 1 || string(flushed[0]) != "a" {
		t.Fatalf("expected immediate delivery of seq 0, got %v", flushed)
	}
	// seq 1 == nextSeq → immediate delivery
	flushed = rb.Insert(1, []byte("b"))
	if len(flushed) != 1 || string(flushed[0]) != "b" {
		t.Fatalf("expected immediate delivery of seq 1, got %v", flushed)
	}
	if rb.Len() != 0 {
		t.Fatal("buffer should be empty after in-order delivery")
	}
}

func TestReorderBuffer_OutOfOrder(t *testing.T) {
	rb := newReorderBuffer()
	// seq 1 arrives before seq 0
	rb.Insert(1, []byte("b"))
	if rb.Len() != 1 {
		t.Fatal("expected 1 buffered")
	}
	// seq 0 arrives — should flush both
	flushed := rb.Insert(0, []byte("a"))
	if len(flushed) != 2 {
		t.Fatalf("expected 2 flushed, got %d", len(flushed))
	}
	if string(flushed[0]) != "a" || string(flushed[1]) != "b" {
		t.Fatalf("unexpected order: %s, %s", flushed[0], flushed[1])
	}
}

func TestReorderBuffer_Duplicate(t *testing.T) {
	rb := newReorderBuffer()
	rb.Insert(1, []byte("b")) // buffer seq 1 (gap at 0)
	rb.Insert(1, []byte("b")) // duplicate
	if rb.Len() != 1 {
		t.Fatal("duplicate should not increase buffer size")
	}
}

func TestReorderBuffer_WrapSafe(t *testing.T) {
	rb := newReorderBuffer()
	rb.nextSeq = 0xFFFFFFFF
	flushed := rb.Insert(0xFFFFFFFF, []byte("wrap"))
	if len(flushed) != 1 {
		t.Fatal("expected delivery at wrap point")
	}
	// Next expected is 0x00000000
	if rb.nextSeq != 0 {
		t.Fatalf("expected nextSeq=0 after wrap, got %d", rb.nextSeq)
	}
}

func TestReorderBuffer_Overflow(t *testing.T) {
	rb := newReorderBuffer()
	// Insert 256 out-of-order frames (skip seq 0)
	for i := uint32(1); i <= reorderMaxFrames; i++ {
		rb.Insert(i, []byte{byte(i)})
	}
	if rb.Overflowed() {
		t.Fatal("should not overflow at exactly 256")
	}
	rb.Insert(reorderMaxFrames+1, []byte{0xFF})
	if !rb.Overflowed() {
		t.Fatal("should overflow at 257")
	}
}

func TestReorderBuffer_Timeout(t *testing.T) {
	rb := newReorderBuffer()
	rb.Insert(1, []byte("b")) // gap at 0
	// Manually set gap time to past
	rb.gapSince = rb.gapSince.Add(-3 * time.Second)
	if !rb.GapTimedOut(2 * time.Second) {
		t.Fatal("expected timeout")
	}
}
