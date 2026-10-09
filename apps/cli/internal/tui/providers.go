package tui

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
)

const (
	providerChoose = iota
	providerKey
	providerModel
	providerOptions
)

type providerSettings struct {
	open, busy bool
	saving     bool
	provider   int
	step       int
	key, query textField
	models     []providers.Model
	chosen     providers.Model
	options    providers.Options
	optionRow  int
	selection  int
	secret     string
	config     providers.Config
	err        error
	sequence   uint64
	cancel     context.CancelFunc
}

type providerConfigLoaded struct {
	sequence uint64
	config   providers.Config
	err      error
}

type startupProviderLoaded struct {
	sequence uint64
	config   providers.Config
	err      error
}

func (m model) checkStartupProvider() tea.Cmd {
	profile, sequence := m.profile, m.providers.sequence
	return func() tea.Msg {
		config, err := providers.LoadConfig(profile)
		return startupProviderLoaded{sequence: sequence, config: config, err: err}
	}
}

type providerModelsLoaded struct {
	sequence uint64
	models   []providers.Model
	secret   string
	err      error
}

type providerSaved struct {
	sequence uint64
	removed  bool
	err      error
}

func providerLabel(name string) string {
	if name == "deepseek" {
		return "DeepSeek"
	}

	return "OpenRouter"
}

func (m model) openProviders() (tea.Model, tea.Cmd) {
	m.commands.open = false
	m.providers = providerSettings{
		open: true, busy: true, sequence: m.providers.sequence + 1,
		key: textField{limit: 4096}, query: textField{limit: 200},
	}
	profile, sequence := m.profile, m.providers.sequence

	return m, func() tea.Msg {
		config, err := providers.LoadConfig(profile)
		return providerConfigLoaded{sequence: sequence, config: config, err: err}
	}
}

func (m model) providerIndices() []int {
	var indices []int

	query := strings.ToLower(m.providers.query.value)
	for i, name := range providers.Names {
		if strings.Contains(strings.ToLower(providerLabel(name)), query) {
			indices = append(indices, i)
		}
	}

	return indices
}

func (m model) providerModelIndices() []int {
	var indices []int

	for i, item := range m.providers.models {
		if strings.Contains(
			strings.ToLower(item.ID+" "+item.DisplayName()),
			strings.ToLower(m.providers.query.value),
		) {
			indices = append(indices, i)
		}
	}

	return indices
}

func (m model) chooseProvider(index int) (tea.Model, tea.Cmd) {
	p := &m.providers
	p.provider, p.step, p.selection, p.err = index, providerKey, 0, nil
	p.query, p.key = textField{limit: 200}, textField{limit: 4096}
	p.models, p.secret = nil, ""
	p.sequence++

	return m, nil
}

func (m model) closeProviders() (tea.Model, tea.Cmd) {
	if m.providers.saving {
		return m, nil
	}

	if m.providers.cancel != nil {
		m.providers.cancel()
	}

	m.providers = providerSettings{sequence: m.providers.sequence + 1}

	return m, nil
}

func (m model) backProvider() (tea.Model, tea.Cmd) {
	p := &m.providers
	if p.saving {
		return m, nil
	}

	if p.step == providerChoose {
		return m.closeProviders()
	}

	if p.step == providerOptions {
		p.step, p.err = providerModel, nil
		return m, nil
	}

	if p.cancel != nil {
		p.cancel()
	}

	p.sequence++
	p.busy, p.err = false, nil

	p.query = textField{limit: 200}
	if p.step == providerModel {
		p.step = providerKey
		p.key = textField{value: p.secret, cursor: utf8.RuneCountInString(p.secret), limit: 4096}
	} else {
		p.step, p.selection = providerChoose, p.provider
		p.key = textField{limit: 4096}
	}

	p.models, p.secret = nil, ""

	return m, nil
}

func (m model) checkProvider() (tea.Model, tea.Cmd) {
	p := &m.providers
	p.sequence++
	name, secret := providers.Names[p.provider], strings.TrimSpace(p.key.value)
	profile, sequence := m.profile, p.sequence
	p.busy, p.err = true, nil
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	p.cancel = cancel

	return m, func() tea.Msg {
		defer cancel()

		if secret == "" {
			var err error

			secret, _, err = providers.Credential(profile, name)
			if err != nil {
				return providerModelsLoaded{sequence: sequence, err: err}
			}
		}

		client, err := providers.NewClient(name, "", secret)
		if err != nil {
			return providerModelsLoaded{sequence: sequence, err: err}
		}

		models, err := client.Models(ctx)

		return providerModelsLoaded{sequence: sequence, models: models, secret: secret, err: err}
	}
}

func (m model) chooseProviderModel() (tea.Model, tea.Cmd) {
	p := &m.providers

	indices := m.providerModelIndices()
	if len(indices) == 0 {
		return m, nil
	}

	p.selection = min(p.selection, len(indices)-1)
	p.chosen = p.models[indices[p.selection]]
	name := providers.Names[p.provider]

	p.options = providers.Options{MaxTokens: min(providers.DefaultMaxTokens, p.chosen.OutputLimit(name))}
	if saved := p.config.Connections[name]; saved.Model == p.chosen.ID &&
		p.chosen.ValidateOptions(name, saved.Options) == nil {
		p.options = saved.Options
	}

	p.step, p.optionRow, p.err = providerOptions, 0, nil

	return m, nil
}

func (m model) saveProvider() (tea.Model, tea.Cmd) {
	p := &m.providers

	name := providers.Names[p.provider]
	if err := p.chosen.ValidateOptions(name, p.options); err != nil {
		p.err = err
		return m, nil
	}

	p.sequence++
	id, options := p.chosen.ID, p.options
	profile, secret, sequence := m.profile, p.secret, p.sequence
	p.busy, p.saving, p.err = true, true, nil
	p.key, p.secret = textField{limit: 4096}, ""

	return m, func() tea.Msg {
		err := providers.SaveConnection(profile, name, id, secret, options)
		return providerSaved{sequence: sequence, err: err}
	}
}

func (m model) updateProviders(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := &m.providers
	if msg.String() == "esc" {
		return m.backProvider()
	}

	if p.busy {
		return m, nil
	}

	if p.step == providerOptions && msg.String() != "ctrl+d" {
		return m.updateProviderOptions(msg)
	}

	switch msg.String() {
	case "up", "down", "tab", "shift+tab":
		if p.step == providerKey {
			return m, nil
		}

		count := len(m.providerIndices())
		if p.step == providerModel {
			count = len(m.providerModelIndices())
		}

		direction := 1
		if msg.String() == "up" || msg.String() == "shift+tab" {
			direction = -1
		}

		p.selection = max(0, min(p.selection+direction, count-1))
	case "enter":
		switch p.step {
		case providerChoose:
			indices := m.providerIndices()
			if len(indices) > 0 {
				return m.chooseProvider(indices[min(p.selection, len(indices)-1)])
			}
		case providerKey:
			return m.checkProvider()
		case providerModel:
			return m.chooseProviderModel()
		}
	case "ctrl+d":
		if p.step == providerChoose {
			indices := m.providerIndices()
			if len(indices) == 0 {
				return m, nil
			}

			p.provider = indices[min(p.selection, len(indices)-1)]
		}

		p.sequence++
		name, profile, sequence := providers.Names[p.provider], m.profile, p.sequence
		p.busy, p.saving, p.err = true, true, nil
		p.key, p.secret = textField{limit: 4096}, ""

		return m, func() tea.Msg {
			return providerSaved{sequence: sequence, removed: true, err: providers.Disconnect(profile, name)}
		}
	default:
		if p.step == providerKey {
			p.key.key(msg)
		} else {
			p.query.key(msg)
			p.selection = 0
		}
	}

	return m, nil
}

func (m model) providerOptionValues(row int) []string {
	p := m.providers

	name := providers.Names[p.provider]
	if row == 0 {
		return p.chosen.ReasoningLevels(name)
	}

	limit := p.chosen.OutputLimit(name)
	numbers := []int{4096, 8192, 16384, 32768, 65536, 131072, 262144}

	current := p.options.OutputLimit()
	if row == 2 {
		limit, current = p.options.OutputLimit()-1, p.options.ReasoningTokens
		numbers = []int{0, 1024, 2048, 4096, 8192, 16384, 32768, 65536}
	}

	if current <= limit && !slices.Contains(numbers, current) {
		numbers = append(numbers, current)
	}

	if row == 1 && !slices.Contains(numbers, limit) {
		numbers = append(numbers, limit)
	}

	slices.Sort(numbers)

	var values []string

	for _, number := range numbers {
		if number <= limit {
			values = append(values, strconv.Itoa(number))
		}
	}

	return values
}

func (m model) changeProviderOption(direction int) (tea.Model, tea.Cmd) {
	p := &m.providers
	values := m.providerOptionValues(p.optionRow)

	current := p.options.Reasoning
	if p.optionRow == 1 {
		current = strconv.Itoa(p.options.OutputLimit())
	} else if p.optionRow == 2 {
		current = strconv.Itoa(p.options.ReasoningTokens)
	}

	index := slices.Index(values, current)
	value := values[(max(0, index)+direction+len(values))%len(values)]

	switch p.optionRow {
	case 0:
		p.options.Reasoning, p.options.ReasoningTokens = value, 0
	case 1:
		p.options.MaxTokens, _ = strconv.Atoi(value)
		if p.options.ReasoningTokens >= p.options.OutputLimit() {
			p.options.ReasoningTokens = 0
		}
	case 2:
		p.options.ReasoningTokens, _ = strconv.Atoi(value)
		p.options.Reasoning = ""
	}

	p.err = nil

	return m, nil
}

func (m model) updateProviderOptions(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := &m.providers

	rows := 2
	if p.chosen.SupportsReasoningBudget(providers.Names[p.provider]) {
		rows++
	}

	switch msg.String() {
	case "up", "shift+tab":
		p.optionRow = (p.optionRow + rows - 1) % rows
	case "down", "tab":
		p.optionRow = (p.optionRow + 1) % rows
	case "left":
		return m.changeProviderOption(-1)
	case "right", "space":
		return m.changeProviderOption(1)
	case "enter":
		return m.saveProvider()
	}

	return m, nil
}
