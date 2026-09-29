package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	pb "github.com/rajandhamala/Maruvo/pb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
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
	app.HandleFunc("POST /demo", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Message string `json:"message"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "expected JSON with a message string", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			http.Error(w, "expected a single JSON object", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		response, err := client.Demo(ctx, &pb.DemoRequest{Message: input.Message})
		if err != nil {
			if status.Code(err) == codes.InvalidArgument {
				http.Error(w, status.Convert(err).Message(), http.StatusBadRequest)
				return
			}
			log.Printf("Rust demo RPC failed: %v", err)
			http.Error(w, "Rust RPC service unavailable", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"message": response.GetMessage(),
			"service": response.GetService(),
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
