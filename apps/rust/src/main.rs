use tonic::{Request, Response, Status, transport::Server};

pub mod maruvo {
    tonic::include_proto!("maruvo");
}

use maruvo::{
    HealthRequest, HealthResponse,
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
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let addr = "127.0.0.1:50051".parse()?;

    println!("gRPC listening on {}", addr);

    Server::builder()
        .add_service(SolanaServiceServer::new(SolanaServer))
        .serve(addr)
        .await?;

    Ok(())
}
