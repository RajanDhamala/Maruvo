package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type demoResponse struct {
	Message string `json:"message"`
	Service string `json:"service"`
}

type demoResult struct {
	response demoResponse
	err      error
}

type model struct {
	ctx      context.Context
	apiURL   string
	message  string
	loading  bool
	response demoResponse
	err      error
}

func (m model) Init() tea.Cmd {
	return m.sendDemo()
}

func (m model) sendDemo() tea.Cmd {
	return func() tea.Msg {
		response, err := requestDemo(m.ctx, m.apiURL, m.message)
		return demoResult{response: response, err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "enter", "r":
			if !m.loading {
				m.loading, m.err = true, nil
				return m, m.sendDemo()
			}
		}
	case demoResult:
		m.loading = false
		m.response, m.err = msg.response, msg.err
	}
	return m, nil
}

func (m model) View() tea.View {
	s := fmt.Sprintf("Maruvo\n\nConnection demo\nAPI: %s\nMessage: %s\n\n", m.apiURL, m.message)
	switch {
	case m.loading:
		s += "Sending to Rust through the Go API...\n"
	case m.err != nil:
		s += fmt.Sprintf("Connection failed: %v\nStart make rust and make api, then retry.\n", m.err)
	default:
		s += fmt.Sprintf("Connected to %s\n%s\n", m.response.Service, m.response.Message)
	}
	s += "\nEnter / r: send again    q: quit\n"
	return tea.NewView(s)
}

func requestDemo(ctx context.Context, apiURL, message string) (demoResponse, error) {
	var result demoResponse
	body, err := json.Marshal(map[string]string{"message": message})
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+"/demo", bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return result, fmt.Errorf("call Go API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return result, fmt.Errorf("Go API returned %s: %s", response.Status, strings.TrimSpace(string(detail)))
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result); err != nil {
		return result, fmt.Errorf("decode demo response: %w", err)
	}
	if result.Message == "" || result.Service == "" {
		return result, fmt.Errorf("Go API returned an incomplete demo response")
	}
	return result, nil
}

func main() {
	defaultURL := os.Getenv("API_URL")
	if defaultURL == "" {
		defaultURL = "http://127.0.0.1:3000"
	}
	apiURL := flag.String("api", defaultURL, "Go API base URL (or set API_URL)")
	message := flag.String("message", "Hello from Bubble Tea", "demo message to send to Rust")
	check := flag.Bool("check", false, "send once without a terminal UI; exit nonzero on failure")
	flag.Parse()
	*apiURL = strings.TrimRight(*apiURL, "/")
	parsed, err := url.Parse(*apiURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.RawQuery != "" || parsed.Fragment != "" {
		fmt.Fprintln(os.Stderr, "API URL must be an http(s) base URL without a query or fragment")
		os.Exit(1)
	}
	if err := run(*apiURL, *message, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(apiURL, message string, check bool) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if check {
		response, err := requestDemo(ctx, apiURL, message)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(response)
	}
	_, err := tea.NewProgram(model{
		ctx: ctx, apiURL: apiURL, message: message, loading: true,
	}).Run()
	return err
}
