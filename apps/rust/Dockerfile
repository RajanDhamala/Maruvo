FROM rust:1.93-slim-bookworm AS build
WORKDIR /app/apps/rust

RUN apt-get update && apt-get install -y --no-install-recommends protobuf-compiler \
    && rm -rf /var/lib/apt/lists/*

COPY apps/rust/Cargo.toml apps/rust/Cargo.lock apps/rust/build.rs ./

COPY apps/rust/src/ ./src/

COPY proto/ /app/proto/

RUN cargo build --release --locked

FROM debian:bookworm-slim
WORKDIR /app

RUN groupadd --gid 10001 appgroup \
    && useradd --uid 10001 --gid appgroup --no-create-home appuser

COPY --from=build --chown=appuser:appgroup /app/apps/rust/target/release/rust ./runtime

ENV RUST_RPC_ADDR=0.0.0.0:50051

USER appuser:appgroup
EXPOSE 50051

CMD ["./runtime"]
