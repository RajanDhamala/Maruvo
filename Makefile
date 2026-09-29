.PHONY: proto api rust

# Go needs committed bindings; Rust generates its bindings during cargo build.
proto:
	protoc -I proto --go_out=apps/api/pb --go_opt=paths=source_relative \
		--go-grpc_out=apps/api/pb --go-grpc_opt=paths=source_relative \
		proto/solana.proto

api:
	cd apps/api && go run .

rust:
	cargo run --locked --manifest-path apps/rust/Cargo.toml
