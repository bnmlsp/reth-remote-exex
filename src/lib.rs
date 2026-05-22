//! reth ExEx gRPC push server library.
//!
//! Exposes the protobuf-generated types (`proto`) and the reth→protobuf
//! conversion logic (`convert`) used by the `exex` binary.

pub mod proto {
    tonic::include_proto!("exex");
}
pub mod convert;
