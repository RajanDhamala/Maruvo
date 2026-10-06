use super::*;
use anchor_lang::AccountSerialize;
use tokio::io::{AsyncReadExt, AsyncWriteExt};

#[tokio::test]
async fn signed_transaction_retries_return_the_existing_receipt() {
    let signed = concat!(
        "Af4l+Pv9QS1+qX7fnCbtLzk4LlMDYYR3m/rskO+GDZF5D+bldYFVrCboMBC23dwHc1Zc27+aJhenUgo+",
        "vncpUwcBAAIEiojj3XQJ8ZX9UtstPLpdcspnCb8dlBIb83SIAbQPb1zKk6wXBRhwcdZ7g8f/Dv6BCOjs",
        "RTBXXXcmh5Mz29q+fAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA03f+56zxbu7vuMz8RCTX",
        "HW2K1rBas54ghU+HjWs3lKAFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQEDAwABAnjavG/d",
        "mHGuBwsAAAAAAAAAQEIPAAAAAACBOXcOqH0XX1ajVGbDTH7My42KkbTuN6Jd9g9bj8mzlO1JKMYo0cLG",
        "6ukDOJBZlWEpWSc6XGP5NjbBRhSshzfRm3lpyuzdxvJQ7bj5Db8cZaNoVk/IU+boTRzd/rBswzw=",
    );
    let mut transaction = decode_transaction(signed).unwrap();
    let signature = transaction.signatures[0].to_string();
    transaction.signatures[0] = Signature::default();
    let unsigned = STANDARD.encode(bincode::serialize(&transaction).unwrap());
    for status in [
        Value::Null,
        json!({"err":null,"confirmationStatus":"processed"}),
        json!({"err":null,"confirmationStatus":"confirmed"}),
        json!({"err":{"InstructionError":[0,"Custom"]},"confirmationStatus":"finalized"}),
    ] {
        let mut replies = vec![
            ("getGenesisHash", json!("fixture")),
            ("getAccountInfo", json!({"value":{"executable":true}})),
            ("getSignatureStatuses", json!({"value":[status]})),
        ];
        if status.is_null() {
            replies.push(("sendTransaction", json!(signature)));
        }
        let (client, server) = rpc_fixture(Pubkey::new_unique(), replies).await;
        assert_eq!(client.submit(&unsigned, signed).await.unwrap(), signature);
        let calls = server.await.unwrap();
        assert_eq!(calls.len(), if status.is_null() { 4 } else { 3 });
        assert_eq!(calls[2]["params"][1]["searchTransactionHistory"], true);
    }
}

async fn fixture(
    height: u64,
    snapshot: Value,
    rent: bool,
) -> (
    EscrowClient,
    CheckEscrowRequest,
    tokio::task::JoinHandle<Vec<Value>>,
) {
    let program = maruvo_escrow::ID;
    let reviewer = Pubkey::new_unique();
    let request = request_fixture(reviewer);
    let mut replies = vec![
        ("getGenesisHash", json!("fixture")),
        ("getAccountInfo", json!({"value":{"executable":true}})),
        (
            "getEpochInfo",
            json!({"absoluteSlot":150,"blockHeight":height}),
        ),
        ("getAccountInfo", snapshot),
    ];
    if rent {
        let agreement = request.agreement.as_ref().unwrap();
        let account = Escrow {
            post_id: agreement.post_id,
            poster: key(&agreement.poster).unwrap(),
            worker: key(&agreement.worker).unwrap(),
            reviewer,
            amount: agreement.lamports,
            agreement_hash: [1; 32],
            state: 0,
            bump: 0,
        };
        let mut bytes = Vec::new();
        account.try_serialize(&mut bytes).unwrap();
        replies[3].1["value"] = json!({"owner":program.to_string(),"lamports":110,"data":[STANDARD.encode(bytes),"base64"]});
        replies.push(("getMinimumBalanceForRentExemption", json!(10)));
    }
    let (client, server) = rpc_fixture(reviewer, replies).await;
    (client, request, server)
}

fn request_fixture(reviewer: Pubkey) -> CheckEscrowRequest {
    let program = maruvo_escrow::ID;
    let poster = Pubkey::new_unique();
    let worker = Pubkey::new_unique();
    let (escrow, _) = Pubkey::find_program_address(
        &[b"escrow", poster.as_ref(), &7_u64.to_le_bytes()],
        &program,
    );
    CheckEscrowRequest {
        agreement: Some(PrepareEscrowRequest {
            post_id: 7,
            lamports: 100,
            poster: poster.to_string(),
            worker: worker.to_string(),
            agreement_hash: vec![1; 32],
        }),
        address: escrow.to_string(),
        program_id: program.to_string(),
        reviewer: reviewer.to_string(),
        network: "localnet".into(),
        signature: Signature::default().to_string(),
        last_valid_block_height: 100,
    }
}

async fn rpc_fixture(
    reviewer: Pubkey,
    replies: Vec<(&'static str, Value)>,
) -> (EscrowClient, tokio::task::JoinHandle<Vec<Value>>) {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let server = tokio::spawn(async move {
        let mut calls = Vec::new();
        for (method, result) in replies {
            let (mut stream, _) = listener.accept().await.unwrap();
            let mut data = Vec::new();
            let (header_end, length) = loop {
                let mut chunk = [0; 4096];
                let count = stream.read(&mut chunk).await.unwrap();
                assert!(count > 0);
                data.extend_from_slice(&chunk[..count]);
                if let Some(end) = data.windows(4).position(|chunk| chunk == b"\r\n\r\n") {
                    let header = std::str::from_utf8(&data[..end]).unwrap();
                    let length = header
                        .lines()
                        .find_map(|line| {
                            let (name, value) = line.split_once(':')?;
                            name.eq_ignore_ascii_case("content-length")
                                .then(|| value.trim().parse::<usize>().unwrap())
                        })
                        .unwrap();
                    break (end + 4, length);
                }
            };
            while data.len() < header_end + length {
                let mut chunk = [0; 4096];
                let count = stream.read(&mut chunk).await.unwrap();
                assert!(count > 0);
                data.extend_from_slice(&chunk[..count]);
            }
            let call: Value =
                serde_json::from_slice(&data[header_end..header_end + length]).unwrap();
            assert_eq!(call["method"], method);
            calls.push(call);
            let body = json!({"jsonrpc":"2.0","id":1,"result":result}).to_string();
            let response = format!(
                "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
                body.len(),
                body
            );
            stream.write_all(response.as_bytes()).await.unwrap();
        }
        calls
    });
    let client = EscrowClient {
        http: reqwest::Client::builder()
            .timeout(Duration::from_secs(2))
            .build()
            .unwrap(),
        url: format!("http://{address}"),
        program: maruvo_escrow::ID,
        reviewer,
        network: "localnet".into(),
    };
    (client, server)
}

#[tokio::test]
async fn recovery_waits_for_finalized_expiry_and_reads_a_newer_account_snapshot() {
    for (height, expected) in [(100, "prepared"), (101, "expired")] {
        let (client, request, server) =
            fixture(height, json!({"context":{"slot":150},"value":null}), false).await;
        assert_eq!(client.check_recovery(request).await.unwrap(), expected);
        let calls = tokio::time::timeout(Duration::from_secs(3), server)
            .await
            .unwrap()
            .unwrap();
        assert_eq!(calls[2]["params"][0]["commitment"], "finalized");
        assert_eq!(calls[3]["params"][1]["commitment"], "finalized");
        assert_eq!(calls[3]["params"][1]["minContextSlot"], 150);
    }
}

#[tokio::test]
async fn recovery_rejects_stale_or_missing_account_snapshots() {
    for snapshot in [
        json!({"context":{"slot":149},"value":null}),
        json!({"value":null}),
        json!({"context":{"slot":150}}),
    ] {
        let (client, request, server) = fixture(101, snapshot, false).await;
        assert_eq!(
            client.check_recovery(request).await.unwrap_err().code(),
            tonic::Code::Unavailable
        );
        tokio::time::timeout(Duration::from_secs(3), server)
            .await
            .unwrap()
            .unwrap();
    }
}

#[tokio::test]
async fn funded_escrow_blocks_recovery_even_after_the_transaction_expires() {
    let (client, request, server) =
        fixture(101, json!({"context":{"slot":150},"value":null}), true).await;
    assert_eq!(client.check_recovery(request).await.unwrap(), "confirmed");
    tokio::time::timeout(Duration::from_secs(3), server)
        .await
        .unwrap()
        .unwrap();
}

#[tokio::test]
async fn funding_failure_requires_a_confirmed_transaction_status() {
    for confirmation in ["processed", "confirmed", "finalized"] {
        for error in [Value::Null, json!({"InstructionError":[0,"Custom"]})] {
            let reviewer = Pubkey::new_unique();
            let request = request_fixture(reviewer);
            let replies = vec![
                ("getAccountInfo", json!({"value":null})),
                (
                    "getSignatureStatuses",
                    json!({"value":[{"err":error,"confirmationStatus":confirmation}]}),
                ),
            ];
            let (client, server) = rpc_fixture(reviewer, replies).await;
            let expected = if confirmation != "processed" && !error.is_null() {
                "failed"
            } else {
                "pending"
            };
            assert_eq!(client.check(request).await.unwrap(), expected);
            tokio::time::timeout(Duration::from_secs(3), server)
                .await
                .unwrap()
                .unwrap();
        }
    }
}

#[tokio::test]
async fn funding_rejects_missing_account_and_height_responses() {
    for snapshot in [json!({}), json!({"context":{"slot":150}})] {
        let reviewer = Pubkey::new_unique();
        let request = request_fixture(reviewer);
        let (client, server) = rpc_fixture(reviewer, vec![("getAccountInfo", snapshot)]).await;
        assert_eq!(
            client.check(request).await.unwrap_err().code(),
            tonic::Code::Unavailable
        );
        server.await.unwrap();
    }
    let reviewer = Pubkey::new_unique();
    let mut request = request_fixture(reviewer);
    request.signature.clear();
    let (client, server) = rpc_fixture(
        reviewer,
        vec![
            ("getAccountInfo", json!({"value":null})),
            ("getBlockHeight", Value::Null),
        ],
    )
    .await;
    assert_eq!(
        client.check(request).await.unwrap_err().code(),
        tonic::Code::Unavailable
    );
    server.await.unwrap();
}

#[tokio::test]
async fn funding_expiry_preserves_the_block_height_boundary() {
    for (height, signed, expected) in [
        (100, false, "prepared"),
        (100, true, "pending"),
        (101, true, "expired"),
    ] {
        let reviewer = Pubkey::new_unique();
        let mut request = request_fixture(reviewer);
        let mut replies = vec![("getAccountInfo", json!({"value":null}))];
        if signed {
            replies.push(("getSignatureStatuses", json!({"value":[null]})));
        } else {
            request.signature.clear();
        }
        replies.push(("getBlockHeight", json!(height)));
        let (client, server) = rpc_fixture(reviewer, replies).await;
        assert_eq!(client.check(request).await.unwrap(), expected);
        server.await.unwrap();
    }
}

#[test]
fn malformed_signature_statuses_cannot_authorize_a_state_change() {
    for response in [
        json!({}),
        json!({"value":[]}),
        json!({"value":[null,null]}),
        json!({"value":[{}]}),
        json!({"value":[{"confirmationStatus":"confirmed"}]}),
        json!({"value":[{"err":null,"confirmationStatus":"unknown"}]}),
    ] {
        assert_eq!(
            signature_status(&response).unwrap_err().code(),
            tonic::Code::Unavailable
        );
    }
    assert!(
        signature_status(&json!({"value":[null]}))
            .unwrap()
            .is_none()
    );
}

#[tokio::test]
async fn devnet_requires_the_full_genesis_hash_and_a_deployed_program() {
    for (genesis, deployed, valid) in [
        ("EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG", true, true),
        ("EtWTRABZaYq6iMfeYKouRu166VU2xqa1", true, false),
        ("5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d", true, false),
        ("EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG", false, false),
    ] {
        let mut replies = vec![("getGenesisHash", json!(genesis))];
        if genesis == "EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG" {
            replies.push(("getAccountInfo", json!({"value":{"executable":deployed}})));
        }
        let (mut client, server) = rpc_fixture(Pubkey::new_unique(), replies).await;
        client.network = "devnet".into();
        let result = client.verify_network().await;
        assert_eq!(result.is_ok(), valid);
        if let Err(error) = result {
            assert_eq!(error.code(), tonic::Code::FailedPrecondition);
        }
        server.await.unwrap();
    }
}
