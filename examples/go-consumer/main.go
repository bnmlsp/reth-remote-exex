package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	pb "exex_consumer/proto/gen"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// maxRecvMsgSize is the per-message receive limit for the gRPC client.
// Mainnet blocks rarely exceed 20 MB; 64 MB provides comfortable headroom.
const maxRecvMsgSize = 64 * 1024 * 1024

func main() {
	addr := "[::1]:10000"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxRecvMsgSize)))
	if err != nil {
		log.Fatalf("connect to %s failed: %v", addr, err)
	}
	defer conn.Close()

	client := pb.NewRemoteExExClient(conn)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	stream, err := client.Subscribe(ctx, &pb.SubscribeRequest{})
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
		fmt.Printf("  block #%d  txs=%d  receipts=%d  withdrawals=%d\n",
			hdr.Number, len(bwr.Txs), len(bwr.Receipts), len(bwr.Withdrawals))
	}

	sd := chain.StateDiff
	if sd == nil {
		fmt.Println("  state_diff: (nil)")
		return
	}

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
	fmt.Println()
}
