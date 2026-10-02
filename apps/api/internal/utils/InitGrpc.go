package utils

import (
	"fmt"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func InitGrpc() (*grpc.ClientConn, error) {
	rustAddr := os.Getenv("RUST_RPC_ADDR")

	if rustAddr == "" {
		rustAddr = "127.0.0.1:50051"
	}

	client, err := grpc.NewClient(rustAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, err
	}

	fmt.Println("GRPC init Succesfully")

	return client, nil
}
