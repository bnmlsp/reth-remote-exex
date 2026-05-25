package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

// wsSubscribeMsg is the JSON-RPC request sent to subscribe to newHeads.
var wsSubscribeMsg = []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_subscribe","params":["newHeads"]}`)

// newHeadsResult is the minimal fields we need from a newHeads notification.
type newHeadsResult struct {
	Number string `json:"number"` // hex-encoded block number, e.g. "0x1406b3e"
}

// wsNotification is the outer envelope for eth_subscription push messages.
type wsNotification struct {
	Params struct {
		Result newHeadsResult `json:"result"`
	} `json:"params"`
}

// SubscribeNewHeads connects to wsURL, subscribes to eth_subscribe("newHeads"),
// and writes each new block's arrival event to ch. Errors are sent to errCh.
// The goroutine exits when ctx is cancelled or a fatal error occurs.
func SubscribeNewHeads(ctx context.Context, wsURL string, ch chan<- arrival, errCh chan<- error) {
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		errCh <- fmt.Errorf("ws dial %s: %w", wsURL, err)
		return
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, wsSubscribeMsg); err != nil {
		errCh <- fmt.Errorf("ws subscribe write: %w", err)
		return
	}

	// Read and discard the subscription confirmation (id=1 response).
	if _, _, err := conn.ReadMessage(); err != nil {
		errCh <- fmt.Errorf("ws subscribe ack: %w", err)
		return
	}

	fmt.Printf("ws connected: %s\n", wsURL)

	// Set a channel to unblock ReadMessage when ctx is cancelled.
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

		arrivedAt := time.Now().UnixMilli()

		var notif wsNotification
		if err := json.Unmarshal(msg, &notif); err != nil {
			// Non-notification messages (e.g. ping frames) are not JSON we care about; skip.
			continue
		}

		blockNum, err := strconv.ParseUint(notif.Params.Result.Number, 0, 64)
		if err != nil || blockNum == 0 {
			// blockNum == 0: subscription result message or missing number field; not a block event.
			continue
		}

		select {
		case ch <- arrival{blockNumber: blockNum, arrivedAtMs: arrivedAt}:
		case <-ctx.Done():
			return
		}
	}
}
