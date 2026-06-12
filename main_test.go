package main

import (
	"os"
	"testing"
)

func TestParseArgs_subcommands(t *testing.T) {
	tests := []struct {
		args    []string
		want    string
		wantLen int
	}{
		{[]string{}, "claude", 0},
		{[]string{"build"}, "build", 0},
		{[]string{"update"}, "update", 0},
		{[]string{"shell"}, "shell", 0},
		{[]string{"version"}, "version", 0},
		{[]string{"help"}, "help", 0},
		{[]string{"-h"}, "help", 0},
		{[]string{"--help"}, "help", 0},
	}
	for _, tt := range tests {
		r := parseArgs(tt.args)
		if r.subcommand != tt.want {
			t.Errorf("parseArgs(%v).subcommand = %q, want %q", tt.args, r.subcommand, tt.want)
		}
		if len(r.claudeArgs) != tt.wantLen {
			t.Errorf("parseArgs(%v).claudeArgs len = %d, want %d", tt.args, len(r.claudeArgs), tt.wantLen)
		}
	}
}

func TestParseArgs_claudeArgs(t *testing.T) {
	tests := []struct {
		args      []string
		wantCmd   string
		wantArgs  []string
		wantForce bool
	}{
		{
			args:      []string{"--chat", "hello"},
			wantCmd:   "claude",
			wantArgs:  []string{"--chat", "hello"},
			wantForce: false,
		},
		{
			args:      []string{"--", "--chat", "hello"},
			wantCmd:   "claude",
			wantArgs:  []string{"--chat", "hello"},
			wantForce: true,
		},
		{
			// build に見えても -- で区切られていればすべて claude に渡る
			args:      []string{"--", "build"},
			wantCmd:   "claude",
			wantArgs:  []string{"build"},
			wantForce: true,
		},
	}
	for _, tt := range tests {
		r := parseArgs(tt.args)
		if r.subcommand != tt.wantCmd {
			t.Errorf("parseArgs(%v).subcommand = %q, want %q", tt.args, r.subcommand, tt.wantCmd)
		}
		if len(r.claudeArgs) != len(tt.wantArgs) {
			t.Errorf("parseArgs(%v).claudeArgs = %v, want %v", tt.args, r.claudeArgs, tt.wantArgs)
			continue
		}
		for i, a := range r.claudeArgs {
			if a != tt.wantArgs[i] {
				t.Errorf("parseArgs(%v).claudeArgs[%d] = %q, want %q", tt.args, i, a, tt.wantArgs[i])
			}
		}
		if r.forceClaude != tt.wantForce {
			t.Errorf("parseArgs(%v).forceClaude = %v, want %v", tt.args, r.forceClaude, tt.wantForce)
		}
	}
}

func TestIsTerminalFile_notTTY(t *testing.T) {
	// /dev/null はキャラクタデバイスだが端末ではないので false でなければならない。
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip("os.DevNull を開けません:", err)
	}
	defer devNull.Close()
	if isTerminalFile(devNull) {
		t.Error("isTerminalFile(/dev/null) = true, want false")
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if isTerminalFile(r) {
		t.Error("isTerminalFile(pipe) = true, want false")
	}
}

func TestDockerfileContent(t *testing.T) {
	r := dockerfileContent()
	if r == nil {
		t.Fatal("dockerfileContent() returned nil")
	}
	// embed が機能していれば FROM node:22 が含まれるはず。
	buf := make([]byte, 512)
	n, _ := r.Read(buf)
	if n == 0 {
		t.Fatal("Dockerfile content is empty")
	}
	content := string(buf[:n])
	if len(content) < 10 {
		t.Errorf("Dockerfile content too short: %q", content)
	}
}
