package telemost

import (
	"testing"
	"time"
)

func TestRetransmitBuffer(t *testing.T) {
	rb := &RetransmitBuffer{}

	rb.Store(42, []byte{1, 2, 3})
	got := rb.Get(42)
	if got == nil || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("expected [1 2 3], got %v", got)
	}

	if rb.Get(43) != nil {
		t.Fatal("expected nil for missing seq")
	}

	rb.Store(42+retxBufSize, []byte{4, 5})
	if rb.Get(42) != nil {
		t.Fatal("old entry should be overwritten")
	}
	got = rb.Get(42 + retxBufSize)
	if got == nil || got[0] != 4 {
		t.Fatalf("expected [4 5], got %v", got)
	}
}

func TestNACKTrackerNoGaps(t *testing.T) {
	nt := NewNACKTracker()

	nt.Record(0)
	nt.Record(1)
	nt.Record(2)
	nacks := nt.GetNACKs()
	if len(nacks) != 0 {
		t.Fatalf("expected no NACKs, got %v", nacks)
	}
}

func TestNACKTrackerGapDetection(t *testing.T) {
	nt := NewNACKTracker()

	nt.Record(0)
	nt.Record(1)
	nt.Record(3) // gap at 2

	// Too early — nackDelay not yet elapsed.
	nacks := nt.GetNACKs()
	if len(nacks) != 0 {
		t.Fatalf("expected no NACKs before delay, got %v", nacks)
	}

	time.Sleep(nackDelay + 5*time.Millisecond)
	nacks = nt.GetNACKs()
	if len(nacks) != 1 || nacks[0] != 2 {
		t.Fatalf("expected NACK for seq 2, got %v", nacks)
	}
}

func TestNACKTrackerRetry(t *testing.T) {
	nt := NewNACKTracker()

	nt.Record(0)
	nt.Record(3) // gap at 1, 2

	time.Sleep(nackDelay + 5*time.Millisecond)
	nacks := nt.GetNACKs()
	if len(nacks) != 2 {
		t.Fatalf("expected 2 NACKs, got %v", nacks)
	}

	// Calling again immediately should return empty (retry interval not elapsed).
	nacks = nt.GetNACKs()
	if len(nacks) != 0 {
		t.Fatalf("expected no NACKs before retry interval, got %v", nacks)
	}

	// After retry interval, should re-NACK.
	time.Sleep(nackRetry + 5*time.Millisecond)
	nacks = nt.GetNACKs()
	if len(nacks) != 2 {
		t.Fatalf("expected re-NACKs for seqs 1,2, got %v", nacks)
	}
}

func TestNACKTrackerResolve(t *testing.T) {
	nt := NewNACKTracker()

	nt.Record(0)
	nt.Record(3) // gap at 1, 2

	nt.Resolve(1) // FEC recovered 1

	time.Sleep(nackDelay + 5*time.Millisecond)
	nacks := nt.GetNACKs()
	if len(nacks) != 1 || nacks[0] != 2 {
		t.Fatalf("expected NACK for seq 2 only, got %v", nacks)
	}
}

func TestNACKTrackerLateArrival(t *testing.T) {
	nt := NewNACKTracker()

	nt.Record(0)
	nt.Record(3) // gap at 1, 2

	nt.Record(2) // late arrival

	time.Sleep(nackDelay + 5*time.Millisecond)
	nacks := nt.GetNACKs()
	// Only seq 1 should be missing.
	if len(nacks) != 1 || nacks[0] != 1 {
		t.Fatalf("expected NACK for seq 1, got %v", nacks)
	}
}
