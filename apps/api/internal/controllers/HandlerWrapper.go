package controller

import (
	"github.com/jackc/pgx/v5/pgxpool"
)

type Controller struct {
	name string
	pool *pgxpool.Pool
}

func NewController(pool *pgxpool.Pool) *Controller {
	return &Controller{
		name: "test",
		pool: pool,
	}
}
