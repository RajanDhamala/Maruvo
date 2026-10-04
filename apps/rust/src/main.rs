use tonic::{Request, Response, Status, transport::Server};
mod escrow;

pub mod maruvo {
    tonic::include_proto!("maruvo");
}

use maruvo::{
    CheckEscrowRequest, CheckSettlementRequest, DemoRequest, DemoResponse, EscrowState,
    HealthRequest, HealthResponse, PrepareEscrowRequest, PrepareSettlementRequest, PreparedEscrow,
    PreparedSettlement, SettlementState, SubmitEscrowRequest, SubmittedEscrow,
    solana_service_server::{SolanaService, SolanaServiceServer},
};

struct SolanaServer {
    escrow: Option<escrow::EscrowClient>,
}

impl SolanaServer {
    fn escrow(&self) -> Result<&escrow::EscrowClient, Status> {
        self.escrow
            .as_ref()
            .ok_or_else(|| Status::failed_precondition("Solana escrow is not configured"))
    }
}

#[tonic::async_trait]
impl SolanaService for SolanaServer {
    async fn prepare_settlement(
        &self,
        request: Request<PrepareSettlementRequest>,
    ) -> Result<Response<PreparedSettlement>, Status> {
        Ok(Response::new(
            self.escrow()?
                .prepare_settlement(request.into_inner())
                .await?,
        ))
    }

    async fn check_settlement(
        &self,
        request: Request<CheckSettlementRequest>,
    ) -> Result<Response<SettlementState>, Status> {
        Ok(Response::new(
            self.escrow()?
                .check_settlement(request.into_inner())
                .await?,
        ))
    }
    async fn prepare_escrow(
        &self,
        request: Request<PrepareEscrowRequest>,
    ) -> Result<Response<PreparedEscrow>, Status> {
        Ok(Response::new(
            self.escrow()?.prepare(request.into_inner()).await?,
        ))
    }

    async fn submit_escrow(
        &self,
        request: Request<SubmitEscrowRequest>,
    ) -> Result<Response<SubmittedEscrow>, Status> {
        let input = request.into_inner();
        let signature = self
            .escrow()?
            .submit(&input.transaction, &input.signed_transaction)
            .await?;
        Ok(Response::new(SubmittedEscrow { signature }))
    }

    async fn check_escrow(
        &self,
        request: Request<CheckEscrowRequest>,
    ) -> Result<Response<EscrowState>, Status> {
        Ok(Response::new(EscrowState {
            state: self.escrow()?.check(request.into_inner()).await?,
        }))
    }

    async fn check_escrow_recovery(
        &self,
        request: Request<CheckEscrowRequest>,
    ) -> Result<Response<EscrowState>, Status> {
        Ok(Response::new(EscrowState {
            state: self.escrow()?.check_recovery(request.into_inner()).await?,
        }))
    }

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
    let _ = dotenvy::from_path(
        std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("../../.solana/env"),
    );
    let addr = std::env::var("RUST_RPC_ADDR")
        .unwrap_or_else(|_| "127.0.0.1:50051".into())
        .parse()?;

    println!("gRPC listening on {}", addr);

    Server::builder()
        .add_service(SolanaServiceServer::new(SolanaServer {
            escrow: escrow::EscrowClient::from_env()?,
        }))
        .serve_with_shutdown(addr, async {
            tokio::signal::ctrl_c()
                .await
                .expect("failed to listen for Ctrl+C");
        })
        .await?;

    Ok(())
}
