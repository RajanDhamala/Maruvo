package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	pb "github.com/rajandhamala/Maruvo/pb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	apiAddr := envOrDefault("API_ADDR", "127.0.0.1:3000")
	rustAddr := envOrDefault("RUST_RPC_ADDR", "127.0.0.1:50051")

	conn, err := grpc.NewClient(
		rustAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("create Rust gRPC client: %v", err)
	}

	defer conn.Close()

	client := pb.NewSolanaServiceClient(conn)

	app := http.NewServeMux()

	app.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("server is up and running\n"))
	})
	// This checks the complete HTTP -> Go -> gRPC -> Rust connection.
	app.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		response, err := client.Health(ctx, &pb.HealthRequest{})
		if err != nil {
			log.Printf("Rust health RPC failed: %v", err)
			http.Error(w, "Rust RPC service unavailable", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"message": response.GetMessage(),
		})
	})

	server := &http.Server{
		Addr:              apiAddr,
		Handler:           app,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("Go API listening on http://%s; Rust gRPC target %s", apiAddr, rustAddr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
