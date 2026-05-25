package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

var wsSubscribeMsg = []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_subscribe","params":["newHeads"]}`)

type newHeadsResult struct {
	Number string `json:"number"`
}

type wsNotification struct {
	Params struct {
		Result newHeadsResult `json:"result"`
	} `json:"params"`
}

// wsAck is the JSON-RPC response to the eth_subscribe request.
type wsAck struct {
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// SubscribeWS connects to wsURL, subscribes to eth_subscribe("newHeads"), and
// for each new block immediately calls eth_getBlockReceipts on rpcURL. The
// arrival time is recorded after the HTTP call returns. Errors are sent to
// errCh. The goroutine exits when ctx is cancelled or a fatal error occurs.
func SubscribeWS(ctx context.Context, wsURL, rpcURL string, ch chan<- arrival, errCh chan<- error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		errCh <- fmt.Errorf("ws dial %s: %w", wsURL, err)
		return
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, wsSubscribeMsg); err != nil {
		errCh <- fmt.Errorf("ws subscribe write: %w", err)
		return
	}

	// Read and validate the subscription confirmation (id=1 response).
	_, ackMsg, err := conn.ReadMessage()
	if err != nil {
		errCh <- fmt.Errorf("ws subscribe ack: %w", err)
		return
	}
	var ack wsAck
	if jsonErr := json.Unmarshal(ackMsg, &ack); jsonErr == nil && ack.Error != nil {
		errCh <- fmt.Errorf("ws subscribe rejected: %s", ack.Error.Message)
		return
	}

	fmt.Printf("ws connected: %s\n", wsURL)

	// Unblock ReadMessage when ctx is cancelled.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			errCh <- fmt.Errorf("ws read: %w", err)
			return
		}

		var notif wsNotification
		if err := json.Unmarshal(msg, &notif); err != nil {
			continue
		}

		blockNum, err := strconv.ParseUint(notif.Params.Result.Number, 0, 64)
		if err != nil || blockNum == 0 {
			continue
		}

		arrivedAt, err := fetchBlockReceipts(ctx, rpcURL, blockNum)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			errCh <- fmt.Errorf("eth_getBlockReceipts block=%d: %w", blockNum, err)
			return
		}

		select {
		case ch <- arrival{blockNumber: blockNum, arrivedAtMs: arrivedAt}:
		case <-ctx.Done():
			return
		}
	}
}

// fetchBlockReceipts calls eth_getBlockReceipts for the given block number and
// returns the time (Unix millis) immediately after the HTTP response returns.
func fetchBlockReceipts(ctx context.Context, rpcURL string, blockNum uint64) (int64, error) {
	hexNum := fmt.Sprintf("0x%x", blockNum)
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"eth_getBlockReceipts","params":["%s"]}`, hexNum)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewBufferString(body))
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("http post: %w", err)
	}
	// Record time immediately after response is received.
	arrivedAt := time.Now().UnixMilli()
	defer resp.Body.Close()
	// Drain so the underlying TCP connection can be reused. A drain error only
	// means the connection is discarded; arrivedAt is already captured above.
	io.Copy(io.Discard, resp.Body) //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("http status %d", resp.StatusCode)
	}

	return arrivedAt, nil
}
