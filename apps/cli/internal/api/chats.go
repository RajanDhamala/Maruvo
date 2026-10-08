package api

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) SaveAgentChat(ctx context.Context, token, profile, id string, snapshot any) error {
	return c.requestJSON(
		ctx,
		http.MethodPut,
		"/agent/chats/"+url.PathEscape(id)+"?profile="+url.QueryEscape(profile),
		token,
		snapshot,
		nil,
	)
}

func (c *Client) AgentChats(ctx context.Context, token, profile string, result any) error {
	return c.request(
		ctx,
		http.MethodGet,
		"/agent/chats?profile="+url.QueryEscape(profile),
		token,
		nil,
		result,
	)
}

func (c *Client) AgentChat(ctx context.Context, token, profile, id string, result any) error {
	return c.request(
		ctx,
		http.MethodGet,
		"/agent/chats/"+url.PathEscape(id)+"?profile="+url.QueryEscape(profile),
		token,
		nil,
		result,
	)
}
