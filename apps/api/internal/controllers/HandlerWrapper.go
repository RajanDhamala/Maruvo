package controller

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rajandhamala/Maruvo/internal/utils"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	sqlc "github.com/rajandhamala/Maruvo/db/sqlc"
)

type Controller struct {
	pool      *pgxpool.Pool
	rpc       *grpc.ClientConn
	redis     *redis.Client
	oauth     *utils.OAuthConfig
	queries   *sqlc.Queries
	cliLogins *utils.CLILogins
}

func NewController(
	pool *pgxpool.Pool,
	rpc *grpc.ClientConn,
	oauth *utils.OAuthConfig,
	redisClient *redis.Client,
) *Controller {
	return &Controller{
		pool:      pool,
		rpc:       rpc,
		redis:     redisClient,
		oauth:     oauth,
		queries:   sqlc.New(pool),
		cliLogins: utils.NewCLILogins(redisClient),
	}
}
