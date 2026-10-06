.PHONY: proto api rust cli build build-api build-cli build-rust test migrate-down migrate-up sqlc tui lintapi lintui solana-local solana-build solana-check solana-devnet local

proto:
	protoc -I proto \
		--go_out=apps/api/pb \
		--go_opt=paths=source_relative \
		--go-grpc_out=apps/api/pb \
		--go-grpc_opt=paths=source_relative \
		proto/solana.proto

MIGRATIONS_DIR := apps/api/db/migrations

migrate-up:
	@read -p "Database URL: " DB_URL; \
	goose -dir $(MIGRATIONS_DIR) postgres "$$DB_URL" up

migrate-down:
	@read -p "Database URL: " DB_URL; \
	goose -dir $(MIGRATIONS_DIR) postgres "$$DB_URL" down

sqlc:
	cd apps/api/ && sqlc generate

api:
	cd apps/api/ && go run ./cmd/api/main.go

rust:
	cargo run --locked --manifest-path apps/rust/Cargo.toml

cli:
	go -C apps/cli run ./main.go $(ARGS)

tui:
	go -C apps/cli run ./main.go $(ARGS)

lintapi:
	cd apps/api && golangci-lint fmt && golangci-lint run --fix $(ARGS) ./...

lintui:
	cd apps/cli && golangci-lint fmt && golangci-lint run --fix $(ARGS) ./...

solana-build:
	./scripts/solana-build.sh

solana-local:
	./scripts/solana-local.sh

local:
	./scripts/local.sh

solana-devnet:
	./scripts/solana-devnet.sh

solana-check:
	./scripts/solana-check.sh

build: build-api build-cli build-rust

build-api:
	mkdir -p bin
	cd apps/api/cmd/api && go build -mod=readonly -o ../../bin/maruvo-api/cmd/api .

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
