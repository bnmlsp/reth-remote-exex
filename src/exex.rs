#![cfg_attr(feature = "base", allow(unused_imports, dead_code, unused_variables))]

use reth_remote_exex::server::{
    remote_exex, start_grpc_server, BROADCAST_CHANNEL_CAPACITY,
};
use reth_exex::ExExNotification;
use std::sync::Arc;
use tokio::sync::broadcast;

#[cfg(feature = "eth")]
use reth_node_ethereum::EthereumNode;
#[cfg(feature = "eth")]
use reth_ethereum_primitives::EthPrimitives;

#[cfg(feature = "base")]
use base_common_consensus::BasePrimitives;

#[cfg(feature = "eth")]
type Primitives = EthPrimitives;
#[cfg(feature = "base")]
type Primitives = BasePrimitives;

#[cfg(feature = "eth")]
fn main() -> eyre::Result<()> {
    reth_ethereum_cli::Cli::parse_args().run(async move |builder, _| {
        let notifications = Arc::new(
            broadcast::channel::<Arc<ExExNotification<Primitives>>>(BROADCAST_CHANNEL_CAPACITY).0,
        );

        let notif_grpc = notifications.clone();

        let handle = builder
            .node(EthereumNode::default())
            .install_exex("exex-for-go", async move |ctx| {
                Ok(remote_exex(ctx, notifications))
            })
            .launch()
            .await?;

        handle.node.task_executor.spawn_critical_task("gRPC server", async move {
            start_grpc_server(notif_grpc).await.unwrap_or_else(|e| panic!("gRPC server failed: {e}"))
        });

        handle.wait_for_node_exit().await
    })
}

#[cfg(feature = "base")]
fn main() -> eyre::Result<()> {
    eprintln!("error: the 'base' binary is not standalone — Base ExEx is integrated into bnmlsp/base");
    std::process::exit(1);
}
