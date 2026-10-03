package api

import (
	"context"
	"net/http"
)

type Escrow struct {
	AgreementVersion int32  `json:"agreement_version"`
	State            string `json:"state"`
	Address          string `json:"address"`
	ProgramID        string `json:"program_id"`
	Reviewer         string `json:"reviewer"`
	Network          string `json:"network"`
	Signature        string `json:"signature"`
	Transaction      string `json:"transaction"`
	FeeLamports      int64  `json:"fee_lamports"`
	StorageLamports  int64  `json:"storage_lamports"`
}

type PostInfo struct {
	Post   Post   `json:"post"`
	Escrow Escrow `json:"escrow"`
}

func (c *Client) Wallet(ctx context.Context, token string) (string, error) {
	var result struct {
		Address string `json:"address"`
	}

	err := c.request(ctx, http.MethodGet, "/wallet", token, nil, &result)

	return result.Address, err
}

func (c *Client) WalletChallenge(ctx context.Context, token, address string) (string, error) {
	var result struct {
		Message string `json:"message"`
	}

	err := c.requestJSON(
		ctx,
		http.MethodPost,
		"/wallet/challenge",
		token,
		map[string]string{"address": address},
		&result,
	)

	return result.Message, err
}

func (c *Client) LinkWallet(ctx context.Context, token, signature string) error {
	return c.requestJSON(
		ctx,
		http.MethodPost,
		"/wallet/link",
		token,
		map[string]string{"signature": signature},
		nil,
	)
}

func (c *Client) AcceptPost(ctx context.Context, token string, id int64) (Post, error) {
	var result struct {
		Post Post `json:"post"`
	}

	err := c.requestJSON(ctx, http.MethodPost, "/posts/accept", token, map[string]int64{"id": id}, &result)

	return result.Post, err
}

func (c *Client) PostInfo(ctx context.Context, token string, id int64) (PostInfo, error) {
	var result PostInfo

	err := c.requestJSON(ctx, http.MethodPost, "/posts/info", token, map[string]int64{"id": id}, &result)

	return result, err
}

func (c *Client) PrepareFunding(ctx context.Context, token string, id int64) (PostInfo, error) {
	var result PostInfo

	err := c.requestJSON(ctx, http.MethodPost, "/posts/fund", token, map[string]int64{"id": id}, &result)

	return result, err
}

func (c *Client) SubmitFunding(
	ctx context.Context,
	token string,
	id int64,
	transaction string,
) (PostInfo, error) {
	var result PostInfo

	err := c.requestJSON(ctx, http.MethodPost, "/posts/fund/submit", token, struct {
		ID          int64  `json:"id"`
		Transaction string `json:"transaction"`
	}{id, transaction}, &result)

	return result, err
}
