package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
)

type rpcResult struct {
	Result rpcCallFrame `json:"result"`
}

type rpcCallFrame struct {
	Type         string         `json:"type"`
	From         string         `json:"from"`
	To           string         `json:"to,omitempty"`
	Value        string         `json:"value,omitempty"`
	Gas          string         `json:"gas"`
	GasUsed      string         `json:"gasUsed"`
	Input        string         `json:"input"`
	Output       string         `json:"output,omitempty"`
	Error        string         `json:"error,omitempty"`
	RevertReason string         `json:"revertReason,omitempty"`
	Calls        []rpcCallFrame `json:"calls,omitempty"`
	Logs         []rpcLogFrame  `json:"logs,omitempty"`
}

type rpcLogFrame struct {
	Address  string   `json:"address"`
	Topics   []string `json:"topics,omitempty"`
	Data     string   `json:"data"`
	Position *uint64  `json:"position,omitempty"`
	Index    *uint64  `json:"index,omitempty"`
}

func fetchRPCTraces(ctx context.Context, rpcURL string, blockNumber uint64) ([]rpcCallFrame, error) {
	frames, err := doRPCCall(ctx, rpcURL, blockNumber)
	if err != nil {
		log.Printf("RPC attempt 1 failed for block#%d: %v", blockNumber, err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(rpcRetryDelay):
		}
		frames, err = doRPCCall(ctx, rpcURL, blockNumber)
		if err != nil {
			return nil, fmt.Errorf("RPC attempt 2 failed for block#%d: %w", blockNumber, err)
		}
		log.Printf("RPC retry succeeded for block#%d", blockNumber)
	}
	return frames, nil
}

func doRPCCall(ctx context.Context, rpcURL string, blockNumber uint64) ([]rpcCallFrame, error) {
	hexBlock := "0x" + strconv.FormatUint(blockNumber, 16)
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "debug_traceBlockByNumber",
		"params":  []any{hexBlock, map[string]string{"tracer": "callTracer"}},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %d for block#%d", resp.StatusCode, blockNumber)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	var rpcResp struct {
		Result []rpcResult `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("RPC error: %s", rpcResp.Error.Message)
	}

	frames := make([]rpcCallFrame, len(rpcResp.Result))
	for i, r := range rpcResp.Result {
		frames[i] = r.Result
	}
	return frames, nil
}
