use crate::proto;
use alloy_primitives::{Address, U256, B256};
use reth_ethereum_primitives::{Receipt, TransactionSigned};
use reth_execution_types::Chain;
use reth_exex::ExExNotification;
use revm_database::states::{AccountRevert, AccountStatus, BundleAccount, RevertToSlot};
use revm_database::states::reverts::AccountInfoRevert;
use revm_state::AccountInfo;

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
pub fn notification_to_proto(notif: &ExExNotification) -> proto::Notification {
    use reth_exex::ExExNotification::*;
    let event = match notif {
        ChainCommitted { new } => proto::notification::Event::ChainCommitted(proto::ChainCommitted {
            new: Some(chain_to_proto(new)),
        }),
        ChainReorged { old, new } => proto::notification::Event::ChainReorged(proto::ChainReorged {
            old: Some(chain_to_proto(old)),
            new: Some(chain_to_proto(new)),
        }),
        ChainReverted { old } => proto::notification::Event::ChainReverted(proto::ChainReverted {
            old: Some(chain_to_proto(old)),
        }),
    };
    proto::Notification { event: Some(event) }
}

// ── chain ────────────────────────────────────────────────────────────────────

fn chain_to_proto(chain: &Chain) -> proto::Chain {
    let blocks = chain
        .blocks_and_receipts()
        .map(|(block, receipts)| block_with_receipts_to_proto(block, receipts))
        .collect();

    let state_diff = state_diff_to_proto(chain);

    proto::Chain { blocks, state_diff: Some(state_diff) }
}

// ── block + receipts ─────────────────────────────────────────────────────────

fn block_with_receipts_to_proto(
    block: &reth_primitives_traits::RecoveredBlock<reth_ethereum_primitives::Block>,
    receipts: &[Receipt],
) -> proto::BlockWithReceipts {
    let header = header_to_proto(block.header());
    let senders: Vec<Vec<u8>> = block.senders().iter().map(|a| addr_to_bytes(*a)).collect();
    let txs: Vec<proto::Transaction> = block
        .body()
        .transactions
        .iter()
        .map(tx_to_proto)
        .collect();
    let receipts: Vec<proto::Receipt> = receipts.iter().map(receipt_to_proto).collect();
    let withdrawals = block
        .body()
        .withdrawals
        .as_deref()
        .map(|ws| ws.iter().map(withdrawal_to_proto).collect())
        .unwrap_or_default();

    proto::BlockWithReceipts { header: Some(header), txs, senders, receipts, withdrawals }
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

fn receipt_to_proto(r: &Receipt) -> proto::Receipt {
    proto::Receipt {
        tx_type: r.tx_type as u32,
        success: r.success,
        cumulative_gas_used: r.cumulative_gas_used,
        logs: r.logs.iter().map(log_to_proto).collect(),
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

fn state_diff_to_proto(chain: &Chain) -> proto::StateDiff {
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
