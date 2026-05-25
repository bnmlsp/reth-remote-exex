package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	pb "state_diff_bench/proto/gen"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const grpcMaxRecvMsgSizeBytes = 64 * 1024 * 1024 // 64 MiB

// SubscribeGRPC connects to grpcAddr and subscribes with include_headers=true
// and include_state_diff=true. For each ChainCommitted notification it writes
// the tip block's arrival event to ch. Other notification types are ignored.
// Errors are sent to errCh. The goroutine exits when ctx is cancelled or a
// fatal error occurs.
func SubscribeGRPC(ctx context.Context, grpcAddr string, ch chan<- arrival, errCh chan<- error) {
	conn, err := grpc.NewClient(
		grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(grpcMaxRecvMsgSizeBytes)),
	)
	if err != nil {
		errCh <- fmt.Errorf("grpc connect %s: %w", grpcAddr, err)
		return
	}
	defer conn.Close()

	client := pb.NewRemoteExExClient(conn)
	stream, err := client.Subscribe(ctx, &pb.SubscribeRequest{
		IncludeHeaders:   true,
		IncludeStateDiff: true,
	})
	if err != nil {
		errCh <- fmt.Errorf("grpc subscribe: %w", err)
		return
	}

	fmt.Printf("grpc connected: %s\n", grpcAddr)

	for {
		notif, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			errCh <- fmt.Errorf("grpc stream closed by server")
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
			continue
		}

		chain := committed.ChainCommitted.New
		if chain == nil || len(chain.Blocks) == 0 {
			continue
		}

		// Use the tip block (last in the committed segment).
		tip := chain.Blocks[len(chain.Blocks)-1]
		if tip.Header == nil {
			continue
		}

		var accounts uint64
		if chain.StateDiff != nil {
			accounts = uint64(len(chain.StateDiff.Accounts))
		}

		select {
		case ch <- arrival{blockNumber: tip.Header.Number, arrivedAtMs: arrivedAt, accounts: accounts}:
		case <-ctx.Done():
			return
		}
	}
}
