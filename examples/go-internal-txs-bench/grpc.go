package main

import (
	"context"
	"fmt"
	"io"
	"time"

	pb "internal_txs_bench/proto/gen"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const grpcMaxRecvMsgSizeBytes = 64 * 1024 * 1024 // 64 MiB

// SubscribeGRPC connects to grpcAddr and subscribes with include_headers=true
// and include_call_traces=true. For each ChainCommitted notification it writes
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
		IncludeHeaders:    true,
		IncludeCallTraces: true,
	})
	if err != nil {
		errCh <- fmt.Errorf("grpc subscribe: %w", err)
		return
	}

	fmt.Printf("grpc connected: %s\n", grpcAddr)

	for {
		notif, err := stream.Recv()
		if err == io.EOF {
			fmt.Printf("grpc stream closed by server\n")
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

		var txCount uint64
		for _, ct := range chain.CallTraces {
			if ct.BlockNumber == tip.Header.Number {
				txCount = uint64(len(ct.Txs))
				break
			}
		}

		select {
		case ch <- arrival{blockNumber: tip.Header.Number, arrivedAtMs: arrivedAt, txCount: txCount, gasUsed: tip.Header.GasUsed}:
		case <-ctx.Done():
			return
		}
	}
}
