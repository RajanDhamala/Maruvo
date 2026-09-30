package main

import (
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/rajandhamala/Maruvo/internal/controllers"
	"github.com/rajandhamala/Maruvo/internal/routes"
	"github.com/rajandhamala/Maruvo/internal/utils"

	"github.com/joho/godotenv"
)

func main() {
	app := http.NewServeMux()
	err := godotenv.Load()
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

	ctrl := controller.NewController(pool)

	if host == "" || port == "" {
		panic("HOST, PORT are required")
	}

	routes.UserRouter(app, ctrl)

	address := net.JoinHostPort(host, port)
	fmt.Println("server running on", address)

	if err := http.ListenAndServe(address, app); err != nil {
		fmt.Println("server error:", err)
	}
}
