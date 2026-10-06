package tui

import (
	"context"
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
)

type providerSettings struct {
	open, busy bool
	saving     bool
	provider   int
	step       int
	key, query textField
	models     []providers.Model
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
		if strings.Contains(strings.ToLower(item.ID), strings.ToLower(m.providers.query.value)) {
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

func (m model) saveProvider() (tea.Model, tea.Cmd) {
	p := &m.providers

	indices := m.providerModelIndices()
	if len(indices) == 0 {
		return m, nil
	}

	p.selection = min(p.selection, len(indices)-1)
	p.sequence++
	name, id := providers.Names[p.provider], p.models[indices[p.selection]].ID
	profile, secret, sequence := m.profile, p.secret, p.sequence
	p.busy, p.saving, p.err = true, true, nil
	p.key, p.secret = textField{limit: 4096}, ""

	return m, func() tea.Msg {
		err := providers.SaveConnection(profile, name, id, secret)
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
			return m.saveProvider()
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
