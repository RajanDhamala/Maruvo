package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rajandhamala/Maruvo/internal/controllers"
	"github.com/rajandhamala/Maruvo/internal/routes"
	"github.com/rajandhamala/Maruvo/internal/utils"

	"github.com/joho/godotenv"
)

func main() {
	app := http.NewServeMux()

	err := godotenv.Load()
	_ = godotenv.Load("../../.solana/env")

	if err != nil {
		fmt.Println("failed to load env")
	}

	port := os.Getenv("PORT")
	host := os.Getenv("HOST")

	pool, err := utils.ConnectDb()
	if err != nil {
		fmt.Println("error while connceting to db", err.Error())
		panic(err)
	}
	defer pool.Close()

	rpc, err := utils.InitGrpc()
	if err != nil {
		fmt.Println("err while connecting to rpc server", err.Error())
		panic(err)
	}
	defer rpc.Close()

	oauthConfig := utils.NewOAuthConfig()
	ctrl := controller.NewController(pool, rpc, oauthConfig)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	monitorDone := make(chan struct{})

	go func() {
		defer close(monitorDone)

		ctrl.RunEscrowMonitor(ctx)
	}()

	defer func() {
		stop()
		<-monitorDone
	}()

	if host == "" || port == "" {
		panic("HOST, PORT are required")
	}

	routes.UserRouter(app, ctrl)
	routes.OauthRoute(app, ctrl)
	routes.PostRoute(app, ctrl)

	address := net.JoinHostPort(host, port)
	fmt.Println("server running on", address)

	server := &http.Server{
		Addr:              address,
		Handler:           app,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = server.Shutdown(shutdownCtx)
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Println("server error:", err)
	}
}
