.PHONY: proto api rust cli build build-api build-cli build-rust test

proto:
	protoc -I proto --go_out=apps/api/pb --go_opt=paths=source_relative \
		--go-grpc_out=apps/api/pb --go-grpc_opt=paths=source_relative \
		proto/solana.proto

api:
	cd apps/api && go run .

rust:
	cargo run --locked --manifest-path apps/rust/Cargo.toml

cli:
	cd apps/cli && go run . $(ARGS)

build: build-api build-cli build-rust

build-api:
	mkdir -p bin
	cd apps/api && go build -mod=readonly -o ../../bin/maruvo-api .

build-cli:
	mkdir -p bin
	cd apps/cli && go build -mod=readonly -o ../../bin/maruvo .

build-rust:
	mkdir -p bin
	cargo build --locked --manifest-path apps/rust/Cargo.toml --target-dir apps/rust/target
	cp apps/rust/target/debug/rust bin/maruvo-rust

test:
	cd apps/api && go test -mod=readonly ./...
	cd apps/cli && go test -mod=readonly ./...
	cargo test --locked --manifest-path apps/rust/Cargo.toml
