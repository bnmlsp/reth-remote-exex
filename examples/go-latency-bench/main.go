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
	defaultBlocks = 100

	// matchTimeout is how long we wait for the second side before marking a block unmatched.
	matchTimeout = 10 * time.Second

	// timeoutScanInterval is how often we scan pending for timed-out entries.
	timeoutScanInterval = time.Second
)

// arrival records a single-side notification event.
type arrival struct {
	blockNumber uint64
	arrivedAtMs int64 // time.Now().UnixMilli(), Unix epoch millis; same basis on both sides so delta = wsMs - grpcMs
}

// partialArrival holds an in-progress match for one block number.
type partialArrival struct {
	wsMs   int64     // 0 means not yet arrived
	grpcMs int64     // 0 means not yet arrived
	since  time.Time // when the first side arrived, used for timeout
}

func main() {
	wsURL    := flag.String("ws", defaultWS, "WebSocket endpoint for eth_subscribe(newHeads)")
	grpcAddr := flag.String("grpc", defaultGRPC, "ExEx gRPC endpoint")
	blocks   := flag.Int("blocks", defaultBlocks, "number of matched blocks before exit (0 = exit immediately)")
	flag.Parse()

	if *blocks == 0 {
		printStats(0, 0, nil)
		os.Exit(0)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	wsCh   := make(chan arrival, 32)
	grpcCh := make(chan arrival, 32)
	errCh  := make(chan error, 2)

	go SubscribeNewHeads(ctx, *wsURL, wsCh, errCh)
	go SubscribeGRPC(ctx, *grpcAddr, grpcCh, errCh)

	// pending holds blocks where only one side has arrived.
	// Accessed only in this goroutine — no locking needed.
	pending := make(map[uint64]*partialArrival)

	var (
		matched   int
		unmatched int
		deltas    []int64 // deltaMs per matched block; wsMs - grpcMs
	)

	ticker := time.NewTicker(timeoutScanInterval)
	defer ticker.Stop()

	target := *blocks

	for {
		select {
		case err := <-errCh:
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)

		case a := <-wsCh:
			p := getOrCreate(pending, a.blockNumber)
			p.wsMs = a.arrivedAtMs
			if p.grpcMs != 0 {
				matched, unmatched, deltas = emit(a.blockNumber, p.wsMs, p.grpcMs, matched, unmatched, deltas)
				delete(pending, a.blockNumber)
				if matched == target {
					printStats(matched, unmatched, deltas)
					os.Exit(0)
				}
			}

		case a := <-grpcCh:
			p := getOrCreate(pending, a.blockNumber)
			p.grpcMs = a.arrivedAtMs
			if p.wsMs != 0 {
				matched, unmatched, deltas = emit(a.blockNumber, p.wsMs, p.grpcMs, matched, unmatched, deltas)
				delete(pending, a.blockNumber)
				if matched == target {
					printStats(matched, unmatched, deltas)
					os.Exit(0)
				}
			}

		case now := <-ticker.C:
			for blockNum, p := range pending {
				if now.Sub(p.since) > matchTimeout {
					fmt.Printf("block=%d timeout (ws=%v grpc=%v)\n",
						blockNum, p.wsMs != 0, p.grpcMs != 0)
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

// getOrCreate returns the partialArrival for blockNum, creating it if needed.
func getOrCreate(pending map[uint64]*partialArrival, blockNum uint64) *partialArrival {
	p, ok := pending[blockNum]
	if !ok {
		p = &partialArrival{since: time.Now()}
		pending[blockNum] = p
	}
	return p
}

// emit prints one matched-block line and appends the delta to deltas.
func emit(blockNum uint64, wsMs, grpcMs int64, matched, unmatched int, deltas []int64) (int, int, []int64) {
	deltaMs := wsMs - grpcMs
	sign := "+"
	if deltaMs < 0 {
		sign = ""
	}
	fmt.Printf("block=%d ws=%dms grpc=%dms delta=%s%dms\n", blockNum, wsMs, grpcMs, sign, deltaMs)
	return matched + 1, unmatched, append(deltas, deltaMs)
}

// printStats prints the final summary.
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
	avg := sum / int64(len(deltas))
	fmt.Printf("delta min=%s%dms max=%s%dms avg=%s%dms\n",
		signPrefix(minD), minD,
		signPrefix(maxD), maxD,
		signPrefix(avg), avg,
	)
}

// signPrefix returns "+" for non-negative values, "" for negatives (fmt prints "-" automatically).
func signPrefix(v int64) string {
	if v >= 0 {
		return "+"
	}
	return ""
}
