package api

import (
	"context"
	"net/http"
)

type Settlement struct {
	State             string `json:"state"`
	Action            string `json:"action"`
	Note              string `json:"note"`
	SubmissionVersion int64  `json:"submission_version"`
	Signature         string `json:"signature"`
	FeeLamports       int64  `json:"fee_lamports"`
	Transaction       string `json:"transaction"`
}

type SettlementPlan struct {
	Settlement Settlement `json:"settlement"`
	Escrow     Escrow     `json:"escrow"`
}

func (c *Client) PrepareSettlement(
	ctx context.Context,
	token string,
	id, version int64,
	action, note string,
) (SettlementPlan, error) {
	var result SettlementPlan

	err := c.requestJSON(
		ctx,
		http.MethodPost,
		workspacePath(id)+"/settle",
		token,
		map[string]any{"action": action, "note": note, "submission_version": version},
		&result,
	)

	return result, err
}

func (c *Client) SubmitSettlement(ctx context.Context, token string, id int64, transaction string) error {
	return c.requestJSON(
		ctx,
		http.MethodPost,
		workspacePath(id)+"/settle/submit",
		token,
		map[string]string{"transaction": transaction},
		nil,
	)
}

func (c *Client) RequestChanges(ctx context.Context, token string, id, version int64, note string) error {
	return c.requestJSON(
		ctx,
		http.MethodPost,
		workspacePath(id)+"/review/changes",
		token,
		map[string]any{"note": note, "submission_version": version},
		nil,
	)
}
