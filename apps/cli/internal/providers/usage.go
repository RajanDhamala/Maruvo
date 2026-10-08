package providers

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Usage struct {
	InputTokens     *int64       `json:"prompt_tokens,omitempty"`
	OutputTokens    *int64       `json:"completion_tokens,omitempty"`
	TotalTokens     *int64       `json:"total_tokens,omitempty"`
	CacheHitTokens  *int64       `json:"prompt_cache_hit_tokens,omitempty"`
	CacheMissTokens *int64       `json:"prompt_cache_miss_tokens,omitempty"`
	Cost            *json.Number `json:"cost,omitempty"`
	PromptDetails   *struct {
		CachedTokens     *int64 `json:"cached_tokens,omitempty"`
		CacheWriteTokens *int64 `json:"cache_write_tokens,omitempty"`
	} `json:"prompt_tokens_details,omitempty"`
	CompletionDetails *struct {
		ReasoningTokens *int64 `json:"reasoning_tokens,omitempty"`
	} `json:"completion_tokens_details,omitempty"`
}

func (u *Usage) Summary(provider string) string {
	if u == nil {
		return ""
	}

	var fields []string

	add := func(label string, value *int64) {
		if value != nil && *value >= 0 {
			fields = append(fields, fmt.Sprintf("%s %d", label, *value))
		}
	}
	add("input", u.InputTokens)
	add("output", u.OutputTokens)

	cacheHit := u.CacheHitTokens
	if cacheHit == nil && u.PromptDetails != nil {
		cacheHit = u.PromptDetails.CachedTokens
	}

	add("cache hit", cacheHit)
	add("cache miss", u.CacheMissTokens)

	if u.PromptDetails != nil {
		add("cache write", u.PromptDetails.CacheWriteTokens)
	}

	if u.CompletionDetails != nil {
		add("reasoning", u.CompletionDetails.ReasoningTokens)
	}

	add("total", u.TotalTokens)

	if u.Cost != nil {
		if value, err := u.Cost.Float64(); err == nil && value >= 0 {
			cost := "cost " + u.Cost.String()
			if provider == "openrouter" {
				cost += " credits"
			}

			fields = append(fields, cost)
		}
	}

	if len(fields) == 0 {
		return ""
	}

	return "Usage (API): " + strings.Join(fields, " · ")
}
