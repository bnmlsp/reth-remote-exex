package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	pb "exex_trace_verifier/proto/gen"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	maxRecvMsgSize = 64 * 1024 * 1024
	workChBuffer   = 32
	rpcRetryDelay  = 500 * time.Millisecond
)

type blockWork struct {
	blockNumber uint64
	grpcFrames  []*pb.CallFrame
}

type blockResult struct {
	blockNumber uint64
	txCount     int
	frameCount  int
	logCount    int
	mismatches  []string
	rpcError    bool
}

func main() {
	grpcAddr := flag.String("grpc", "127.0.0.1:10000", "gRPC server address")
	rpcURL := flag.String("rpc", "http://127.0.0.1:8545", "Ethereum JSON-RPC URL")
	workers := flag.Int("workers", 4, "number of parallel comparison workers")
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: go run . <n> [--grpc ADDR] [--rpc URL] [--workers W]")
		os.Exit(2)
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n <= 0 {
		fmt.Fprintf(os.Stderr, "n must be a positive integer, got %q\n", args[0])
		os.Exit(2)
	}
	if *workers <= 0 {
		fmt.Fprintf(os.Stderr, "--workers must be a positive integer, got %d\n", *workers)
		os.Exit(2)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workCh := make(chan blockWork, workChBuffer)
	resultCh := make(chan blockResult, *workers*2)

	go streamLoop(ctx, *grpcAddr, workCh, n)

	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go workerLoop(ctx, *rpcURL, workCh, resultCh, &wg)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	var (
		totalTx         int
		totalFrames     int
		totalLogs       int
		totalMismatches int
		blocksVerified  int
		failed          bool
	)

	for res := range resultCh {
		blocksVerified++
		totalTx += res.txCount
		totalFrames += res.frameCount
		totalLogs += res.logCount
		totalMismatches += len(res.mismatches)
		if res.rpcError || len(res.mismatches) > 0 {
			failed = true
		}

		if res.rpcError {
			fmt.Printf("block#%d RPC_ERROR\n", res.blockNumber)
		} else if len(res.mismatches) == 0 {
			fmt.Printf("block#%d PASS (%d txs, %d frames, %d logs)\n",
				res.blockNumber, res.txCount, res.frameCount, res.logCount)
		} else {
			fmt.Printf("block#%d MISMATCH (%d errors):\n", res.blockNumber, len(res.mismatches))
			for _, m := range res.mismatches {
				fmt.Printf("  %s\n", m)
			}
		}
	}

	fmt.Println()
	fmt.Println("=== Summary ===")
	fmt.Printf("Blocks verified:   %d\n", blocksVerified)
	fmt.Printf("Total tx traces:   %d\n", totalTx)
	fmt.Printf("Total frames:      %d\n", totalFrames)
	fmt.Printf("Total logs:        %d\n", totalLogs)
	fmt.Printf("Total mismatches:  %d\n", totalMismatches)

	if failed {
		os.Exit(1)
	}
}

func streamLoop(ctx context.Context, grpcAddr string, workCh chan<- blockWork, n int) {
	defer close(workCh)

	conn, err := grpc.NewClient(grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxRecvMsgSize)),
	)
	if err != nil {
		log.Printf("streamLoop: connect to %s failed: %v", grpcAddr, err)
		return
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil {
			log.Printf("streamLoop: close gRPC connection: %v", cerr)
		}
	}()

	client := pb.NewRemoteExExClient(conn)
	stream, err := client.Subscribe(ctx, &pb.SubscribeRequest{IncludeCallTraces: true})
	if err != nil {
		log.Printf("streamLoop: subscribe failed: %v", err)
		return
	}

	sent := 0
	for sent < n {
		notif, err := stream.Recv()
		if err == io.EOF {
			log.Println("streamLoop: stream closed by server")
			return
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("streamLoop: recv error: %v", err)
			return
		}

		cc, ok := notif.Event.(*pb.Notification_ChainCommitted)
		if !ok {
			continue
		}
		chain := cc.ChainCommitted.New
		if chain == nil {
			continue
		}

		for _, bct := range chain.CallTraces {
			if len(bct.Txs) == 0 {
				continue
			}
			select {
			case workCh <- blockWork{blockNumber: bct.BlockNumber, grpcFrames: bct.Txs}:
				sent++
				if sent >= n {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}
}

func workerLoop(ctx context.Context, rpcURL string, workCh <-chan blockWork, resultCh chan<- blockResult, wg *sync.WaitGroup) {
	defer wg.Done()

	for work := range workCh {
		rpcFrames, err := fetchRPCTraces(ctx, rpcURL, work.blockNumber)
		if err != nil {
			log.Printf("workerLoop: RPC_ERROR block#%d: %v", work.blockNumber, err)
			resultCh <- blockResult{blockNumber: work.blockNumber, rpcError: true}
			continue
		}

		mismatches, frameCount, logCount := compareBlock(work.blockNumber, work.grpcFrames, rpcFrames)
		resultCh <- blockResult{
			blockNumber: work.blockNumber,
			txCount:     len(work.grpcFrames),
			frameCount:  frameCount,
			logCount:    logCount,
			mismatches:  mismatches,
		}
	}
}
