package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	defaultWS     = "ws://127.0.0.1:8546"
	defaultGRPC   = "127.0.0.1:10000"
	defaultRPC    = "http://127.0.0.1:8545"
	defaultBlocks = 10

	matchTimeout        = 10 * time.Second
	timeoutScanInterval = time.Second

	// chCapacity is the buffer size for per-source arrival channels.
	// 32 is enough to absorb bursts during reorgs without blocking subscriber goroutines.
	chCapacity = 32
	// errChCapacity matches the number of subscriber goroutines so neither blocks
	// on send; only the first error received is reported and the process exits.
	errChCapacity = 2
)

// arrival records a single-side notification event.
type arrival struct {
	blockNumber uint64
	arrivedAtMs int64  // time.Now().UnixMilli(); delta = grpcMs - rpcMs
	accounts    uint64 // number of changed accounts (gRPC side only; 0 on WS side)
}

// partialArrival holds an in-progress match for one block number.
type partialArrival struct {
	grpcMs   int64     // 0 means not yet arrived
	rpcMs    int64     // 0 means not yet arrived
	since    time.Time // when the first side arrived, used for timeout
	accounts uint64
}

func main() {
	grpcAddr := flag.String("grpc", defaultGRPC, "ExEx gRPC endpoint")
	wsURL    := flag.String("ws", defaultWS, "WebSocket endpoint for eth_subscribe(newHeads)")
	rpcURL   := flag.String("rpc", defaultRPC, "HTTP JSON-RPC endpoint for trace_replayBlockTransactions")
	blocks   := flag.Int("blocks", defaultBlocks, "number of matched blocks before exit (0 = exit immediately)")
	flag.Parse()

	if *blocks < 0 {
		fmt.Fprintf(os.Stderr, "error: --blocks must be >= 0, got %d\n", *blocks)
		os.Exit(1)
	}
	if *blocks == 0 {
		printStats(0, 0, nil)
		os.Exit(0)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	grpcCh := make(chan arrival, chCapacity)
	rpcCh  := make(chan arrival, chCapacity)
	errCh  := make(chan error, errChCapacity)

	go SubscribeGRPC(ctx, *grpcAddr, grpcCh, errCh)
	go SubscribeWS(ctx, *wsURL, *rpcURL, rpcCh, errCh)

	// pending holds blocks where only one side has arrived.
	// Accessed only in this goroutine — no locking needed.
	pending := make(map[uint64]*partialArrival)

	var (
		matched   int
		unmatched int
		deltas    []int64 // grpcMs - rpcMs per matched block
	)

	ticker := time.NewTicker(timeoutScanInterval)
	defer ticker.Stop()

	target := *blocks

	for {
		select {
		case err := <-errCh:
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)

		case a := <-grpcCh:
			p := getOrCreate(pending, a.blockNumber)
			p.grpcMs = a.arrivedAtMs
			p.accounts = a.accounts
			if p.rpcMs != 0 {
				matched, unmatched, deltas = emit(a.blockNumber, p.accounts, p.grpcMs, p.rpcMs, matched, unmatched, deltas)
				delete(pending, a.blockNumber)
				if matched == target {
					printStats(matched, unmatched, deltas)
					os.Exit(0)
				}
			}

		case a := <-rpcCh:
			p := getOrCreate(pending, a.blockNumber)
			p.rpcMs = a.arrivedAtMs
			if p.grpcMs != 0 {
				// p.accounts was written by the grpcCh case above.
				matched, unmatched, deltas = emit(a.blockNumber, p.accounts, p.grpcMs, p.rpcMs, matched, unmatched, deltas)
				delete(pending, a.blockNumber)
				if matched == target {
					printStats(matched, unmatched, deltas)
					os.Exit(0)
				}
			}

		case now := <-ticker.C:
			for blockNum, p := range pending {
				if now.Sub(p.since) > matchTimeout {
					fmt.Printf("block=%d timeout (grpc=%v rpc=%v)\n",
						blockNum, p.grpcMs != 0, p.rpcMs != 0)
					unmatched++
					delete(pending, blockNum)
				}
			}

		case <-ctx.Done():
			printStats(matched, unmatched, deltas)
			os.Exit(0)
		}
	}
}

func getOrCreate(pending map[uint64]*partialArrival, blockNum uint64) *partialArrival {
	p, ok := pending[blockNum]
	if !ok {
		p = &partialArrival{since: time.Now()}
		pending[blockNum] = p
	}
	return p
}

// emit prints one matched-block line and appends the delta to deltas.
// delta = grpcMs - rpcMs; negative means gRPC faster.
func emit(blockNum, accounts uint64, grpcMs, rpcMs int64, matched, unmatched int, deltas []int64) (int, int, []int64) {
	deltaMs := grpcMs - rpcMs
	sign := "+"
	if deltaMs < 0 {
		sign = ""
	}
	fmt.Printf("block=%d accounts=%d grpc=%dms rpc=%dms delta=%s%dms\n", blockNum, accounts, grpcMs, rpcMs, sign, deltaMs)
	return matched + 1, unmatched, append(deltas, deltaMs)
}

func printStats(matched, unmatched int, deltas []int64) {
	fmt.Println("--- results ---")
	fmt.Printf("matched=%d unmatched=%d\n", matched, unmatched)
	if len(deltas) == 0 {
		fmt.Println("(no data)")
		return
	}
	var sum, minD, maxD int64
	minD = deltas[0]
	maxD = deltas[0]
	for _, d := range deltas {
		sum += d
		if d < minD {
			minD = d
		}
		if d > maxD {
			maxD = d
		}
	}
	avg := sum / int64(len(deltas)) // integer ms; sub-ms precision not needed
	fmt.Printf("delta min=%s%dms max=%s%dms avg=%s%dms\n",
		signPrefix(minD), minD,
		signPrefix(maxD), maxD,
		signPrefix(avg), avg,
	)
}

func signPrefix(v int64) string {
	if v >= 0 {
		return "+"
	}
	return ""
}
