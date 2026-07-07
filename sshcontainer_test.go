package main

import (
	"slices"
	"strings"
	"testing"
)

func TestBuildPersistentRunArgs(t *testing.T) {
	got := buildPersistentRunArgs("ccbox-myapp-abc12345", "/Users/foo/.ccbox/home", "/Users/foo/myapp")

	for _, want := range [][]string{
		{"run", "-d", "--name", "ccbox-myapp-abc12345"},
		{"--label", "ccbox.managed=true"},
		{"--label", "ccbox.project=/Users/foo/myapp"},
		{"--init"},
		{"--security-opt", "no-new-privileges"},
		{"--pids-limit", "1024"},
		{"-v", "/Users/foo/.ccbox/home:/home/ccbox"},
		{"-v", "/Users/foo/myapp:/Users/foo/myapp"},
		{"-w", "/Users/foo/myapp"},
		{"--mount", "type=tmpfs,destination=/home/ccbox/.claude/remote/run,tmpfs-mode=0700"},
		{"--mount", "type=tmpfs,destination=/home/ccbox/.codex/app-server-control,tmpfs-mode=0700"},
		{imageTag, "sleep", "infinity"},
	} {
		if !containsSubsequence(got, want) {
			t.Errorf("引数列に %v が含まれない:\n%v", want, got)
		}
	}
}

// containsSubsequence は want が got 内に連続して現れるか調べる。
func containsSubsequence(got, want []string) bool {
	for i := 0; i+len(want) <= len(got); i++ {
		if slices.Equal(got[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

func TestChownArgs(t *testing.T) {
	got := chownArgs("ccbox-x-11112222")
	want := []string{"exec", "-u", "root", "ccbox-x-11112222",
		"chown", "ccbox:", "/home/ccbox/.claude/remote/run", "/home/ccbox/.codex/app-server-control"}
	if !slices.Equal(got, want) {
		t.Errorf("chownArgs = %v, want %v", got, want)
	}
}

func TestSSHProxyExecArgs(t *testing.T) {
	got := sshProxyExecArgs("ccbox-x-11112222")
	want := []string{"exec", "-i", "ccbox-x-11112222",
		"/usr/sbin/sshd", "-i", "-f", "/home/ccbox/.ssh/sshd_config"}
	if !slices.Equal(got, want) {
		t.Errorf("sshProxyExecArgs = %v, want %v", got, want)
	}
}

func TestPsAndDownArgs(t *testing.T) {
	if got := psArgs(); !strings.Contains(strings.Join(got, " "),
		"ps -a --filter label=ccbox.managed=true") {
		t.Errorf("psArgs = %v", got)
	}
	if got := downArgs("ccbox-x-11112222"); !slices.Equal(got,
		[]string{"rm", "-f", "ccbox-x-11112222"}) {
		t.Errorf("downArgs = %v", got)
	}
}
