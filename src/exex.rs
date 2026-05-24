use reth_remote_exex::{
    convert::{notification_to_proto, SubscribeFlags},
    proto::{
        remote_ex_ex_server::{RemoteExEx, RemoteExExServer},
        Notification, SubscribeRequest,
    },
};
use tokio::sync::broadcast::error::RecvError;
use futures_util::TryStreamExt;
use reth::{builder::NodeTypes, primitives::EthPrimitives};
use reth_exex::{ExExContext, ExExEvent, ExExNotification};
use reth_node_api::FullNodeComponents;
use reth_node_ethereum::EthereumNode;
use reth_tracing::tracing::{info, warn};
use std::sync::Arc;
use tokio::sync::{broadcast, mpsc};

use tokio_stream::wrappers::ReceiverStream;
use tonic::{transport::Server, Request, Response, Status};

/// Capacity of the broadcast channel from the ExEx loop to all gRPC subscribers.
/// When the channel is full, lagged subscribers will miss notifications.
const BROADCAST_CHANNEL_CAPACITY: usize = 16;

/// Per-client mpsc buffer. Each Subscribe call gets its own channel of this capacity.
const PER_CLIENT_CHANNEL_CAPACITY: usize = 16;

/// gRPC service that fans out ExEx notifications to connected subscribers.
struct ExExService {
    notifications: Arc<broadcast::Sender<Arc<ExExNotification>>>,
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
                        info!("Notification sent to gRPC client");
                    }
                    Err(RecvError::Lagged(n)) => {
                        warn!("gRPC client lagged, dropped {} notifications", n);
                    }
                    Err(RecvError::Closed) => break,
                }
            }
        });

        Ok(Response::new(ReceiverStream::new(rx)))
    }
}

/// ExEx loop: receives chain notifications from reth and forwards them to the broadcast channel.
async fn remote_exex<Node: FullNodeComponents<Types: NodeTypes<Primitives = EthPrimitives>>>(
    mut ctx: ExExContext<Node>,
    notifications: Arc<broadcast::Sender<Arc<ExExNotification>>>,
) -> eyre::Result<()> {
    while let Some(notification) = ctx.notifications.try_next().await? {
        if let Some(committed_chain) = notification.committed_chain() {
            ctx.events.send(ExExEvent::FinishedHeight(committed_chain.tip().num_hash()))?;
        }
        // Err means no active subscribers; dropping the notification is intentional.
        let _ = notifications.send(Arc::new(notification));
    }
    Ok(())
}

fn main() -> eyre::Result<()> {
    reth::cli::Cli::parse_args().run(async move |builder, _| {
        let notifications = Arc::new(broadcast::channel::<Arc<ExExNotification>>(BROADCAST_CHANNEL_CAPACITY).0);

        let server = Server::builder()
            .add_service(RemoteExExServer::new(ExExService {
                notifications: notifications.clone(),
            }))
            // Compile-time known valid literal; parse() cannot fail.
            .serve("0.0.0.0:10000".parse().unwrap());

        let handle = builder
            .node(EthereumNode::default())
            .install_exex("exex-for-go", async move |ctx| {
                Ok(remote_exex(ctx, notifications))
            })
            .launch()
            .await?;

        handle.node.task_executor.spawn_critical_task("gRPC server", async move {
            server.await.expect("failed to start gRPC server")
        });

        handle.wait_for_node_exit().await
    })
}
