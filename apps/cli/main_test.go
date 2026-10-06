package main

import "testing"

func TestAgentInvocation(t *testing.T) {
	for _, test := range []struct {
		args []string
		want bool
	}{
		{[]string{"agent", "tools"}, true},
		{[]string{"--profile", "worker", "--api=http://localhost:3000", "agent", "submit"}, true},
		{[]string{"--invalid", "agent", "tools"}, true},
		{[]string{"--", "agent", "tools"}, true},
		{[]string{"--message", "agent"}, false},
		{[]string{"--profile=agent"}, false},
		{nil, false},
	} {
		if got := agentInvocation(test.args); got != test.want {
			t.Fatalf("%v: agent=%v, want %v", test.args, got, test.want)
		}
	}
}

func TestLocalProviderCommandInvocation(t *testing.T) {
	for _, command := range []string{"provider", "chat"} {
		if got := commandInvocation([]string{"-profile", "worker", command}); got != command {
			t.Fatalf("local command: got %q, want %q", got, command)
		}

		if got := commandInvocation([]string{"-message", command}); got != "" {
			t.Fatal("flag values must not select local provider commands")
		}
	}
}
