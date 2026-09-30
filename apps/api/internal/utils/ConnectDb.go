package utils

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func ConnectDb() (*pgxpool.Pool, error) {
	url := os.Getenv("DATABASE_URL")

	if url == "" {
		return nil, errors.New("DATABASE_URL is missing")
	}
	ctx, cancle := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancle()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}

	err = pool.Ping(ctx)
	if err != nil {
		pool.Close()
		return nil, err
	}

	fmt.Println("Connceted to the Databse Succesfully")
	return pool, nil
}
