package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

func TestIsUnsafeMountDir(t *testing.T) {
	// ホームを含むディレクトリを mount すると認証情報が読めるため、祖先だけを危険扱いする。
	home := filepath.Join("/", "Users", "x")
	tests := []struct {
		name string
		pwd  string
		home string
		want bool
	}{
		{
			name: "pwd が home と同じ",
			pwd:  home,
			home: home,
			want: true,
		},
		{
			name: "pwd が home の親",
			pwd:  filepath.Join("/", "Users"),
			home: home,
			want: true,
		},
		{
			name: "pwd がルート",
			pwd:  string(filepath.Separator),
			home: home,
			want: true,
		},
		{
			name: "pwd が home 配下",
			pwd:  filepath.Join(home, "project"),
			home: home,
			want: false,
		},
		{
			name: "pwd が home と無関係",
			pwd:  filepath.Join("/", "tmp"),
			home: home,
			want: false,
		},
	}
	for _, tt := range tests {
		got := isUnsafeMountDir(tt.pwd, tt.home)
		if got != tt.want {
			t.Errorf("%s: isUnsafeMountDir(%q, %q) = %v, want %v", tt.name, tt.pwd, tt.home, got, tt.want)
		}
	}
}

func TestBuildRunArgs_ttyMode(t *testing.T) {
	// TTY モードでは -t と --sig-proxy=false が必須。
	// sig-proxy が有効のままだとホスト側 SIGIO が転送されてコンテナ内プロセスが
	// exit 157 で即死する（macOS + colima で実発生）。
	args := buildRunArgs("bash", nil, "/h/.ccbox/home", "/work", "xterm", true)
	if !slices.Contains(args, "-t") {
		t.Error("TTY モードで -t が付与されていない")
	}
	if !slices.Contains(args, "--sig-proxy=false") {
		t.Error("TTY モードで --sig-proxy=false が付与されていない")
	}
	if !slices.Contains(args, "--security-opt") {
		t.Error("TTY モードで --security-opt が付与されていない")
	}
	if !slices.Contains(args, "no-new-privileges") {
		t.Error("TTY モードで no-new-privileges が付与されていない")
	}
	if !slices.Contains(args, "--pids-limit") {
		t.Error("TTY モードで --pids-limit が付与されていない")
	}
	if !slices.Contains(args, "1024") {
		t.Error("TTY モードで pids limit 1024 が付与されていない")
	}
}

func TestBuildRunArgs_nonTTYMode(t *testing.T) {
	// 非 TTY モードでは -t を付けず（docker が "not a TTY" で失敗するため）、
	// Ctrl+C 転送に sig-proxy が必要なので --sig-proxy=false も付けない。
	args := buildRunArgs("claude", []string{"-p", "hi"}, "/h/.ccbox/home", "/work", "xterm", false)
	if slices.Contains(args, "-t") {
		t.Error("非 TTY モードで -t が付与されている")
	}
	if slices.Contains(args, "--sig-proxy=false") {
		t.Error("非 TTY モードで --sig-proxy=false が付与されている")
	}
	if !slices.Contains(args, "--security-opt") {
		t.Error("非 TTY モードで --security-opt が付与されていない")
	}
	if !slices.Contains(args, "no-new-privileges") {
		t.Error("非 TTY モードで no-new-privileges が付与されていない")
	}
	if !slices.Contains(args, "--pids-limit") {
		t.Error("非 TTY モードで --pids-limit が付与されていない")
	}
	if !slices.Contains(args, "1024") {
		t.Error("非 TTY モードで pids limit 1024 が付与されていない")
	}
	// extraArgs は末尾に連結される。
	if args[len(args)-2] != "-p" || args[len(args)-1] != "hi" {
		t.Errorf("extraArgs が末尾に連結されていない: %v", args)
	}
}

func TestRunContainerRejectsColonInPwd(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "has:colon")
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(work, 0755); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	currentWork, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatalf("作業ディレクトリを戻せません: %v", err)
		}
	})
	t.Setenv("HOME", home)
	t.Setenv("PATH", root)

	err = runContainer("bash", nil)
	if err == nil {
		t.Fatal("runContainer() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "パスに ':' が含まれるため docker の -v 構文で安全にマウントできません") {
		t.Errorf("エラーメッセージに理由が含まれていない: %v", err)
	}
	if !strings.Contains(err.Error(), currentWork) {
		t.Errorf("エラーメッセージに対象パスが含まれていない: %v", err)
	}
}

func TestRunContainerRejectsColonInCcboxHome(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	home := filepath.Join(root, "home:colon")
	if err := os.MkdirAll(work, 0755); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatalf("作業ディレクトリを戻せません: %v", err)
		}
	})
	t.Setenv("HOME", home)
	t.Setenv("PATH", root)

	err = runContainer("bash", nil)
	if err == nil {
		t.Fatal("runContainer() error = nil, want error")
	}
	wantPath := filepath.Join(home, ".ccbox", "home")
	if !strings.Contains(err.Error(), "パスに ':' が含まれるため docker の -v 構文で安全にマウントできません") {
		t.Errorf("エラーメッセージに理由が含まれていない: %v", err)
	}
	if !strings.Contains(err.Error(), wantPath) {
		t.Errorf("エラーメッセージに対象パスが含まれていない: %v", err)
	}
}

func TestParseArgs_newSubcommands(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want dispatchResult
	}{
		{"codex 単体", []string{"codex"},
			dispatchResult{subcommand: "codex", claudeArgs: []string{}}},
		{"codex 引数付き", []string{"codex", "レビューして"},
			dispatchResult{subcommand: "codex", claudeArgs: []string{"レビューして"}}},
		{"ssh", []string{"ssh"},
			dispatchResult{subcommand: "ssh"}},
		{"ssh-proxy パス付き", []string{"ssh-proxy", "/path/to/proj"},
			dispatchResult{subcommand: "ssh-proxy", claudeArgs: []string{"/path/to/proj"}}},
		{"ps", []string{"ps"},
			dispatchResult{subcommand: "ps"}},
		{"down パスなし", []string{"down"},
			dispatchResult{subcommand: "down", claudeArgs: []string{}}},
		{"down パス付き", []string{"down", "/path/to/proj"},
			dispatchResult{subcommand: "down", claudeArgs: []string{"/path/to/proj"}}},
		{"-- codex は claude に渡る", []string{"--", "codex"},
			dispatchResult{subcommand: "claude", claudeArgs: []string{"codex"}, forceClaude: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseArgs(tt.args)
			if got.subcommand != tt.want.subcommand ||
				!slices.Equal(got.claudeArgs, tt.want.claudeArgs) ||
				got.forceClaude != tt.want.forceClaude {
				t.Errorf("parseArgs(%v) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
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
