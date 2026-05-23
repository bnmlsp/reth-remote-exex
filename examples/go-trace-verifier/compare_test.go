package main

import (
	"strings"
	"testing"

	pb "exex_trace_verifier/proto/gen"
)

func gas(v uint64) []byte {
	b := make([]byte, 32)
	for i := 31; v > 0; i-- {
		b[i] = byte(v & 0xff)
		v >>= 8
	}
	return b
}

func TestNormalizeU256grpc_nil(t *testing.T) {
	if got := normalizeU256grpc(nil); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestNormalizeU256grpc_zero(t *testing.T) {
	if got := normalizeU256grpc(make([]byte, 32)); got != "0x0" {
		t.Fatalf("want 0x0, got %q", got)
	}
}

func TestNormalizeU256grpc_strip(t *testing.T) {
	b := make([]byte, 32)
	b[31] = 1
	if got := normalizeU256grpc(b); got != "0x1" {
		t.Fatalf("want 0x1, got %q", got)
	}
}

func TestNormalizeU256rpc_absent(t *testing.T) {
	if got := normalizeU256rpc(""); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestNormalizeU256rpc_zero(t *testing.T) {
	if got := normalizeU256rpc("0x0"); got != "0x0" {
		t.Fatalf("want 0x0, got %q", got)
	}
}

func TestNormalizeU256rpc_strip(t *testing.T) {
	if got := normalizeU256rpc("0x000001"); got != "0x1" {
		t.Fatalf("want 0x1, got %q", got)
	}
}

func TestNormalizeAddress_nil(t *testing.T) {
	if got := normalizeAddress(nil); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestNormalizeAddress_valid(t *testing.T) {
	b := make([]byte, 20)
	b[0] = 0xab
	b[19] = 0xcd
	got := normalizeAddress(b)
	if got[:4] != "0xab" {
		t.Fatalf("unexpected prefix: %q", got)
	}
	if got[len(got)-2:] != "cd" {
		t.Fatalf("unexpected suffix: %q", got)
	}
	if len(got) != 42 {
		t.Fatalf("want 42 chars, got %d: %q", len(got), got)
	}
}

func TestNormalizeBytes_nil(t *testing.T) {
	if got := normalizeBytes(nil); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestNormalizeBytes_nonempty(t *testing.T) {
	if got := normalizeBytes([]byte{0xde, 0xad}); got != "0xdead" {
		t.Fatalf("want 0xdead, got %q", got)
	}
}

func TestNormalizeHexStr_empty(t *testing.T) {
	if got := normalizeHexStr(""); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestNormalizeHexStr_uppercase(t *testing.T) {
	if got := normalizeHexStr("0xDEAD"); got != "0xdead" {
		t.Fatalf("want 0xdead, got %q", got)
	}
}

func TestNormalizeHexStr_empty_hex(t *testing.T) {
	if got := normalizeHexStr("0x"); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestCompareFrame_pass(t *testing.T) {
	from := make([]byte, 20)
	from[0] = 0xaa
	grpcFrame := &pb.CallFrame{
		Typ:     "CALL",
		From:    from,
		GasUsed: gas(0x5208),
		Gas:     gas(0x5208),
	}
	rpc := rpcCallFrame{
		Type:    "CALL",
		From:    normalizeAddress(from),
		GasUsed: "0x5208",
		Gas:     "0x5208",
	}
	mm, _, _ := compareFrame("tx[0]", grpcFrame, rpc)
	if len(mm) != 0 {
		t.Fatalf("expected 0 mismatches, got: %v", mm)
	}
}

func TestCompareFrame_typ_mismatch(t *testing.T) {
	grpcFrame := &pb.CallFrame{Typ: "CALL"}
	rpc := rpcCallFrame{Type: "STATICCALL"}
	mm, _, _ := compareFrame("tx[0]", grpcFrame, rpc)
	if len(mm) != 1 {
		t.Fatalf("expected 1 mismatch, got %d: %v", len(mm), mm)
	}
	if !strings.Contains(mm[0], ".typ:") {
		t.Fatalf("mismatch should mention .typ: %v", mm)
	}
}

func TestCompareFrame_gasUsed_mismatch(t *testing.T) {
	grpcFrame := &pb.CallFrame{GasUsed: gas(0x5208)}
	rpc := rpcCallFrame{GasUsed: "0x5209"}
	mm, _, _ := compareFrame("tx[0]", grpcFrame, rpc)
	if len(mm) != 1 {
		t.Fatalf("expected 1 mismatch, got %d: %v", len(mm), mm)
	}
	if !strings.Contains(mm[0], "gasUsed") {
		t.Fatalf("mismatch should mention gasUsed: %v", mm)
	}
}

func TestCompareFrame_to_absent_both(t *testing.T) {
	grpcFrame := &pb.CallFrame{Typ: "CREATE", To: nil}
	rpc := rpcCallFrame{Type: "CREATE", To: ""}
	mm, _, _ := compareFrame("tx[0]", grpcFrame, rpc)
	for _, m := range mm {
		if strings.Contains(m, ".to:") {
			t.Fatalf("to should match when both absent: %v", mm)
		}
	}
}

func TestCompareFrame_value_zero_vs_nil(t *testing.T) {
	grpcFrame := &pb.CallFrame{Value: make([]byte, 32)} // 32 zero bytes = Some(U256::ZERO)
	rpc := rpcCallFrame{Value: ""}                      // absent = None
	mm, _, _ := compareFrame("tx[0]", grpcFrame, rpc)
	found := false
	for _, m := range mm {
		if strings.Contains(m, ".value:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected value mismatch (0x0 vs empty), got: %v", mm)
	}
}

func TestCompareFrame_value_nil_nil(t *testing.T) {
	grpcFrame := &pb.CallFrame{Value: nil}
	rpc := rpcCallFrame{Value: ""}
	mm, _, _ := compareFrame("tx[0]", grpcFrame, rpc)
	for _, m := range mm {
		if strings.Contains(m, ".value:") {
			t.Fatalf("both absent value should match: %v", mm)
		}
	}
}

func TestCompareFrame_calls_count_mismatch(t *testing.T) {
	grpcFrame := &pb.CallFrame{
		Calls: []*pb.CallFrame{{Typ: "CALL"}, {Typ: "CALL"}},
	}
	rpc := rpcCallFrame{
		Calls: []rpcCallFrame{{Type: "CALL"}},
	}
	mm, _, _ := compareFrame("tx[0]", grpcFrame, rpc)
	found := false
	for _, m := range mm {
		if strings.Contains(m, ".calls:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected calls count mismatch: %v", mm)
	}
}

func TestCompareFrame_recursive_pass(t *testing.T) {
	grpcFrame := &pb.CallFrame{Calls: []*pb.CallFrame{{Typ: "CALL"}}}
	rpc := rpcCallFrame{Calls: []rpcCallFrame{{Type: "CALL"}}}
	mm, _, _ := compareFrame("tx[0]", grpcFrame, rpc)
	if len(mm) != 0 {
		t.Fatalf("expected 0 mismatches, got: %v", mm)
	}
}

func TestCompareFrame_recursive_mismatch(t *testing.T) {
	grpcFrame := &pb.CallFrame{Calls: []*pb.CallFrame{{Typ: "CALL"}}}
	rpc := rpcCallFrame{Calls: []rpcCallFrame{{Type: "DELEGATECALL"}}}
	mm, _, _ := compareFrame("tx[0]", grpcFrame, rpc)
	found := false
	for _, m := range mm {
		if strings.Contains(m, ".calls[0].typ") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected .calls[0].typ mismatch: %v", mm)
	}
}

func TestCompareLog_pass(t *testing.T) {
	addr := make([]byte, 20)
	addr[0] = 0x11
	topic := make([]byte, 32)
	topic[0] = 0xaa
	pos := uint64(3)
	grpcLog := &pb.CallLogFrame{
		Address:     addr,
		Topics:      [][]byte{topic},
		Data:        []byte{0x01},
		HasPosition: true,
		Position:    3,
		HasIndex:    false,
	}
	rpc := rpcLogFrame{
		Address:  normalizeAddress(addr),
		Topics:   []string{normalizeBytes(topic)},
		Data:     normalizeBytes([]byte{0x01}),
		Position: &pos,
	}
	mm := compareLog("tx[0].logs[0]", grpcLog, rpc)
	if len(mm) != 0 {
		t.Fatalf("expected 0 mismatches, got: %v", mm)
	}
}

func TestCompareLog_topic_mismatch(t *testing.T) {
	topicG := make([]byte, 32)
	topicG[0] = 0xaa
	topicR := "0x" + string(make([]byte, 64)) // different value
	grpcLog := &pb.CallLogFrame{Topics: [][]byte{topicG}}
	rpc := rpcLogFrame{Topics: []string{topicR}}
	mm := compareLog("tx[0].logs[0]", grpcLog, rpc)
	found := false
	for _, m := range mm {
		if strings.Contains(m, ".topics[0]") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected topics[0] mismatch: %v", mm)
	}
}

func TestCompareLog_position_rpc_nil_grpc_set(t *testing.T) {
	grpcLog := &pb.CallLogFrame{HasPosition: true, Position: 3}
	rpc := rpcLogFrame{Position: nil}
	mm := compareLog("tx[0].logs[0]", grpcLog, rpc)
	found := false
	for _, m := range mm {
		if strings.Contains(m, ".position:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected position mismatch: %v", mm)
	}
}

func TestCompareLog_position_match(t *testing.T) {
	pos := uint64(3)
	grpcLog := &pb.CallLogFrame{HasPosition: true, Position: 3}
	rpc := rpcLogFrame{Position: &pos}
	mm := compareLog("tx[0].logs[0]", grpcLog, rpc)
	for _, m := range mm {
		if strings.Contains(m, ".position:") {
			t.Fatalf("position should match: %v", mm)
		}
	}
}

func TestCompareLog_index_rpc_present_grpc_absent(t *testing.T) {
	idx := uint64(0)
	grpcLog := &pb.CallLogFrame{HasIndex: false}
	rpc := rpcLogFrame{Index: &idx}
	mm := compareLog("tx[0].logs[0]", grpcLog, rpc)
	found := false
	for _, m := range mm {
		if strings.Contains(m, ".index:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected index mismatch: %v", mm)
	}
}

func TestCompareBlock_tx_count_mismatch(t *testing.T) {
	grpcFrames := []*pb.CallFrame{{Typ: "CALL"}, {Typ: "CALL"}, {Typ: "CALL"}}
	rpcFrames := []rpcCallFrame{{Type: "CALL"}, {Type: "CALL"}}
	mm, _, _ := compareBlock(100, grpcFrames, rpcFrames)
	if len(mm) != 1 {
		t.Fatalf("expected 1 mismatch, got %d: %v", len(mm), mm)
	}
	if !strings.Contains(mm[0], "tx count:") {
		t.Fatalf("mismatch should mention tx count: %v", mm)
	}
}

func TestCompareBlock_all_pass(t *testing.T) {
	from := make([]byte, 20)
	from[0] = 0xbb
	grpcFrames := []*pb.CallFrame{
		{Typ: "CALL", From: from, GasUsed: gas(0x100)},
		{Typ: "CALL", From: from, GasUsed: gas(0x200)},
	}
	rpcFrames := []rpcCallFrame{
		{Type: "CALL", From: normalizeAddress(from), GasUsed: "0x100"},
		{Type: "CALL", From: normalizeAddress(from), GasUsed: "0x200"},
	}
	mm, fc, lc := compareBlock(100, grpcFrames, rpcFrames)
	if len(mm) != 0 {
		t.Fatalf("expected 0 mismatches, got: %v", mm)
	}
	if fc != 2 {
		t.Fatalf("expected frameCount=2, got %d", fc)
	}
	if lc != 0 {
		t.Fatalf("expected logCount=0, got %d", lc)
	}
}

func TestCompareBlock_empty(t *testing.T) {
	mm, fc, lc := compareBlock(100, nil, nil)
	if len(mm) != 0 {
		t.Fatalf("expected 0 mismatches for empty block, got: %v", mm)
	}
	if fc != 0 || lc != 0 {
		t.Fatalf("expected frameCount=0 logCount=0, got fc=%d lc=%d", fc, lc)
	}
}
