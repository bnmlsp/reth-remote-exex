package main

import (
	"encoding/hex"
	"fmt"
	"strings"

	pb "exex_trace_verifier/proto/gen"
)

func compareBlock(blockNum uint64, grpcFrames []*pb.CallFrame, rpcFrames []rpcCallFrame) (mismatches []string, frameCount int, logCount int) {
	if len(grpcFrames) != len(rpcFrames) {
		return []string{fmt.Sprintf("tx count: grpc=%d rpc=%d", len(grpcFrames), len(rpcFrames))}, 0, 0
	}
	for i := range grpcFrames {
		path := fmt.Sprintf("tx[%d]", i)
		mm, fc, lc := compareFrame(path, grpcFrames[i], rpcFrames[i])
		mismatches = append(mismatches, mm...)
		frameCount += fc
		logCount += lc
	}
	return mismatches, frameCount, logCount
}

func compareFrame(path string, grpcFrame *pb.CallFrame, rpc rpcCallFrame) (mismatches []string, frameCount int, logCount int) {
	frameCount = 1

	check := func(field, gVal, rVal string) {
		if gVal != rVal {
			mismatches = append(mismatches, fmt.Sprintf("%s.%s: grpc=%s rpc=%s", path, field, gVal, rVal))
		}
	}

	check("typ", grpcFrame.Typ, rpc.Type)
	check("from", normalizeAddress(grpcFrame.From), normalizeHexStr(rpc.From))
	check("to", normalizeAddress(grpcFrame.To), normalizeHexStr(rpc.To))
	check("value", normalizeU256grpc(grpcFrame.Value), normalizeU256rpc(rpc.Value))
	check("gas", normalizeU256grpc(grpcFrame.Gas), normalizeU256rpc(rpc.Gas))
	check("gasUsed", normalizeU256grpc(grpcFrame.GasUsed), normalizeU256rpc(rpc.GasUsed))
	check("input", normalizeBytes(grpcFrame.Input), normalizeHexStr(rpc.Input))
	check("output", normalizeBytes(grpcFrame.Output), normalizeHexStr(rpc.Output))
	check("error", grpcFrame.Error, rpc.Error)
	check("revertReason", grpcFrame.RevertReason, rpc.RevertReason)

	if len(grpcFrame.Calls) != len(rpc.Calls) {
		mismatches = append(mismatches, fmt.Sprintf("%s.calls: grpc=%d rpc=%d", path, len(grpcFrame.Calls), len(rpc.Calls)))
	} else {
		for i := range grpcFrame.Calls {
			mm, fc, lc := compareFrame(fmt.Sprintf("%s.calls[%d]", path, i), grpcFrame.Calls[i], rpc.Calls[i])
			mismatches = append(mismatches, mm...)
			frameCount += fc
			logCount += lc
		}
	}

	if len(grpcFrame.Logs) != len(rpc.Logs) {
		mismatches = append(mismatches, fmt.Sprintf("%s.logs: grpc=%d rpc=%d", path, len(grpcFrame.Logs), len(rpc.Logs)))
	} else {
		for i := range grpcFrame.Logs {
			mm := compareLog(fmt.Sprintf("%s.logs[%d]", path, i), grpcFrame.Logs[i], rpc.Logs[i])
			mismatches = append(mismatches, mm...)
			logCount++
		}
	}

	return mismatches, frameCount, logCount
}

func compareLog(path string, grpcLog *pb.CallLogFrame, rpc rpcLogFrame) []string {
	var mismatches []string

	check := func(field, gVal, rVal string) {
		if gVal != rVal {
			mismatches = append(mismatches, fmt.Sprintf("%s.%s: grpc=%s rpc=%s", path, field, gVal, rVal))
		}
	}

	check("address", normalizeAddress(grpcLog.Address), normalizeHexStr(rpc.Address))

	if len(grpcLog.Topics) != len(rpc.Topics) {
		mismatches = append(mismatches, fmt.Sprintf("%s.topics: grpc=%d rpc=%d", path, len(grpcLog.Topics), len(rpc.Topics)))
	} else {
		for i := range grpcLog.Topics {
			check(fmt.Sprintf("topics[%d]", i), normalizeBytes(grpcLog.Topics[i]), normalizeHexStr(rpc.Topics[i]))
		}
	}

	check("data", normalizeBytes(grpcLog.Data), normalizeHexStr(rpc.Data))

	if rpc.Position == nil {
		if grpcLog.HasPosition {
			mismatches = append(mismatches, fmt.Sprintf("%s.position: grpc=present(%d) rpc=absent", path, grpcLog.Position))
		}
	} else {
		if !grpcLog.HasPosition {
			mismatches = append(mismatches, fmt.Sprintf("%s.position: grpc=absent rpc=present(%d)", path, *rpc.Position))
		} else if grpcLog.Position != *rpc.Position {
			mismatches = append(mismatches, fmt.Sprintf("%s.position: grpc=%d rpc=%d", path, grpcLog.Position, *rpc.Position))
		}
	}

	if rpc.Index == nil {
		if grpcLog.HasIndex {
			mismatches = append(mismatches, fmt.Sprintf("%s.index: grpc=present(%d) rpc=absent", path, grpcLog.Index))
		}
	} else {
		if !grpcLog.HasIndex {
			mismatches = append(mismatches, fmt.Sprintf("%s.index: grpc=absent rpc=present(%d)", path, *rpc.Index))
		} else if grpcLog.Index != *rpc.Index {
			mismatches = append(mismatches, fmt.Sprintf("%s.index: grpc=%d rpc=%d", path, grpcLog.Index, *rpc.Index))
		}
	}

	return mismatches
}

func normalizeAddress(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return "0x" + hex.EncodeToString(b)
}

func normalizeU256grpc(b []byte) string {
	if len(b) == 0 {
		// nil/empty bytes = Option::None (absent value)
		return ""
	}
	s := hex.EncodeToString(b)
	s = strings.TrimLeft(s, "0")
	if s == "" {
		// 32 zero bytes = Option::Some(U256::ZERO); distinct from None
		return "0x0"
	}
	return "0x" + s
}

func normalizeU256rpc(s string) string {
	if s == "" {
		return ""
	}
	after, ok := strings.CutPrefix(s, "0x")
	if !ok {
		return s
	}
	trimmed := strings.TrimLeft(after, "0")
	if trimmed == "" {
		return "0x0"
	}
	return "0x" + trimmed
}

func normalizeBytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return "0x" + hex.EncodeToString(b)
}

func normalizeHexStr(s string) string {
	if s == "" || s == "0x" {
		return ""
	}
	return strings.ToLower(s)
}
