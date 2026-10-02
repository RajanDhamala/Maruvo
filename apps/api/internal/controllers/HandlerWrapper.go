package controller

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rajandhamala/Maruvo/internal/utils"
	"google.golang.org/grpc"

	sqlc "github.com/rajandhamala/Maruvo/db/sqlc"
)

type Controller struct {
	pool      *pgxpool.Pool
	rpc       *grpc.ClientConn
	oauth     *utils.OAuthConfig
	queries   *sqlc.Queries
	cliLogins *utils.CLILogins
}

func NewController(pool *pgxpool.Pool, rpc *grpc.ClientConn, oauth *utils.OAuthConfig) *Controller {
	return &Controller{
		pool:      pool,
		rpc:       rpc,
		oauth:     oauth,
		queries:   sqlc.New(pool),
		cliLogins: utils.NewCLILogins(),
	}
}
