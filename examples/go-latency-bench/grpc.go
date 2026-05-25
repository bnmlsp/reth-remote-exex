package main

import (
	"context"
	"fmt"
	"io"
	"time"

	pb "latency_bench/proto/gen"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const grpcMaxRecvMsgSize = 64 * 1024 * 1024

// SubscribeGRPC connects to grpcAddr and subscribes with include_headers=true.
// For each ChainCommitted notification it writes the tip block's arrival event
// to ch. Other notification types (reorged, reverted) are ignored.
// Errors are sent to errCh. The goroutine exits when ctx is cancelled or a
// fatal error occurs.
func SubscribeGRPC(ctx context.Context, grpcAddr string, ch chan<- arrival, errCh chan<- error) {
	conn, err := grpc.NewClient(
		grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(grpcMaxRecvMsgSize)),
	)
	if err != nil {
		errCh <- fmt.Errorf("grpc connect %s: %w", grpcAddr, err)
		return
	}
	defer conn.Close()

	client := pb.NewRemoteExExClient(conn)
	stream, err := client.Subscribe(ctx, &pb.SubscribeRequest{
		IncludeHeaders: true,
	})
	if err != nil {
		errCh <- fmt.Errorf("grpc subscribe: %w", err)
		return
	}

	fmt.Printf("grpc connected: %s\n", grpcAddr)

	for {
		notif, err := stream.Recv()
		if err == io.EOF {
			return
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			errCh <- fmt.Errorf("grpc recv: %w", err)
			return
		}

		arrivedAt := time.Now().UnixMilli()

		committed, ok := notif.Event.(*pb.Notification_ChainCommitted)
		if !ok {
			// ChainReorged / ChainReverted: not relevant for latency comparison.
			continue
		}

		chain := committed.ChainCommitted.New
		if chain == nil || len(chain.Blocks) == 0 {
			// Empty committed chain: should not occur in practice; skip defensively.
			continue
		}

		// Use the tip block (last in the committed segment).
		tip := chain.Blocks[len(chain.Blocks)-1]
		if tip.Header == nil {
			// Header absent: IncludeHeaders flag not honoured; skip defensively.
			continue
		}

		select {
		case ch <- arrival{blockNumber: tip.Header.Number, arrivedAtMs: arrivedAt}:
		case <-ctx.Done():
			return
		}
	}
}
