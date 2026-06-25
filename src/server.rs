//! gRPC server and ExEx loop for streaming chain notifications to subscribers.

use crate::{
    convert::{notification_to_proto, SubscribeFlags},
    proto::{
        remote_ex_ex_server::{RemoteExEx, RemoteExExServer},
        Notification, SubscribeRequest,
    },
};
use futures_util::StreamExt;
use reth_exex::{ExExContext, ExExEvent, ExExNotification};
use reth_node_api::{FullNodeComponents, NodeTypes};
use reth_provider::{CanonStateNotification, CanonStateSubscriptions};
use reth_tracing::tracing::{info, trace, warn};
use std::sync::Arc;
use tokio::sync::broadcast::error::RecvError;
use tokio::sync::{broadcast, mpsc};
use tokio_stream::wrappers::ReceiverStream;
use tonic::{transport::Server, Request, Response, Status};

#[cfg(feature = "eth")]
use reth_ethereum_primitives::EthPrimitives;

#[cfg(feature = "base")]
use base_common_consensus::BasePrimitives;

#[cfg(feature = "eth")]
type Primitives = EthPrimitives;
#[cfg(feature = "base")]
type Primitives = BasePrimitives;

/// Capacity of the broadcast channel from the ExEx loop to all gRPC subscribers.
pub const BROADCAST_CHANNEL_CAPACITY: usize = 16;

/// Per-client mpsc buffer. Each Subscribe call gets its own channel of this capacity.
pub const PER_CLIENT_CHANNEL_CAPACITY: usize = 16;

/// gRPC service that fans out ExEx notifications to connected subscribers.
pub struct ExExService {
    /// Broadcast sender shared with the ExEx loop.
    pub notifications: Arc<broadcast::Sender<Arc<ExExNotification<Primitives>>>>,
}

#[tonic::async_trait]
impl RemoteExEx for ExExService {
    type SubscribeStream = ReceiverStream<Result<Notification, Status>>;

    async fn subscribe(
        &self,
        request: Request<SubscribeRequest>,
    ) -> Result<Response<Self::SubscribeStream>, Status> {
        let req = request.into_inner();
        let flags = SubscribeFlags {
            include_headers: req.include_headers,
            include_transactions: req.include_transactions,
            include_receipts: req.include_receipts,
            include_withdrawals: req.include_withdrawals,
            include_state_diff: req.include_state_diff,
            include_call_traces: req.include_call_traces,
        };

        let (tx, rx) = mpsc::channel(PER_CLIENT_CHANNEL_CAPACITY);
        let mut notifications = self.notifications.subscribe();

        tokio::spawn(async move {
            loop {
                match notifications.recv().await {
                    Ok(notification) => {
                        let proto_notif = notification_to_proto(&notification, &flags);
                        if tx.send(Ok(proto_notif)).await.is_err() {
                            info!("gRPC client disconnected");
                            break;
                        }
                        trace!("notification sent to gRPC client");
                    }
                    Err(RecvError::Lagged(n)) => {
                        warn!(dropped = n, "gRPC client lagged");
                    }
                    Err(RecvError::Closed) => break,
                }
            }
        });

        Ok(Response::new(ReceiverStream::new(rx)))
    }
}

/// ExEx loop: receives chain notifications from reth and forwards them to the broadcast channel.
pub async fn remote_exex<Node: FullNodeComponents<Types: NodeTypes<Primitives = Primitives>>>(
    ctx: ExExContext<Node>,
    notifications: Arc<broadcast::Sender<Arc<ExExNotification<Primitives>>>>,
) -> eyre::Result<()> {
    info!("ExEx started, subscribing to canonical state stream");
    let mut stream = ctx.provider().canonical_state_stream();
    while let Some(canon_notification) = stream.next().await {
        if let CanonStateNotification::Commit { new } = &canon_notification {
            ctx.events.send(ExExEvent::FinishedHeight(new.tip().num_hash()))?;
        }
        if notifications.send(Arc::new(ExExNotification::from(canon_notification))).is_err() {
            trace!("no active gRPC subscribers, notification dropped");
        }
    }
    info!("ExEx canonical state stream ended");
    Ok(())
}

/// Starts the gRPC server on `0.0.0.0:10000`.
pub async fn start_grpc_server(
    notifications: Arc<broadcast::Sender<Arc<ExExNotification<Primitives>>>>,
) -> eyre::Result<()> {
    let server = Server::builder()
        .add_service(RemoteExExServer::new(ExExService { notifications }))
        .serve("0.0.0.0:10000".parse().expect("valid socket addr"));

    info!(addr = "0.0.0.0:10000", "gRPC server starting");
    server.await.map_err(|e| eyre::eyre!("gRPC server failed: {e}"))
}
