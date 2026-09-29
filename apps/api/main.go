package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	pb "github.com/rajandhamala/Maruvo/pb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	PORT := "3000"

	conn, err := grpc.NewClient(
		"127.0.0.1:50051",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		fmt.Println("error while concneting to grpc")
		panic(err)
	}

	defer conn.Close()

	client := pb.NewHelloServiceClient(conn)

	app := http.NewServeMux()

	app.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("server is up and running\n"))
	})
	type helloReq struct {
		name string
		age  int32
	}

	app.HandleFunc("GET /test/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "" {
			name = "World"
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		data := &pb.HelloRequest{
			Name: "rajan",
			Age:  22,
		}
		response, err := client.SayHello(ctx, data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"isEligible": response.GetIsEligible(),
		})
	})

	fmt.Println("Server listening on port", PORT)

	err = http.ListenAndServe("127.0.0.1:"+PORT, app)
	if err != nil {
		fmt.Println("Error:", err)
	}
}
