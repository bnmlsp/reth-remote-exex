package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	pb "exex_consumer/proto/gen"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// maxRecvMsgSize is the per-message receive limit for the gRPC client.
// Mainnet blocks rarely exceed 20 MB; 64 MB provides comfortable headroom.
const maxRecvMsgSize = 64 * 1024 * 1024

func main() {
	headers      := flag.Bool("headers", false, "subscribe to block headers")
	transactions := flag.Bool("transactions", false, "subscribe to transactions and senders")
	receipts     := flag.Bool("receipts", false, "subscribe to receipts")
	withdrawals  := flag.Bool("withdrawals", false, "subscribe to withdrawals")
	stateDiff    := flag.Bool("state-diff", false, "subscribe to state diffs")
	callTraces   := flag.Bool("call-traces", false, "subscribe to call traces")
	flag.Parse()

	addr := "[::1]:10000"
	if flag.NArg() > 0 {
		addr = flag.Arg(0)
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxRecvMsgSize)))
	if err != nil {
		log.Fatalf("connect to %s failed: %v", addr, err)
	}
	defer conn.Close()

	client := pb.NewRemoteExExClient(conn)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	stream, err := client.Subscribe(ctx, &pb.SubscribeRequest{
		IncludeHeaders:      *headers,
		IncludeTransactions: *transactions,
		IncludeReceipts:     *receipts,
		IncludeWithdrawals:  *withdrawals,
		IncludeStateDiff:    *stateDiff,
		IncludeCallTraces:   *callTraces,
	})
	if err != nil {
		log.Fatalf("subscribe failed: %v", err)
	}

	log.Printf("connected to %s, waiting for notifications...", addr)

	for {
		notif, err := stream.Recv()
		if err == io.EOF {
			log.Println("stream closed by server")
			return
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// Demo-level error handling: exits on any stream error.
			// Production consumers should implement reconnect logic here.
			log.Fatalf("recv error: %v", err)
		}
		printNotification(notif)
	}
}

// hexBytes encodes b as a "0x"-prefixed hex string, or "(empty)" for nil/empty input.
func hexBytes(b []byte) string {
	if len(b) == 0 {
		return "(empty)"
	}
	return "0x" + hex.EncodeToString(b)
}

// printNotification logs a human-readable summary of a received notification.
func printNotification(n *pb.Notification) {
	switch ev := n.Event.(type) {
	case *pb.Notification_ChainCommitted:
		printChain("COMMITTED new", ev.ChainCommitted.New)
	case *pb.Notification_ChainReorged:
		printChain("REORGED old", ev.ChainReorged.Old)
		printChain("REORGED new", ev.ChainReorged.New)
	case *pb.Notification_ChainReverted:
		printChain("REVERTED old", ev.ChainReverted.Old)
	}
}

// printChain prints block-level and state-diff details for a chain segment.
func printChain(label string, chain *pb.Chain) {
	if chain == nil {
		return
	}
	fmt.Printf("=== %s | %d blocks ===\n", label, len(chain.Blocks))
	for _, bwr := range chain.Blocks {
		hdr := bwr.Header
		var blockNum uint64
		if hdr != nil {
			blockNum = hdr.Number
		}
		fmt.Printf("  block #%d  txs=%d  receipts=%d  withdrawals=%d\n",
			blockNum, len(bwr.Txs), len(bwr.Receipts), len(bwr.Withdrawals))
		printTxSummary(bwr)
	}

	sd := chain.StateDiff
	if sd == nil {
		fmt.Println("  state_diff: (nil)")
	} else {
		fmt.Printf("  state_diff: accounts=%d  contracts=%d  revert_blocks=%d\n",
			len(sd.Accounts), len(sd.Contracts), len(sd.Reverts))

		// accounts
		for i, acc := range sd.Accounts {
			fmt.Printf("    account[%d] addr=%s  status=%s\n", i, hexBytes(acc.Address), acc.Status.String())
			if acc.OriginalInfo != nil {
				fmt.Printf("      original: balance=%s  nonce=%d  code_hash=%s\n",
					hexBytes(acc.OriginalInfo.Balance), acc.OriginalInfo.Nonce, hexBytes(acc.OriginalInfo.CodeHash))
			}
			if acc.Info != nil {
				fmt.Printf("      current:  balance=%s  nonce=%d  code_hash=%s\n",
					hexBytes(acc.Info.Balance), acc.Info.Nonce, hexBytes(acc.Info.CodeHash))
			}
			for j, slot := range acc.Storage {
				fmt.Printf("      storage[%d] key=%s  %s -> %s\n",
					j, hexBytes(slot.Key), hexBytes(slot.Previous), hexBytes(slot.Current))
			}
		}

		// contracts (new/changed bytecode)
		for i, c := range sd.Contracts {
			fmt.Printf("    contract[%d] code_hash=%s  bytecode_len=%d\n",
				i, hexBytes(c.CodeHash), len(c.Bytecode))
		}

		// reverts (per-block)
		for i, br := range sd.Reverts {
			fmt.Printf("    revert_block[%d]: %d account reverts\n", i, len(br.Accounts))
			for j, ar := range br.Accounts {
				fmt.Printf("      revert[%d] addr=%s  kind=%s  wipe_storage=%v\n",
					j, hexBytes(ar.Address), ar.Kind.String(), ar.WipeStorage)
				for k, sr := range ar.Storage {
					switch rv := sr.RevertTo.(type) {
					case *pb.StorageRevert_Value:
						fmt.Printf("        storage_revert[%d] key=%s -> value=%s\n", k, hexBytes(sr.Key), hexBytes(rv.Value))
					case *pb.StorageRevert_Destroyed:
						fmt.Printf("        storage_revert[%d] key=%s -> destroyed\n", k, hexBytes(sr.Key))
					}
				}
			}
		}
	}

	// call traces (internal txs)
	fmt.Printf("  call_traces: %d blocks\n", len(chain.CallTraces))
	for _, bct := range chain.CallTraces {
		fmt.Printf("    block #%d: %d tx traces\n", bct.BlockNumber, len(bct.Txs))
		for i, frame := range bct.Txs {
			fmt.Printf("      tx[%d]:\n", i)
			printCallFrame(frame, 8)
		}
	}
	fmt.Println()
}

// depositTxType is the OP-Stack Deposit transaction type (0x7E). Base places the
// L1 attributes deposit as the first transaction of every block.
const depositTxType = 126

// printTxSummary prints the tx_type distribution of a block, then expands the
// Deposit-specific fields of the first transaction and its receipt. Printing every
// transaction is impractical — Base blocks routinely carry 150-350 of them — and the
// first slot is where the L1 attributes deposit lives, so it is the one worth showing.
func printTxSummary(bwr *pb.BlockWithReceipts) {
	if len(bwr.Txs) == 0 {
		return
	}

	counts := make(map[uint32]int, 4)
	for _, tx := range bwr.Txs {
		counts[tx.TxType]++
	}
	txTypes := make([]uint32, 0, len(counts))
	for txType := range counts {
		txTypes = append(txTypes, txType)
	}
	// Sorted so repeated runs produce diffable output; Go map iteration order is random.
	sort.Slice(txTypes, func(i, j int) bool { return txTypes[i] < txTypes[j] })
	parts := make([]string, 0, len(txTypes))
	for _, txType := range txTypes {
		parts = append(parts, fmt.Sprintf("%d:%d", txType, counts[txType]))
	}
	fmt.Printf("    tx_types: {%s}\n", strings.Join(parts, ", "))

	tx := bwr.Txs[0]
	fmt.Printf("    tx[0] type=%d  hash=%s\n", tx.TxType, hexBytes(tx.Hash))
	if tx.TxType == depositTxType {
		fmt.Printf("      deposit: source_hash=%s  mint=%s  is_system_tx=%v\n",
			hexBytes(tx.SourceHash), hexBytes(tx.Mint), tx.IsSystemTransaction)
	}

	if len(bwr.Receipts) == 0 {
		return
	}
	receipt := bwr.Receipts[0]
	fmt.Printf("    receipt[0] type=%d  success=%v  cumulative_gas_used=%d  logs=%d\n",
		receipt.TxType, receipt.Success, receipt.CumulativeGasUsed, len(receipt.Logs))
	if receipt.HasDepositNonce || receipt.HasDepositReceiptVersion {
		fmt.Printf("      deposit: nonce=%d (present=%v)  receipt_version=%d (present=%v)\n",
			receipt.DepositNonce, receipt.HasDepositNonce,
			receipt.DepositReceiptVersion, receipt.HasDepositReceiptVersion)
	}
}

// printCallFrame recursively prints a CallFrame tree at the given indent depth.
func printCallFrame(f *pb.CallFrame, indent int) {
	pad := fmt.Sprintf("%*s", indent, "")
	fmt.Printf("%stype=%s  from=%s  to=%s  value=%s  gas_used=%s\n",
		pad, f.Typ, hexBytes(f.From), hexBytes(f.To), hexBytes(f.Value), hexBytes(f.GasUsed))
	if f.Error != "" {
		fmt.Printf("%s  error=%s\n", pad, f.Error)
	}
	if f.RevertReason != "" {
		fmt.Printf("%s  revert_reason=%s\n", pad, f.RevertReason)
	}
	for i, log := range f.Logs {
		fmt.Printf("%s  log[%d] addr=%s  topics=%d\n", pad, i, hexBytes(log.Address), len(log.Topics))
	}
	for _, child := range f.Calls {
		printCallFrame(child, indent+2)
	}
}
