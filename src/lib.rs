//! reth ExEx gRPC push server library.
//!
//! Exposes the protobuf-generated types (`proto`) and the reth→protobuf
//! conversion logic (`convert`) used by the `exex` binary.

#[cfg(all(feature = "eth", feature = "base"))]
compile_error!("features 'eth' and 'base' are mutually exclusive — enable only one");

#[cfg(not(any(feature = "eth", feature = "base")))]
compile_error!("either feature 'eth' or 'base' must be enabled");

pub mod proto {
    tonic::include_proto!("exex");
}
pub mod convert;
