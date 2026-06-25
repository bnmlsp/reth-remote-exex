use crate::proto;
use alloy_consensus::Transaction;

/// Per-client subscription flags. Decouples proto-generated SubscribeRequest
/// from the conversion layer so convert.rs has no dependency on proto types.
#[derive(Default)]
pub struct SubscribeFlags {
    pub include_headers: bool,
    pub include_transactions: bool,
    pub include_receipts: bool,
    pub include_withdrawals: bool,
    pub include_state_diff: bool,
    pub include_call_traces: bool,
}
use alloy_primitives::{Address, U256, B256};
use alloy_rpc_types_trace::geth::{CallFrame, CallLogFrame};
use reth_execution_types::Chain;
use reth_exex::ExExNotification;
use revm_database::states::{AccountRevert, AccountStatus, BundleAccount, RevertToSlot};
use revm_database::states::reverts::AccountInfoRevert;
use revm_state::AccountInfo;

#[cfg(feature = "eth")]
use reth_ethereum_primitives::{Receipt, TransactionSigned};

#[cfg(feature = "base")]
use base_common_consensus::{BaseTxEnvelope, BaseReceipt, BaseBlock, BasePrimitives};

#[cfg(feature = "eth")]
type Primitives = reth_ethereum_primitives::EthPrimitives;
#[cfg(feature = "eth")]
type BlockType = reth_ethereum_primitives::Block;

#[cfg(feature = "base")]
type Primitives = BasePrimitives;
#[cfg(feature = "base")]
type BlockType = BaseBlock;

#[cfg(feature = "base")]
const DEPOSIT_TX_TYPE: u32 = 126;

// ── helpers ──────────────────────────────────────────────────────────────────

fn u256_to_bytes(v: U256) -> Vec<u8> {
    v.to_be_bytes::<32>().to_vec()
}

fn b256_to_bytes(v: &B256) -> Vec<u8> {
    v.as_slice().to_vec()
}

fn addr_to_bytes(v: Address) -> Vec<u8> {
    v.as_slice().to_vec()
}

fn u128_to_bytes(v: u128) -> Vec<u8> {
    v.to_be_bytes().to_vec()
}

// ── notification ─────────────────────────────────────────────────────────────

/// Converts an [`ExExNotification`] to its protobuf representation for gRPC streaming.
pub fn notification_to_proto(notif: &ExExNotification<Primitives>, flags: &SubscribeFlags) -> proto::Notification {
    use reth_exex::ExExNotification::*;
    let event = match notif {
        ChainCommitted { new } => proto::notification::Event::ChainCommitted(proto::ChainCommitted {
            new: Some(chain_to_proto(new, flags)),
        }),
        ChainReorged { old, new } => proto::notification::Event::ChainReorged(proto::ChainReorged {
            old: Some(chain_to_proto(old, flags)),
            new: Some(chain_to_proto(new, flags)),
        }),
        ChainReverted { old } => proto::notification::Event::ChainReverted(proto::ChainReverted {
            old: Some(chain_to_proto(old, flags)),
        }),
    };
    proto::Notification { event: Some(event) }
}

// ── chain ────────────────────────────────────────────────────────────────────

pub(crate) fn chain_to_proto(chain: &Chain<Primitives>, flags: &SubscribeFlags) -> proto::Chain {
    let blocks = chain
        .blocks_and_receipts()
        .map(|(block, receipts)| block_with_receipts_to_proto(block, receipts, flags))
        .collect();

    let state_diff = if flags.include_state_diff { Some(state_diff_to_proto(chain)) } else { None };
    let call_traces = if flags.include_call_traces { call_traces_to_proto(chain) } else { vec![] };

    proto::Chain { blocks, state_diff, call_traces }
}

// ── block + receipts ─────────────────────────────────────────────────────────

pub(crate) fn block_with_receipts_to_proto(
    block: &reth_primitives_traits::RecoveredBlock<BlockType>,
    receipts: &[<Primitives as reth_primitives_traits::NodePrimitives>::Receipt],
    flags: &SubscribeFlags,
) -> proto::BlockWithReceipts {
    let header = if flags.include_headers { Some(header_to_proto(block.header())) } else { None };
    let (txs, senders) = if flags.include_transactions {
        let txs = block.body().transactions.iter().map(tx_to_proto).collect();
        let senders = block.senders().iter().map(|a| addr_to_bytes(*a)).collect();
        (txs, senders)
    } else {
        (vec![], vec![])
    };
    let receipts: Vec<proto::Receipt> = if flags.include_receipts {
        receipts.iter().map(receipt_to_proto).collect()
    } else {
        vec![]
    };
    let withdrawals: Vec<proto::Withdrawal> = if flags.include_withdrawals {
        block
            .body()
            .withdrawals
            .as_deref()
            .map(|ws| ws.iter().map(withdrawal_to_proto).collect())
            .unwrap_or_default()
    } else {
        vec![]
    };

    proto::BlockWithReceipts { header, txs, senders, receipts, withdrawals }
}

// has_xxx / xxx field pairs encode proto3's lack of native optional scalars for post-merge fields.
fn header_to_proto(h: &alloy_consensus::Header) -> proto::BlockHeader {
    proto::BlockHeader {
        parent_hash: b256_to_bytes(&h.parent_hash),
        ommers_hash: b256_to_bytes(&h.ommers_hash),
        beneficiary: addr_to_bytes(h.beneficiary),
        state_root: b256_to_bytes(&h.state_root),
        transactions_root: b256_to_bytes(&h.transactions_root),
        receipts_root: b256_to_bytes(&h.receipts_root),
        logs_bloom: h.logs_bloom.as_slice().to_vec(),
        difficulty: u256_to_bytes(h.difficulty),
        number: h.number,
        gas_limit: h.gas_limit,
        gas_used: h.gas_used,
        timestamp: h.timestamp,
        extra_data: h.extra_data.to_vec(),
        mix_hash: b256_to_bytes(&h.mix_hash),
        nonce: h.nonce.as_slice().to_vec(),
        has_base_fee_per_gas: h.base_fee_per_gas.is_some(),
        base_fee_per_gas: h.base_fee_per_gas.unwrap_or(0),
        has_withdrawals_root: h.withdrawals_root.is_some(),
        withdrawals_root: h.withdrawals_root.as_ref().map(b256_to_bytes).unwrap_or_default(),
        has_blob_gas_used: h.blob_gas_used.is_some(),
        blob_gas_used: h.blob_gas_used.unwrap_or(0),
        has_excess_blob_gas: h.excess_blob_gas.is_some(),
        excess_blob_gas: h.excess_blob_gas.unwrap_or(0),
        has_parent_beacon_block_root: h.parent_beacon_block_root.is_some(),
        parent_beacon_block_root: h.parent_beacon_block_root.as_ref().map(b256_to_bytes).unwrap_or_default(),
        has_requests_hash: h.requests_hash.is_some(),
        requests_hash: h.requests_hash.as_ref().map(b256_to_bytes).unwrap_or_default(),
    }
}

fn withdrawal_to_proto(w: &alloy_eips::eip4895::Withdrawal) -> proto::Withdrawal {
    proto::Withdrawal {
        index: w.index,
        validator_index: w.validator_index,
        address: addr_to_bytes(w.address),
        amount: w.amount,
    }
}

// ── transaction ───────────────────────────────────────────────────────────────

#[cfg(feature = "eth")]
fn tx_to_proto(tx: &TransactionSigned) -> proto::Transaction {
    use alloy_consensus::EthereumTxEnvelope::*;
    match tx {
        Legacy(signed) => {
            let t = signed.tx();
            proto::Transaction {
                chain_id: t.chain_id.map(|c| c.to_be_bytes().to_vec()).unwrap_or_default(),
                gas_price: u128_to_bytes(t.gas_price),
                ..tx_common_fields(0, signed.hash(), signed.signature(), t.nonce, t.gas_limit, t.value, &t.input, txkind_to_bytes(&t.to))
            }
        }
        Eip2930(signed) => {
            let t = signed.tx();
            proto::Transaction {
                chain_id: t.chain_id.to_be_bytes().to_vec(),
                gas_price: u128_to_bytes(t.gas_price),
                access_list: access_list_to_proto(&t.access_list),
                ..tx_common_fields(1, signed.hash(), signed.signature(), t.nonce, t.gas_limit, t.value, &t.input, txkind_to_bytes(&t.to))
            }
        }
        Eip1559(signed) => {
            let t = signed.tx();
            proto::Transaction {
                chain_id: t.chain_id.to_be_bytes().to_vec(),
                max_fee_per_gas: u128_to_bytes(t.max_fee_per_gas),
                max_priority_fee_per_gas: u128_to_bytes(t.max_priority_fee_per_gas),
                access_list: access_list_to_proto(&t.access_list),
                ..tx_common_fields(2, signed.hash(), signed.signature(), t.nonce, t.gas_limit, t.value, &t.input, txkind_to_bytes(&t.to))
            }
        }
        Eip4844(signed) => {
            let t = signed.tx();
            proto::Transaction {
                chain_id: t.chain_id.to_be_bytes().to_vec(),
                max_fee_per_gas: u128_to_bytes(t.max_fee_per_gas),
                max_priority_fee_per_gas: u128_to_bytes(t.max_priority_fee_per_gas),
                access_list: access_list_to_proto(&t.access_list),
                blob_versioned_hashes: t.blob_versioned_hashes.iter().map(b256_to_bytes).collect(),
                max_fee_per_blob_gas: u128_to_bytes(t.max_fee_per_blob_gas),
                ..tx_common_fields(3, signed.hash(), signed.signature(), t.nonce, t.gas_limit, t.value, &t.input, t.to.as_slice().to_vec())
            }
        }
        Eip7702(signed) => {
            let t = signed.tx();
            proto::Transaction {
                chain_id: t.chain_id.to_be_bytes().to_vec(),
                max_fee_per_gas: u128_to_bytes(t.max_fee_per_gas),
                max_priority_fee_per_gas: u128_to_bytes(t.max_priority_fee_per_gas),
                access_list: access_list_to_proto(&t.access_list),
                authorization_list: t.authorization_list.iter().map(auth_to_proto).collect(),
                ..tx_common_fields(4, signed.hash(), signed.signature(), t.nonce, t.gas_limit, t.value, &t.input, t.to.as_slice().to_vec())
            }
        }
    }
}

fn tx_common_fields(
    tx_type: u32,
    hash: &B256,
    sig: &alloy_primitives::Signature,
    nonce: u64,
    gas_limit: u64,
    value: alloy_primitives::U256,
    input: &alloy_primitives::Bytes,
    to: Vec<u8>,
) -> proto::Transaction {
    proto::Transaction {
        hash: b256_to_bytes(hash),
        tx_type,
        signature: Some(sig_to_proto(sig)),
        nonce,
        gas_limit,
        value: u256_to_bytes(value),
        input: input.to_vec(),
        to,
        ..Default::default()
    }
}

fn txkind_to_bytes(kind: &alloy_primitives::TxKind) -> Vec<u8> {
    match kind {
        alloy_primitives::TxKind::Create => vec![],
        alloy_primitives::TxKind::Call(addr) => addr.as_slice().to_vec(),
    }
}

fn sig_to_proto(sig: &alloy_primitives::Signature) -> proto::Signature {
    proto::Signature {
        y_parity: sig.v(),
        r: u256_to_bytes(sig.r()),
        s: u256_to_bytes(sig.s()),
    }
}

fn access_list_to_proto(al: &alloy_eip2930::AccessList) -> Vec<proto::AccessListItem> {
    al.0.iter()
        .map(|item| proto::AccessListItem {
            address: addr_to_bytes(item.address),
            storage_keys: item.storage_keys.iter().map(b256_to_bytes).collect(),
        })
        .collect()
}

fn auth_to_proto(auth: &alloy_eip7702::SignedAuthorization) -> proto::SignedAuthorization {
    proto::SignedAuthorization {
        chain_id: u256_to_bytes(*auth.chain_id()),
        address: addr_to_bytes(auth.address),
        nonce: auth.nonce,
        y_parity: auth.y_parity() != 0,
        r: u256_to_bytes(auth.r()),
        s: u256_to_bytes(auth.s()),
    }
}

// ── receipt ───────────────────────────────────────────────────────────────────

#[cfg(feature = "eth")]
fn receipt_to_proto(r: &Receipt) -> proto::Receipt {
    proto::Receipt {
        tx_type: r.tx_type as u32,
        success: r.success,
        cumulative_gas_used: r.cumulative_gas_used,
        logs: r.logs.iter().map(log_to_proto).collect(),
        ..Default::default()
    }
}

#[cfg(feature = "base")]
fn tx_to_proto(tx: &BaseTxEnvelope) -> proto::Transaction {
    use base_common_consensus::BaseTxEnvelope::*;
    match tx {
        Legacy(signed) => {
            let t = signed.tx();
            proto::Transaction {
                chain_id: t.chain_id.map(|c| c.to_be_bytes().to_vec()).unwrap_or_default(),
                gas_price: u128_to_bytes(t.gas_price),
                ..tx_common_fields(0, signed.hash(), signed.signature(), t.nonce, t.gas_limit, t.value, &t.input, txkind_to_bytes(&t.to))
            }
        }
        Eip2930(signed) => {
            let t = signed.tx();
            proto::Transaction {
                chain_id: t.chain_id.to_be_bytes().to_vec(),
                gas_price: u128_to_bytes(t.gas_price),
                access_list: access_list_to_proto(&t.access_list),
                ..tx_common_fields(1, signed.hash(), signed.signature(), t.nonce, t.gas_limit, t.value, &t.input, txkind_to_bytes(&t.to))
            }
        }
        Eip1559(signed) => {
            let t = signed.tx();
            proto::Transaction {
                chain_id: t.chain_id.to_be_bytes().to_vec(),
                max_fee_per_gas: u128_to_bytes(t.max_fee_per_gas),
                max_priority_fee_per_gas: u128_to_bytes(t.max_priority_fee_per_gas),
                access_list: access_list_to_proto(&t.access_list),
                ..tx_common_fields(2, signed.hash(), signed.signature(), t.nonce, t.gas_limit, t.value, &t.input, txkind_to_bytes(&t.to))
            }
        }
        Eip7702(signed) => {
            let t = signed.tx();
            proto::Transaction {
                chain_id: t.chain_id.to_be_bytes().to_vec(),
                max_fee_per_gas: u128_to_bytes(t.max_fee_per_gas),
                max_priority_fee_per_gas: u128_to_bytes(t.max_priority_fee_per_gas),
                access_list: access_list_to_proto(&t.access_list),
                authorization_list: t.authorization_list.iter().map(auth_to_proto).collect(),
                ..tx_common_fields(4, signed.hash(), signed.signature(), t.nonce, t.gas_limit, t.value, &t.input, t.to.as_slice().to_vec())
            }
        }
        Deposit(sealed) => {
            let t = sealed.inner();
            proto::Transaction {
                hash: sealed.hash_ref().as_slice().to_vec(),
                tx_type: DEPOSIT_TX_TYPE,
                signature: None,
                nonce: 0,
                gas_limit: t.gas_limit,
                value: u256_to_bytes(t.value),
                input: t.input.to_vec(),
                to: txkind_to_bytes(&t.to),
                source_hash: t.source_hash.as_slice().to_vec(),
                mint: if t.mint == 0 { vec![] } else { u128_to_bytes(t.mint) },
                is_system_transaction: t.is_system_transaction,
                ..Default::default()
            }
        }
        Eip8130(_) => unimplemented!("EIP-8130 transaction serialization is not supported"),
    }
}

#[cfg(feature = "base")]
fn receipt_to_proto(r: &BaseReceipt) -> proto::Receipt {
    let inner = r.as_receipt();
    let tx_type = r.tx_type() as u8 as u32;
    let (has_deposit_nonce, deposit_nonce, has_deposit_receipt_version, deposit_receipt_version) =
        if let BaseReceipt::Deposit(deposit) = r {
            (
                deposit.deposit_nonce.is_some(),
                deposit.deposit_nonce.unwrap_or(0),
                deposit.deposit_receipt_version.is_some(),
                deposit.deposit_receipt_version.unwrap_or(0),
            )
        } else {
            (false, 0, false, 0)
        };

    proto::Receipt {
        tx_type,
        success: inner.status.coerce_status(),
        cumulative_gas_used: inner.cumulative_gas_used,
        logs: inner.logs.iter().map(log_to_proto).collect(),
        has_deposit_nonce,
        deposit_nonce,
        has_deposit_receipt_version,
        deposit_receipt_version,
    }
}

fn log_to_proto(log: &alloy_primitives::Log) -> proto::EventLog {
    proto::EventLog {
        address: addr_to_bytes(log.address),
        topics: log.topics().iter().map(b256_to_bytes).collect(),
        data: log.data.data.to_vec(),
    }
}

// ── state diff ────────────────────────────────────────────────────────────────

fn state_diff_to_proto(chain: &Chain<Primitives>) -> proto::StateDiff {
    let outcome = chain.execution_outcome();
    let bundle = &outcome.bundle;

    let accounts: Vec<proto::AccountDiff> = bundle
        .state
        .iter()
        .map(|(addr, acc)| account_diff_to_proto(addr, acc))
        .collect();

    let contracts: Vec<proto::ContractDiff> = bundle
        .contracts
        .iter()
        .map(|(code_hash, bytecode)| proto::ContractDiff {
            code_hash: b256_to_bytes(code_hash),
            bytecode: bytecode.original_byte_slice().to_vec(),
        })
        .collect();

    let reverts: Vec<proto::BlockReverts> = bundle
        .reverts
        .iter()
        .map(|block_reverts| proto::BlockReverts {
            accounts: block_reverts
                .iter()
                .map(|(addr, revert)| account_revert_to_proto(addr, revert))
                .collect(),
        })
        .collect();

    proto::StateDiff { accounts, contracts, reverts }
}

fn account_diff_to_proto(addr: &Address, acc: &BundleAccount) -> proto::AccountDiff {
    let status = account_status_to_proto(acc.status) as i32;
    let info = acc.info.as_ref().map(account_info_to_proto).unwrap_or_default();
    let original_info = acc.original_info.as_ref().map(account_info_to_proto).unwrap_or_default();

    let storage: Vec<proto::StorageSlotDiff> = acc
        .storage
        .iter()
        .map(|(key, slot)| proto::StorageSlotDiff {
            key: u256_to_bytes(*key),
            previous: u256_to_bytes(slot.previous_or_original_value),
            current: u256_to_bytes(slot.present_value),
        })
        .collect();

    proto::AccountDiff {
        address: addr_to_bytes(*addr),
        status,
        info: Some(info),
        original_info: Some(original_info),
        storage,
    }
}

fn account_info_to_proto(info: &AccountInfo) -> proto::AccountInfo {
    proto::AccountInfo {
        balance: u256_to_bytes(info.balance),
        nonce: info.nonce,
        code_hash: b256_to_bytes(&info.code_hash),
    }
}

fn account_status_to_proto(status: AccountStatus) -> proto::AccountStatus {
    match status {
        AccountStatus::LoadedNotExisting => proto::AccountStatus::LoadedNotExisting,
        AccountStatus::Loaded => proto::AccountStatus::Loaded,
        AccountStatus::LoadedEmptyEIP161 => proto::AccountStatus::LoadedEmptyEip161,
        AccountStatus::InMemoryChange => proto::AccountStatus::InMemoryChange,
        AccountStatus::Changed => proto::AccountStatus::Changed,
        AccountStatus::Destroyed => proto::AccountStatus::Destroyed,
        AccountStatus::DestroyedChanged => proto::AccountStatus::DestroyedChanged,
        AccountStatus::DestroyedAgain => proto::AccountStatus::DestroyedAgain,
    }
}

fn account_revert_to_proto(addr: &Address, revert: &AccountRevert) -> proto::AccountRevert {
    let (kind, revert_to_info) = match &revert.account {
        AccountInfoRevert::DoNothing => (proto::AccountRevertKind::DoNothing as i32, None),
        AccountInfoRevert::DeleteIt => (proto::AccountRevertKind::DeleteIt as i32, None),
        AccountInfoRevert::RevertTo(info) => (
            proto::AccountRevertKind::RevertTo as i32,
            Some(account_info_to_proto(info)),
        ),
    };

    let storage: Vec<proto::StorageRevert> = revert
        .storage
        .iter()
        .map(|(key, slot)| {
            let revert_to = match slot {
                RevertToSlot::Some(val) => {
                    proto::storage_revert::RevertTo::Value(u256_to_bytes(*val))
                }
                RevertToSlot::Destroyed => proto::storage_revert::RevertTo::Destroyed(true),
            };
            proto::StorageRevert {
                key: u256_to_bytes(*key),
                revert_to: Some(revert_to),
            }
        })
        .collect();

    proto::AccountRevert {
        address: addr_to_bytes(*addr),
        kind,
        revert_to_info,
        storage,
        previous_status: account_status_to_proto(revert.previous_status) as i32,
        wipe_storage: revert.wipe_storage,
    }
}

// ── call traces ───────────────────────────────────────────────────────────────

fn call_traces_to_proto(chain: &Chain<Primitives>) -> Vec<proto::BlockCallTraces> {
    let Some(traces) = chain.call_traces() else {
        return vec![];
    };
    chain
        .blocks_and_receipts()
        .filter_map(|(block, _)| {
            let block_number = block.header().number;
            let tx_frames = traces.get(&block_number)?;
            let protos = block
                .body()
                .transactions
                .iter()
                .zip(tx_frames.iter())
                .map(|(tx, frame)| {
                    let mut proto_frame = call_frame_to_proto(frame);
                    // revm TracingInspector sets inputs.gas_limit = tx.gas_limit - intrinsic_gas;
                    // restore to tx.gas_limit to match Geth callTracer semantics.
                    proto_frame.gas = u256_to_bytes(U256::from(tx.gas_limit()));
                    proto_frame
                })
                .collect();
            Some(proto::BlockCallTraces { block_number, txs: protos })
        })
        .collect()
}

fn call_frame_to_proto(f: &CallFrame) -> proto::CallFrame {
    proto::CallFrame {
        typ: f.typ.clone(),
        from: addr_to_bytes(f.from),
        to: f.to.map(addr_to_bytes).unwrap_or_default(),
        value: f.value.map(u256_to_bytes).unwrap_or_default(),
        gas: u256_to_bytes(f.gas),
        gas_used: u256_to_bytes(f.gas_used),
        input: f.input.to_vec(),
        output: f.output.as_ref().map(|b| b.to_vec()).unwrap_or_default(),
        error: f.error.clone().unwrap_or_default(),
        revert_reason: f.revert_reason.clone().unwrap_or_default(),
        calls: f.calls.iter().map(call_frame_to_proto).collect(),
        logs: f.logs.iter().map(call_log_frame_to_proto).collect(),
    }
}

fn call_log_frame_to_proto(l: &CallLogFrame) -> proto::CallLogFrame {
    proto::CallLogFrame {
        address: l.address.map(addr_to_bytes).unwrap_or_default(),
        topics: l.topics
            .as_deref()
            .map(|ts| ts.iter().map(b256_to_bytes).collect())
            .unwrap_or_default(),
        data: l.data.as_ref().map(|b| b.to_vec()).unwrap_or_default(),
        has_position: l.position.is_some(),
        position: l.position.unwrap_or(0),
        has_index: l.index.is_some(),
        index: l.index.unwrap_or(0),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn all_false() -> SubscribeFlags {
        SubscribeFlags {
            include_headers: false,
            include_transactions: false,
            include_receipts: false,
            include_withdrawals: false,
            include_state_diff: false,
            include_call_traces: false,
        }
    }

    fn all_true() -> SubscribeFlags {
        SubscribeFlags {
            include_headers: true,
            include_transactions: true,
            include_receipts: true,
            include_withdrawals: true,
            include_state_diff: true,
            include_call_traces: true,
        }
    }

    // Applies flags to a fully-populated proto::Chain and returns it.
    // This mirrors what chain_to_proto does for state_diff and call_traces,
    // and what block_with_receipts_to_proto does for BlockWithReceipts fields.
    fn apply_flags(flags: &SubscribeFlags) -> proto::Chain {
        let full_header = proto::BlockHeader { number: 1, ..Default::default() };
        let full_block = proto::BlockWithReceipts {
            header: Some(full_header),
            txs: vec![proto::Transaction::default()],
            senders: vec![vec![0u8; 20]],
            receipts: vec![proto::Receipt::default()],
            withdrawals: vec![proto::Withdrawal::default()],
        };
        let full_state_diff = proto::StateDiff {
            accounts: vec![proto::AccountDiff::default()],
            ..Default::default()
        };
        let full_call_traces = vec![proto::BlockCallTraces {
            block_number: 1,
            txs: vec![proto::CallFrame::default()],
        }];

        // Simulate flag filtering on a pre-built full block
        let header = if flags.include_headers { full_block.header.clone() } else { None };
        let txs = if flags.include_transactions { full_block.txs.clone() } else { vec![] };
        let senders = if flags.include_transactions { full_block.senders.clone() } else { vec![] };
        let receipts = if flags.include_receipts { full_block.receipts.clone() } else { vec![] };
        let withdrawals = if flags.include_withdrawals { full_block.withdrawals.clone() } else { vec![] };
        let state_diff = if flags.include_state_diff { Some(full_state_diff) } else { None };
        let call_traces = if flags.include_call_traces { full_call_traces } else { vec![] };

        proto::Chain {
            blocks: vec![proto::BlockWithReceipts { header, txs, senders, receipts, withdrawals }],
            state_diff,
            call_traces,
        }
    }

    #[test]
    fn test_flags_all_false_yields_empty_chain() {
        let chain = apply_flags(&all_false());
        let block = &chain.blocks[0];
        assert!(block.header.is_none());
        assert!(block.txs.is_empty());
        assert!(block.senders.is_empty());
        assert!(block.receipts.is_empty());
        assert!(block.withdrawals.is_empty());
        assert!(chain.state_diff.is_none());
        assert!(chain.call_traces.is_empty());
    }

    #[test]
    fn test_flags_all_true_yields_full_chain() {
        let chain = apply_flags(&all_true());
        let block = &chain.blocks[0];
        assert!(block.header.is_some());
        assert_eq!(block.txs.len(), 1);
        assert_eq!(block.senders.len(), 1);
        assert_eq!(block.receipts.len(), 1);
        assert_eq!(block.withdrawals.len(), 1);
        assert!(chain.state_diff.is_some());
        assert_eq!(chain.call_traces.len(), 1);
    }

    #[test]
    fn test_flag_include_headers_only() {
        let flags = SubscribeFlags { include_headers: true, ..all_false() };
        let chain = apply_flags(&flags);
        let block = &chain.blocks[0];
        assert!(block.header.is_some());
        assert!(block.txs.is_empty());
        assert!(block.receipts.is_empty());
        assert!(block.withdrawals.is_empty());
        assert!(chain.state_diff.is_none());
        assert!(chain.call_traces.is_empty());
    }

    #[test]
    fn test_flag_include_transactions_only() {
        let flags = SubscribeFlags { include_transactions: true, ..all_false() };
        let chain = apply_flags(&flags);
        let block = &chain.blocks[0];
        assert!(block.header.is_none());
        assert_eq!(block.txs.len(), 1);
        assert_eq!(block.senders.len(), 1, "senders must be bundled with txs");
        assert!(block.receipts.is_empty());
        assert!(block.withdrawals.is_empty());
    }

    #[test]
    fn test_flag_include_receipts_only() {
        let flags = SubscribeFlags { include_receipts: true, ..all_false() };
        let chain = apply_flags(&flags);
        let block = &chain.blocks[0];
        assert!(block.header.is_none());
        assert!(block.txs.is_empty());
        assert_eq!(block.receipts.len(), 1);
        assert!(block.withdrawals.is_empty());
    }

    #[test]
    fn test_flag_include_withdrawals_only() {
        let flags = SubscribeFlags { include_withdrawals: true, ..all_false() };
        let chain = apply_flags(&flags);
        let block = &chain.blocks[0];
        assert!(block.header.is_none());
        assert!(block.txs.is_empty());
        assert!(block.receipts.is_empty());
        assert_eq!(block.withdrawals.len(), 1);
    }

    #[test]
    fn test_flag_include_state_diff_only() {
        let flags = SubscribeFlags { include_state_diff: true, ..all_false() };
        let chain = apply_flags(&flags);
        let block = &chain.blocks[0];
        assert!(block.header.is_none());
        assert!(block.txs.is_empty());
        assert!(chain.state_diff.is_some());
        assert!(chain.call_traces.is_empty());
    }

    #[test]
    fn test_flag_include_call_traces_only() {
        let flags = SubscribeFlags { include_call_traces: true, ..all_false() };
        let chain = apply_flags(&flags);
        let block = &chain.blocks[0];
        assert!(block.header.is_none());
        assert!(block.txs.is_empty());
        assert!(chain.state_diff.is_none());
        assert_eq!(chain.call_traces.len(), 1);
    }

    #[test]
    fn test_senders_absent_when_transactions_false() {
        let flags = SubscribeFlags { include_transactions: false, ..all_true() };
        let chain = apply_flags(&flags);
        let block = &chain.blocks[0];
        assert!(block.txs.is_empty());
        assert!(block.senders.is_empty(), "senders must be empty when txs are excluded");
    }
}

#[cfg(all(test, feature = "base"))]
mod base_tests {
    use super::*;
    use alloy_consensus::{Receipt, Sealed};
    use alloy_primitives::{Address, Bytes, Log, B256, U256};
    use base_common_consensus::{BaseTxEnvelope, BaseReceipt, DepositReceipt, TxDeposit};

    fn make_deposit_tx() -> BaseTxEnvelope {
        let tx = TxDeposit {
            source_hash: B256::from([0xab; 32]),
            from: Address::from([0x01; 20]),
            to: alloy_primitives::TxKind::Call(Address::from([0x02; 20])),
            mint: 1_000_000_000_000_000_000u128,
            value: U256::from(500u64),
            gas_limit: 21000,
            is_system_transaction: false,
            input: Bytes::from(vec![0xde, 0xad]),
        };
        BaseTxEnvelope::Deposit(Sealed::new(tx))
    }

    fn make_deposit_tx_zero_mint() -> BaseTxEnvelope {
        let tx = TxDeposit {
            source_hash: B256::from([0xcc; 32]),
            from: Address::from([0x03; 20]),
            to: alloy_primitives::TxKind::Call(Address::from([0x04; 20])),
            mint: 0,
            value: U256::ZERO,
            gas_limit: 100000,
            is_system_transaction: true,
            input: Bytes::new(),
        };
        BaseTxEnvelope::Deposit(Sealed::new(tx))
    }

    fn make_deposit_tx_create() -> BaseTxEnvelope {
        let tx = TxDeposit {
            source_hash: B256::from([0xdd; 32]),
            from: Address::from([0x05; 20]),
            to: alloy_primitives::TxKind::Create,
            mint: 0,
            value: U256::ZERO,
            gas_limit: 500000,
            is_system_transaction: false,
            input: Bytes::from(vec![0x60, 0x80]),
        };
        BaseTxEnvelope::Deposit(Sealed::new(tx))
    }

    #[test]
    fn test_deposit_tx_to_proto() {
        let tx = make_deposit_tx();
        let proto = tx_to_proto(&tx);
        assert_eq!(proto.tx_type, 126);
        assert!(proto.signature.is_none());
        assert_eq!(proto.nonce, 0);
        assert_eq!(proto.gas_limit, 21000);
        assert_eq!(proto.source_hash, vec![0xab; 32]);
        assert!(!proto.mint.is_empty());
        assert_eq!(proto.mint, u128_to_bytes(1_000_000_000_000_000_000u128));
        assert!(!proto.is_system_transaction);
        assert_eq!(proto.input, vec![0xde, 0xad]);
        assert_eq!(proto.to, Address::from([0x02; 20]).as_slice().to_vec());
    }

    #[test]
    fn test_deposit_tx_zero_mint() {
        let tx = make_deposit_tx_zero_mint();
        let proto = tx_to_proto(&tx);
        assert_eq!(proto.tx_type, 126);
        assert!(proto.mint.is_empty());
        assert!(proto.is_system_transaction);
    }

    #[test]
    fn test_deposit_tx_create() {
        let tx = make_deposit_tx_create();
        let proto = tx_to_proto(&tx);
        assert_eq!(proto.tx_type, 126);
        assert!(proto.to.is_empty());
    }

    #[test]
    fn test_deposit_receipt_with_nonce() {
        let receipt = BaseReceipt::Deposit(DepositReceipt {
            inner: Receipt {
                status: true.into(),
                cumulative_gas_used: 42000,
                logs: vec![],
            },
            deposit_nonce: Some(42),
            deposit_receipt_version: Some(1),
        });
        let proto = receipt_to_proto(&receipt);
        assert_eq!(proto.tx_type, 126);
        assert!(proto.success);
        assert_eq!(proto.cumulative_gas_used, 42000);
        assert!(proto.has_deposit_nonce);
        assert_eq!(proto.deposit_nonce, 42);
        assert!(proto.has_deposit_receipt_version);
        assert_eq!(proto.deposit_receipt_version, 1);
    }

    #[test]
    fn test_deposit_receipt_without_nonce() {
        let receipt = BaseReceipt::Deposit(DepositReceipt {
            inner: Receipt {
                status: true.into(),
                cumulative_gas_used: 21000,
                logs: vec![],
            },
            deposit_nonce: None,
            deposit_receipt_version: None,
        });
        let proto = receipt_to_proto(&receipt);
        assert_eq!(proto.tx_type, 126);
        assert!(!proto.has_deposit_nonce);
        assert_eq!(proto.deposit_nonce, 0);
        assert!(!proto.has_deposit_receipt_version);
        assert_eq!(proto.deposit_receipt_version, 0);
    }

    #[test]
    fn test_base_receipt_legacy() {
        let receipt = BaseReceipt::Legacy(Receipt {
            status: true.into(),
            cumulative_gas_used: 100000,
            logs: vec![Log::new(Address::from([0x01; 20]), vec![], Bytes::new()).unwrap_or_default()],
        });
        let proto = receipt_to_proto(&receipt);
        assert_eq!(proto.tx_type, 0);
        assert!(proto.success);
        assert_eq!(proto.cumulative_gas_used, 100000);
        assert_eq!(proto.logs.len(), 1);
        assert!(!proto.has_deposit_nonce);
        assert!(!proto.has_deposit_receipt_version);
    }

    #[test]
    fn test_base_receipt_eip1559() {
        let receipt = BaseReceipt::Eip1559(Receipt {
            status: false.into(),
            cumulative_gas_used: 200000,
            logs: vec![],
        });
        let proto = receipt_to_proto(&receipt);
        assert_eq!(proto.tx_type, 2);
        assert!(!proto.success);
        assert_eq!(proto.cumulative_gas_used, 200000);
        assert!(!proto.has_deposit_nonce);
    }

    #[test]
    fn test_base_receipt_eip2930() {
        let receipt = BaseReceipt::Eip2930(Receipt {
            status: true.into(),
            cumulative_gas_used: 150000,
            logs: vec![],
        });
        let proto = receipt_to_proto(&receipt);
        assert_eq!(proto.tx_type, 1);
        assert!(proto.success);
        assert!(!proto.has_deposit_nonce);
    }

    #[test]
    fn test_base_receipt_eip7702() {
        let receipt = BaseReceipt::Eip7702(Receipt {
            status: true.into(),
            cumulative_gas_used: 180000,
            logs: vec![],
        });
        let proto = receipt_to_proto(&receipt);
        assert_eq!(proto.tx_type, 4);
        assert!(proto.success);
        assert!(!proto.has_deposit_nonce);
    }

    #[test]
    fn test_base_tx_legacy() {
        use alloy_consensus::{Signed, TxLegacy};
        use alloy_primitives::Signature;

        let tx = TxLegacy {
            chain_id: Some(8453),
            nonce: 7,
            gas_price: 1_000_000_000,
            gas_limit: 21000,
            to: alloy_primitives::TxKind::Call(Address::from([0xaa; 20])),
            value: U256::from(1_000_000u64),
            input: Bytes::from(vec![0x01, 0x02]),
        };
        let sig = Signature::new(U256::from(1u64), U256::from(2u64), false);
        let signed = Signed::new_unhashed(tx, sig);
        let envelope = BaseTxEnvelope::Legacy(signed);

        let proto = tx_to_proto(&envelope);
        assert_eq!(proto.tx_type, 0);
        assert!(proto.signature.is_some());
        assert_eq!(proto.nonce, 7);
        assert_eq!(proto.gas_limit, 21000);
        assert_eq!(proto.chain_id, 8453u64.to_be_bytes().to_vec());
        assert_eq!(proto.to, Address::from([0xaa; 20]).as_slice().to_vec());
        assert_eq!(proto.input, vec![0x01, 0x02]);
    }

    #[test]
    fn test_base_tx_eip1559() {
        use alloy_consensus::{Signed, TxEip1559};
        use alloy_primitives::Signature;

        let tx = TxEip1559 {
            chain_id: 8453,
            nonce: 42,
            gas_limit: 100000,
            max_fee_per_gas: 50_000_000_000,
            max_priority_fee_per_gas: 1_000_000_000,
            to: alloy_primitives::TxKind::Call(Address::from([0xbb; 20])),
            value: U256::from(500u64),
            input: Bytes::from(vec![0xab, 0xcd]),
            access_list: Default::default(),
        };
        let sig = Signature::new(U256::from(3u64), U256::from(4u64), true);
        let signed = Signed::new_unhashed(tx, sig);
        let envelope = BaseTxEnvelope::Eip1559(signed);

        let proto = tx_to_proto(&envelope);
        assert_eq!(proto.tx_type, 2);
        assert!(proto.signature.is_some());
        assert_eq!(proto.nonce, 42);
        assert_eq!(proto.gas_limit, 100000);
        assert_eq!(proto.max_fee_per_gas, u128_to_bytes(50_000_000_000));
        assert_eq!(proto.max_priority_fee_per_gas, u128_to_bytes(1_000_000_000));
        assert_eq!(proto.chain_id, 8453u64.to_be_bytes().to_vec());
        assert_eq!(proto.to, Address::from([0xbb; 20]).as_slice().to_vec());
    }
}
