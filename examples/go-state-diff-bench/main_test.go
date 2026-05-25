package main

import (
	"testing"
	"time"
)

// ── signPrefix ────────────────────────────────────────────────────────────────

func TestSignPrefix_negative(t *testing.T) {
	if got := signPrefix(-1); got != "" {
		t.Fatalf("want empty string for negative, got %q", got)
	}
}

func TestSignPrefix_zero(t *testing.T) {
	if got := signPrefix(0); got != "+" {
		t.Fatalf("want \"+\" for zero, got %q", got)
	}
}

func TestSignPrefix_positive(t *testing.T) {
	if got := signPrefix(1); got != "+" {
		t.Fatalf("want \"+\" for positive, got %q", got)
	}
}

// ── emit ──────────────────────────────────────────────────────────────────────

// TestEmit_GrpcFaster verifies delta = grpcMs - rpcMs is negative when gRPC arrives first.
func TestEmit_GrpcFaster(t *testing.T) {
	_, _, deltas := emit(100, 0, 1000, 1020, 0, 0, nil)
	if len(deltas) != 1 {
		t.Fatalf("want 1 delta, got %d", len(deltas))
	}
	if deltas[0] != -20 {
		t.Fatalf("want delta=-20 (grpc faster), got %d", deltas[0])
	}
}

// TestEmit_RpcFaster verifies delta is positive when RPC arrives first.
func TestEmit_RpcFaster(t *testing.T) {
	_, _, deltas := emit(100, 0, 1020, 1000, 0, 0, nil)
	if len(deltas) != 1 {
		t.Fatalf("want 1 delta, got %d", len(deltas))
	}
	if deltas[0] != 20 {
		t.Fatalf("want delta=+20 (rpc faster), got %d", deltas[0])
	}
}

// TestEmit_Equal verifies delta is zero when both arrive at the same millisecond.
func TestEmit_Equal(t *testing.T) {
	_, _, deltas := emit(100, 0, 1000, 1000, 0, 0, nil)
	if len(deltas) != 1 {
		t.Fatalf("want 1 delta, got %d", len(deltas))
	}
	if deltas[0] != 0 {
		t.Fatalf("want delta=0, got %d", deltas[0])
	}
}

// TestEmit_MatchedIncrement verifies the matched counter increments by exactly 1.
func TestEmit_MatchedIncrement(t *testing.T) {
	matched, unmatched, _ := emit(100, 0, 1000, 1000, 5, 2, nil)
	if matched != 6 {
		t.Fatalf("want matched=6, got %d", matched)
	}
	if unmatched != 2 {
		t.Fatalf("want unmatched=2 (unchanged), got %d", unmatched)
	}
}

// TestEmit_AppendsDelta verifies deltas slice grows correctly across multiple calls.
func TestEmit_AppendsDelta(t *testing.T) {
	_, _, deltas := emit(1, 0, 100, 110, 0, 0, nil)
	_, _, deltas = emit(2, 0, 200, 190, 1, 0, deltas)
	if len(deltas) != 2 {
		t.Fatalf("want 2 deltas, got %d", len(deltas))
	}
	if deltas[0] != -10 {
		t.Fatalf("want deltas[0]=-10, got %d", deltas[0])
	}
	if deltas[1] != 10 {
		t.Fatalf("want deltas[1]=+10, got %d", deltas[1])
	}
}

// TestEmit_AccountsZero verifies accounts=0 (empty block) is handled correctly.
func TestEmit_AccountsZero(t *testing.T) {
	_, _, deltas := emit(200, 0, 1000, 1010, 0, 0, nil)
	if len(deltas) != 1 {
		t.Fatalf("want 1 delta, got %d", len(deltas))
	}
	if deltas[0] != -10 {
		t.Fatalf("want delta=-10, got %d", deltas[0])
	}
}

// ── getOrCreate ───────────────────────────────────────────────────────────────

// TestGetOrCreate_New verifies a new entry is created with since set.
func TestGetOrCreate_New(t *testing.T) {
	before := time.Now()
	pending := make(map[uint64]*partialArrival)
	p := getOrCreate(pending, 42)
	if p == nil {
		t.Fatal("want non-nil partialArrival")
	}
	if p.grpcMs != 0 {
		t.Fatalf("want grpcMs=0 for new entry, got %d", p.grpcMs)
	}
	if p.rpcMs != 0 {
		t.Fatalf("want rpcMs=0 for new entry, got %d", p.rpcMs)
	}
	if p.since.Before(before) {
		t.Fatal("since should be set to approximately now")
	}
	if _, ok := pending[42]; !ok {
		t.Fatal("entry should be stored in pending map")
	}
}

// TestGetOrCreate_Existing verifies the same pointer is returned on the second call.
func TestGetOrCreate_Existing(t *testing.T) {
	pending := make(map[uint64]*partialArrival)
	p1 := getOrCreate(pending, 99)
	p1.grpcMs = 12345
	p2 := getOrCreate(pending, 99)
	if p1 != p2 {
		t.Fatal("want same pointer for existing entry")
	}
	if p2.grpcMs != 12345 {
		t.Fatalf("want grpcMs=12345 preserved, got %d", p2.grpcMs)
	}
}

// TestGetOrCreate_IndependentKeys verifies different block numbers get independent entries.
func TestGetOrCreate_IndependentKeys(t *testing.T) {
	pending := make(map[uint64]*partialArrival)
	p1 := getOrCreate(pending, 1)
	p2 := getOrCreate(pending, 2)
	if p1 == p2 {
		t.Fatal("different block numbers should produce different entries")
	}
}
