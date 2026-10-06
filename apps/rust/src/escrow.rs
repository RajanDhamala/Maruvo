use crate::maruvo::{
    CheckEscrowRequest, CheckSettlementRequest, PrepareEscrowRequest, PrepareSettlementRequest,
    PreparedEscrow, PreparedSettlement, SettlementState,
};
use anchor_lang::solana_program::{instruction::Instruction, pubkey::Pubkey, system_program};
use anchor_lang::{AccountDeserialize, InstructionData, ToAccountMetas};
use base64::{Engine, engine::general_purpose::STANDARD};
use bincode::Options;
use maruvo_escrow::{Escrow, accounts, instruction};
use serde_json::{Value, json};
use solana_hash::Hash;
use solana_message::Message;
use solana_signature::Signature;
use solana_transaction::Transaction;
use std::{str::FromStr, time::Duration};
use tonic::Status;

pub struct EscrowClient {
    http: reqwest::Client,
    url: String,
    pub program: Pubkey,
    pub reviewer: Pubkey,
    pub network: String,
}

impl EscrowClient {
    pub fn from_env() -> Result<Option<Self>, Box<dyn std::error::Error>> {
        let Ok(url) = std::env::var("SOLANA_RPC_URL") else {
            return Ok(None);
        };
        let network = match url.trim_end_matches('/') {
            "https://api.devnet.solana.com" => "devnet",
            "http://127.0.0.1:8899" | "http://localhost:8899" => "localnet",
            _ => {
                return Err(
                    "SOLANA_RPC_URL must be the public Devnet endpoint or localhost:8899".into(),
                );
            }
        }
        .to_string();
        let program = Pubkey::from_str(&std::env::var("SOLANA_PROGRAM_ID")?)?;
        if program != maruvo_escrow::ID {
            return Err("SOLANA_PROGRAM_ID must match the compiled escrow program".into());
        }
        Ok(Some(Self {
            http: reqwest::Client::builder()
                .timeout(Duration::from_secs(5))
                .build()?,
            url,
            program,
            reviewer: Pubkey::from_str(&std::env::var("SOLANA_REVIEWER")?)?,
            network,
        }))
    }

    pub async fn rpc(&self, method: &str, params: Value) -> Result<Value, Status> {
        let response: Value = self
            .http
            .post(&self.url)
            .json(&json!({"jsonrpc":"2.0","id":1,"method":method,"params":params}))
            .send()
            .await
            .map_err(|e| {
                if self.network == "localnet" {
                    Status::unavailable("Local Solana validator unavailable; run make local in the project folder, then retry funding")
                } else {
                    Status::unavailable(format!("Solana RPC unavailable: {e}"))
                }
            })?
            .error_for_status()
            .map_err(|e| Status::unavailable(format!("Solana RPC HTTP error: {e}")))?
            .json()
            .await
            .map_err(|_| Status::unavailable("invalid Solana RPC response"))?;
        if let Some(error) = response.get("error") {
            return Err(Status::failed_precondition(format!(
                "Solana rejected {method}: {}",
                error["message"]
            )));
        }
        response
            .get("result")
            .cloned()
            .ok_or_else(|| Status::unavailable("missing Solana RPC result"))
    }

    async fn verify_network(&self) -> Result<(), Status> {
        let genesis = self.rpc("getGenesisHash", json!([])).await?;
        if self.network == "devnet"
            && genesis.as_str() != Some("EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG")
        {
            return Err(Status::failed_precondition("RPC is not Solana Devnet"));
        }
        let program = self
            .rpc(
                "getAccountInfo",
                json!([self.program.to_string(), {"encoding":"base64", "commitment":"confirmed"}]),
            )
            .await?;
        if program["value"]["executable"] != true {
            return Err(Status::failed_precondition(
                "escrow program is not deployed on this network",
            ));
        }
        Ok(())
    }

    pub async fn prepare(&self, request: PrepareEscrowRequest) -> Result<PreparedEscrow, Status> {
        let poster = key(&request.poster)?;
        let worker = key(&request.worker)?;
        let agreement_hash: [u8; 32] = request
            .agreement_hash
            .try_into()
            .map_err(|_| Status::invalid_argument("invalid agreement hash"))?;
        if request.post_id == 0
            || request.lamports > i64::MAX as u64
            || poster == worker
            || worker == Pubkey::default()
        {
            return Err(Status::invalid_argument("invalid escrow agreement"));
        }
        self.verify_network().await?;
        let (address, _) = Pubkey::find_program_address(
            &[b"escrow", poster.as_ref(), &request.post_id.to_le_bytes()],
            &self.program,
        );
        let instruction = Instruction {
            program_id: self.program,
            accounts: accounts::Fund {
                poster,
                escrow: address,
                system_program: system_program::ID,
            }
            .to_account_metas(None),
            data: instruction::Fund {
                post_id: request.post_id,
                amount: request.lamports,
                worker,
                reviewer: self.reviewer,
                agreement_hash,
            }
            .data(),
        };
        let block = self
            .rpc("getLatestBlockhash", json!([{"commitment":"confirmed"}]))
            .await?;
        let hash = Hash::from_str(
            block["value"]["blockhash"]
                .as_str()
                .ok_or_else(|| Status::unavailable("missing blockhash"))?,
        )
        .map_err(|_| Status::unavailable("invalid blockhash"))?;
        let mut transaction =
            Transaction::new_unsigned(Message::new(&[instruction], Some(&poster)));
        transaction.message.recent_blockhash = hash;
        let fee = self.rpc("getFeeForMessage", json!([STANDARD.encode(transaction.message.serialize()), {"commitment":"confirmed"}])).await?;
        let rent = self
            .rpc(
                "getMinimumBalanceForRentExemption",
                json!([Escrow::SPACE, {"commitment":"confirmed"}]),
            )
            .await?;
        Ok(PreparedEscrow {
            transaction: STANDARD.encode(
                bincode::serialize(&transaction)
                    .map_err(|_| Status::internal("cannot encode funding transaction"))?,
            ),
            address: address.to_string(),
            program_id: self.program.to_string(),
            reviewer: self.reviewer.to_string(),
            network: self.network.clone(),
            last_valid_block_height: block["value"]["lastValidBlockHeight"]
                .as_u64()
                .ok_or_else(|| Status::unavailable("missing block height"))?,
            fee_lamports: fee["value"]
                .as_u64()
                .ok_or_else(|| Status::unavailable("funding blockhash expired"))?,
            storage_lamports: rent
                .as_u64()
                .ok_or_else(|| Status::unavailable("missing account storage cost"))?,
        })
    }

    pub async fn submit(&self, unsigned: &str, signed: &str) -> Result<String, Status> {
        let expected = decode_transaction(unsigned)?;
        let transaction = decode_transaction(signed)?;
        if expected.message != transaction.message
            || transaction.signatures.len() != 1
            || transaction.message.instructions.len() != 1
        {
            return Err(Status::invalid_argument(
                "signed transaction differs from the prepared transaction",
            ));
        }
        transaction
            .verify()
            .map_err(|_| Status::invalid_argument("invalid transaction signature"))?;
        let instruction = &transaction.message.instructions[0];
        if transaction
            .message
            .account_keys
            .get(instruction.program_id_index as usize)
            != Some(&self.program)
        {
            return Err(Status::invalid_argument("wrong escrow program"));
        }
        self.verify_network().await?;
        let signature = transaction.signatures[0].to_string();
        let statuses = self
            .rpc(
                "getSignatureStatuses",
                json!([[signature], {"searchTransactionHistory":true}]),
            )
            .await?;
        if signature_status(&statuses)?.is_some() {
            return Ok(signature);
        }
        let signature = self.rpc("sendTransaction", json!([signed, {"encoding":"base64", "skipPreflight":false, "preflightCommitment":"confirmed", "maxRetries":3}])).await?;
        if signature.as_str() != Some(&transaction.signatures[0].to_string()) {
            return Err(Status::unavailable("unexpected transaction signature"));
        }
        Ok(transaction.signatures[0].to_string())
    }

    pub async fn check(&self, request: CheckEscrowRequest) -> Result<String, Status> {
        self.check_state(request, false).await
    }

    pub async fn check_recovery(&self, request: CheckEscrowRequest) -> Result<String, Status> {
        self.verify_network().await?;
        self.check_state(request, true).await
    }

    async fn check_state(
        &self,
        request: CheckEscrowRequest,
        recovery: bool,
    ) -> Result<String, Status> {
        if request.program_id != self.program.to_string()
            || request.reviewer != self.reviewer.to_string()
            || request.network != self.network
        {
            return Err(Status::failed_precondition(
                "escrow network or reviewer configuration changed",
            ));
        }
        let agreement = request
            .agreement
            .ok_or_else(|| Status::invalid_argument("missing agreement"))?;
        let poster = key(&agreement.poster)?;
        let worker = key(&agreement.worker)?;
        let (address, _) = Pubkey::find_program_address(
            &[b"escrow", poster.as_ref(), &agreement.post_id.to_le_bytes()],
            &self.program,
        );
        if address.to_string() != request.address {
            return Err(Status::invalid_argument("wrong escrow address"));
        }
        let mut config = json!({"encoding":"base64", "commitment":"confirmed"});
        let mut expired = false;
        let mut minimum_slot = 0;
        if recovery {
            let epoch = self
                .rpc("getEpochInfo", json!([{"commitment":"finalized"}]))
                .await?;
            let height = epoch["blockHeight"]
                .as_u64()
                .ok_or_else(|| Status::unavailable("missing finalized block height"))?;
            let slot = epoch["absoluteSlot"]
                .as_u64()
                .ok_or_else(|| Status::unavailable("missing finalized slot"))?;
            expired = height > request.last_valid_block_height;
            minimum_slot = slot;
            config = json!({"encoding":"base64", "commitment":"finalized", "minContextSlot":slot});
        }
        let account = self
            .rpc("getAccountInfo", json!([request.address, config]))
            .await?;
        if account.get("value").is_none() {
            return Err(Status::unavailable("missing escrow account snapshot"));
        }
        if recovery {
            let slot = account["context"]["slot"]
                .as_u64()
                .ok_or_else(|| Status::unavailable("missing escrow snapshot slot"))?;
            if slot < minimum_slot || account.get("value").is_none() {
                return Err(Status::unavailable("invalid or stale escrow snapshot"));
            }
        }
        if !account["value"].is_null() {
            let value = &account["value"];
            let data = STANDARD
                .decode(value["data"][0].as_str().unwrap_or(""))
                .map_err(|_| Status::failed_precondition("invalid escrow account"))?;
            let escrow = Escrow::try_deserialize(&mut data.as_slice())
                .map_err(|_| Status::failed_precondition("invalid escrow data"))?;
            if value["owner"].as_str() != Some(&self.program.to_string())
                || escrow.post_id != agreement.post_id
                || escrow.poster != poster
                || escrow.worker != worker
                || escrow.reviewer != self.reviewer
                || escrow.amount != agreement.lamports
                || escrow.agreement_hash.as_slice() != agreement.agreement_hash
            {
                return Err(Status::failed_precondition(
                    "on-chain escrow does not match this agreement",
                ));
            }
            let rent = self
                .rpc(
                    "getMinimumBalanceForRentExemption",
                    json!([Escrow::SPACE, {"commitment":"confirmed"}]),
                )
                .await?
                .as_u64()
                .ok_or_else(|| Status::unavailable("missing storage minimum"))?;
            match escrow.state {
                0 if value["lamports"].as_u64().unwrap_or(0)
                    >= escrow.amount.saturating_add(rent) =>
                {
                    return Ok("confirmed".into());
                }
                1 => return Ok("released".into()),
                2 => return Ok("refunded".into()),
                _ => {
                    return Err(Status::failed_precondition(
                        "invalid escrow balance or state",
                    ));
                }
            }
        }
        if recovery {
            return Ok(if expired { "expired" } else { "prepared" }.into());
        }
        if !request.signature.is_empty() {
            Signature::from_str(&request.signature)
                .map_err(|_| Status::invalid_argument("invalid transaction signature"))?;
            let status = self
                .rpc(
                    "getSignatureStatuses",
                    json!([[request.signature], {"searchTransactionHistory":true}]),
                )
                .await?;
            if let Some(status) = signature_status(&status)? {
                let confirmed = matches!(
                    status["confirmationStatus"].as_str(),
                    Some("confirmed" | "finalized")
                );
                return Ok(if confirmed && !status["err"].is_null() {
                    "failed"
                } else {
                    "pending"
                }
                .into());
            }
        }
        let height = self
            .rpc("getBlockHeight", json!([{"commitment":"confirmed"}]))
            .await?;
        if height
            .as_u64()
            .ok_or_else(|| Status::unavailable("missing block height"))?
            > request.last_valid_block_height
        {
            return Ok("expired".into());
        }
        Ok(if request.signature.is_empty() {
            "prepared"
        } else {
            "pending"
        }
        .into())
    }

    pub async fn prepare_settlement(
        &self,
        request: PrepareSettlementRequest,
    ) -> Result<PreparedSettlement, Status> {
        let escrow = request
            .escrow
            .ok_or_else(|| Status::invalid_argument("missing escrow"))?;
        if self.check(escrow.clone()).await? != "confirmed" {
            return Err(Status::failed_precondition(
                "escrow must be funded and unsettled",
            ));
        }
        self.verify_network().await?;
        let agreement = escrow
            .agreement
            .ok_or_else(|| Status::invalid_argument("missing agreement"))?;
        let data = match request.action.as_str() {
            "release" => instruction::Release {}.data(),
            "refund" => instruction::Refund {}.data(),
            _ => return Err(Status::invalid_argument("invalid settlement action")),
        };
        let instruction = Instruction {
            program_id: self.program,
            accounts: accounts::Settle {
                reviewer: self.reviewer,
                escrow: key(&escrow.address)?,
                poster: key(&agreement.poster)?,
                worker: key(&agreement.worker)?,
            }
            .to_account_metas(None),
            data,
        };
        let block = self
            .rpc("getLatestBlockhash", json!([{"commitment":"confirmed"}]))
            .await?;
        let hash = Hash::from_str(
            block["value"]["blockhash"]
                .as_str()
                .ok_or_else(|| Status::unavailable("missing blockhash"))?,
        )
        .map_err(|_| Status::unavailable("invalid blockhash"))?;
        let mut transaction =
            Transaction::new_unsigned(Message::new(&[instruction], Some(&self.reviewer)));
        transaction.message.recent_blockhash = hash;
        let fee = self.rpc("getFeeForMessage", json!([STANDARD.encode(transaction.message.serialize()), {"commitment":"confirmed"}])).await?;
        Ok(PreparedSettlement {
            transaction: STANDARD.encode(
                bincode::serialize(&transaction)
                    .map_err(|_| Status::internal("cannot encode settlement"))?,
            ),
            last_valid_block_height: block["value"]["lastValidBlockHeight"]
                .as_u64()
                .ok_or_else(|| Status::unavailable("missing block height"))?,
            fee_lamports: fee["value"]
                .as_u64()
                .ok_or_else(|| Status::unavailable("settlement blockhash expired"))?,
        })
    }

    pub async fn check_settlement(
        &self,
        request: CheckSettlementRequest,
    ) -> Result<SettlementState, Status> {
        let expected = match request.action.as_str() {
            "release" => "released",
            "refund" => "refunded",
            _ => return Err(Status::invalid_argument("invalid settlement action")),
        };
        let escrow = request
            .escrow
            .ok_or_else(|| Status::invalid_argument("missing escrow"))?;
        let escrow_state = self.check(escrow).await?;
        if escrow_state == "released" || escrow_state == "refunded" {
            return Ok(SettlementState {
                state: if escrow_state == expected {
                    "confirmed"
                } else {
                    "failed"
                }
                .into(),
                escrow_state,
            });
        }
        if escrow_state != "confirmed" {
            return Err(Status::failed_precondition("funded escrow is missing"));
        }
        if !request.signature.is_empty() {
            Signature::from_str(&request.signature)
                .map_err(|_| Status::invalid_argument("invalid settlement signature"))?;
            let statuses = self
                .rpc(
                    "getSignatureStatuses",
                    json!([[request.signature], {"searchTransactionHistory":true}]),
                )
                .await?;
            if let Some(status) = signature_status(&statuses)? {
                let confirmed = matches!(
                    status["confirmationStatus"].as_str(),
                    Some("confirmed" | "finalized")
                );
                let state = if confirmed && !status["err"].is_null() {
                    "failed"
                } else {
                    "pending"
                };
                return Ok(SettlementState {
                    state: state.into(),
                    escrow_state,
                });
            }
        }
        let height = self
            .rpc("getBlockHeight", json!([{"commitment":"confirmed"}]))
            .await?;
        let state = if height
            .as_u64()
            .ok_or_else(|| Status::unavailable("missing block height"))?
            > request.last_valid_block_height
        {
            "expired"
        } else if request.signature.is_empty() {
            "prepared"
        } else {
            "pending"
        };
        Ok(SettlementState {
            state: state.into(),
            escrow_state,
        })
    }
}

fn signature_status(response: &Value) -> Result<Option<&Value>, Status> {
    let statuses = response["value"]
        .as_array()
        .filter(|values| values.len() == 1)
        .ok_or_else(|| Status::unavailable("invalid transaction status response"))?;
    let status = &statuses[0];
    if status.is_null() {
        return Ok(None);
    }
    if status.get("err").is_none()
        || !matches!(
            status["confirmationStatus"].as_str(),
            Some("processed" | "confirmed" | "finalized")
        )
    {
        return Err(Status::unavailable("invalid transaction status"));
    }
    Ok(Some(status))
}

fn key(value: &str) -> Result<Pubkey, Status> {
    Pubkey::from_str(value).map_err(|_| Status::invalid_argument("invalid wallet address"))
}

fn decode_transaction(value: &str) -> Result<Transaction, Status> {
    let data = STANDARD
        .decode(value)
        .map_err(|_| Status::invalid_argument("invalid transaction encoding"))?;
    if data.len() > 1232 {
        return Err(Status::invalid_argument("transaction too large"));
    }
    bincode::DefaultOptions::new()
        .with_fixint_encoding()
        .with_limit(1232)
        .reject_trailing_bytes()
        .deserialize(&data)
        .map_err(|_| Status::invalid_argument("invalid transaction"))
}

#[cfg(test)]
#[path = "escrow_tests.rs"]
mod tests;
