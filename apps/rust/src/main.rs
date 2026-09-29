use tonic::{Request, Response, Status, transport::Server};

pub mod maruvo {
    tonic::include_proto!("maruvo");
}

use maruvo::{
    DemoRequest, DemoResponse, HealthRequest, HealthResponse,
    solana_service_server::{SolanaService, SolanaServiceServer},
};

#[derive(Default)]
struct SolanaServer;

#[tonic::async_trait]
impl SolanaService for SolanaServer {
    async fn health(
        &self,
        _request: Request<HealthRequest>,
    ) -> Result<Response<HealthResponse>, Status> {
        Ok(Response::new(HealthResponse {
            message: "Maruvo Rust service running".into(),
        }))
    }

    async fn demo(&self, request: Request<DemoRequest>) -> Result<Response<DemoResponse>, Status> {
        let input = request.into_inner();
        let message = input.message.trim();
        if message.is_empty() || message.len() > 256 {
            return Err(Status::invalid_argument(
                "message must contain 1 to 256 bytes after trimming",
            ));
        }

        Ok(Response::new(DemoResponse {
            message: format!("Rust received: {message}"),
            service: "maruvo-rust".into(),
        }))
    }
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let addr = std::env::var("RUST_RPC_ADDR")
        .unwrap_or_else(|_| "127.0.0.1:50051".into())
        .parse()?;

    println!("gRPC listening on {}", addr);

    Server::builder()
        .add_service(SolanaServiceServer::new(SolanaServer))
        .serve_with_shutdown(addr, async {
            tokio::signal::ctrl_c()
                .await
                .expect("failed to listen for Ctrl+C");
        })
        .await?;

    Ok(())
}
