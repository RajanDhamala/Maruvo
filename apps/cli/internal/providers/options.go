package providers

import (
	"encoding/json"
	"errors"
	"slices"
)

const DefaultMaxTokens = 16384

var gatewayEfforts = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

type Options struct {
	Reasoning       string `json:"reasoning,omitempty"`
	MaxTokens       int    `json:"max_tokens,omitempty"`
	ReasoningTokens int    `json:"reasoning_tokens,omitempty"`
}

type ReasoningSupport struct {
	SupportedEfforts  json.RawMessage `json:"supported_efforts,omitempty"`
	DefaultEffort     string          `json:"default_effort,omitempty"`
	DefaultEnabled    bool            `json:"default_enabled"`
	Mandatory         bool            `json:"mandatory"`
	SupportsMaxTokens bool            `json:"supports_max_tokens"`
}

func (o Options) OutputLimit() int {
	if o.MaxTokens == 0 {
		return DefaultMaxTokens
	}

	return o.MaxTokens
}

func (o Options) Validate(provider string) error {
	if o.Reasoning != "" && !slices.Contains(gatewayEfforts, o.Reasoning) {
		return errors.New("invalid reasoning level")
	}

	if provider == "deepseek" && (o.ReasoningTokens != 0 ||
		(o.Reasoning != "" && !slices.Contains([]string{"none", "low", "high", "max"}, o.Reasoning))) {
		return errors.New("DeepSeek supports reasoning default, none, low, high, or max")
	}

	if o.MaxTokens < 0 || o.MaxTokens > 393216 || o.ReasoningTokens < 0 ||
		o.ReasoningTokens >= o.OutputLimit() {
		return errors.New("output limit must be 1–393216; reasoning budget must be smaller than output")
	}

	if o.ReasoningTokens > 0 && o.Reasoning != "" {
		return errors.New("choose a reasoning level or a reasoning token budget, not both")
	}

	return nil
}

func (m Model) ReasoningLevels(provider string) []string {
	levels := []string{""}

	if provider == "deepseek" {
		switch m.ID {
		case "deepseek-flash", "deepseek-v4-pro", "deepseek-v4-flash", "deepseek-v4-flash-vision-exp":
			return append(levels, "none", "low", "high", "max")
		}

		return levels
	}

	if m.Reasoning == nil {
		return levels
	}

	var supported []string
	if string(m.Reasoning.SupportedEfforts) == "null" {
		supported = gatewayEfforts
	} else {
		_ = json.Unmarshal(m.Reasoning.SupportedEfforts, &supported)
	}

	for _, effort := range gatewayEfforts {
		if slices.Contains(supported, effort) && !(effort == "none" && m.Reasoning.Mandatory) {
			levels = append(levels, effort)
		}
	}

	return levels
}

func (m Model) SupportsReasoningBudget(provider string) bool {
	return provider == "openrouter" && m.Reasoning != nil && m.Reasoning.SupportsMaxTokens
}

func (m Model) OutputLimit(provider string) int {
	limit := m.TopProvider.MaxCompletionTokens
	if provider == "deepseek" {
		limit = 393216
	}

	if m.ContextLength > 0 && (limit == 0 || m.ContextLength < limit) {
		limit = m.ContextLength
	}

	if limit <= 0 || limit > 393216 {
		limit = 393216
	}

	return limit
}

func (m Model) ValidateOptions(provider string, options Options) error {
	if err := options.Validate(provider); err != nil {
		return err
	}

	if !slices.Contains(m.ReasoningLevels(provider), options.Reasoning) {
		return errors.New("this model does not support the selected reasoning level")
	}

	if options.ReasoningTokens > 0 && !m.SupportsReasoningBudget(provider) {
		return errors.New("this model does not support a reasoning token budget")
	}

	if options.OutputLimit() > m.OutputLimit(provider) {
		return errors.New("output limit exceeds this model's maximum; choose a smaller --max-tokens")
	}

	return nil
}

func FindModel(models []Model, id string) (Model, error) {
	for _, model := range models {
		if model.ID == id {
			return model, nil
		}
	}

	return Model{}, errors.New("model is not available from this provider")
}
