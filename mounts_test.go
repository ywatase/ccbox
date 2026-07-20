package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadMounts_absent(t *testing.T) {
	home := t.TempDir()
	cfg, err := loadMounts(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Mounts) != 0 {
		t.Errorf("空でない: %v", cfg.Mounts)
	}
}

func TestSaveAndLoadMounts_roundtrip(t *testing.T) {
	home := t.TempDir()
	orig := &MountsConfig{
		Mounts: []Mount{
			{Host: "/tmp/a", Container: "/opt/a", Readonly: false},
			{Host: "/tmp/b", Readonly: true},
		},
	}
	if err := saveMounts(home, orig); err != nil {
		t.Fatal(err)
	}
	got, err := loadMounts(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Mounts) != len(orig.Mounts) {
		t.Fatalf("len mismatch: got %d want %d", len(got.Mounts), len(orig.Mounts))
	}
	for i, m := range got.Mounts {
		if m != orig.Mounts[i] {
			t.Errorf("[%d] got=%+v want=%+v", i, m, orig.Mounts[i])
		}
	}
	// ファイル権限が 0600 か
	info, err := os.Stat(mountsConfigPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("projects.yaml の権限 = %v, want 0600", info.Mode().Perm())
	}
}

func TestMountsConfig_addOrUpdate(t *testing.T) {
	c := &MountsConfig{}
	c.addOrUpdate(Mount{Host: "/a"})
	c.addOrUpdate(Mount{Host: "/b", Readonly: true})
	c.addOrUpdate(Mount{Host: "/a", Container: "/x", Readonly: true}) // /a を上書き
	if len(c.Mounts) != 2 {
		t.Fatalf("len = %d, want 2", len(c.Mounts))
	}
	if c.Mounts[0].Container != "/x" || !c.Mounts[0].Readonly {
		t.Errorf("/a が上書きされていない: %+v", c.Mounts[0])
	}
}

func TestMountsConfig_remove(t *testing.T) {
	c := &MountsConfig{Mounts: []Mount{
		{Host: "/a"}, {Host: "/b"}, {Host: "/c"},
	}}
	if !c.remove("/b") {
		t.Fatal("remove(/b) = false")
	}
	if c.remove("/nonexistent") {
		t.Fatal("存在しないエントリで true が返った")
	}
	if len(c.Mounts) != 2 || c.Mounts[0].Host != "/a" || c.Mounts[1].Host != "/c" {
		t.Errorf("remove 後の順序が不正: %+v", c.Mounts)
	}
}

func TestValidateMount(t *testing.T) {
	home := t.TempDir()
	// 実在するパスを 1 つ用意
	sub := filepath.Join(home, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		m       Mount
		wantErr string
	}{
		{"host 空",
			Mount{},
			"host は必須"},
		{"host 相対",
			Mount{Host: "rel/path"},
			"絶対パス"},
		{"host コロン含む",
			Mount{Host: "/tmp/has:colon"},
			":' が含まれる"},
		{"container 相対",
			Mount{Host: sub, Container: "rel"},
			"container は絶対パス"},
		{"container コロン含む",
			Mount{Host: sub, Container: "/opt:x"},
			"container に ':'"},
		{"存在しない host",
			Mount{Host: "/nonexistent/path"},
			"存在しないか"},
		{"home そのものは拒否",
			Mount{Host: home},
			"ホームディレクトリ全体"},
		{"正常",
			Mount{Host: sub},
			""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMount(tt.m, home)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("err = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want contains %q", err, tt.wantErr)
			}
		})
	}
}

func TestMountToDockerArg(t *testing.T) {
	tests := []struct {
		m    Mount
		want string
	}{
		{Mount{Host: "/a"}, "/a:/a"},
		{Mount{Host: "/a", Container: "/b"}, "/a:/b"},
		{Mount{Host: "/a", Readonly: true}, "/a:/a:ro"},
		{Mount{Host: "/a", Container: "/b", Readonly: true}, "/a:/b:ro"},
	}
	for _, tt := range tests {
		if got := mountToDockerArg(tt.m); got != tt.want {
			t.Errorf("mountToDockerArg(%+v) = %q, want %q", tt.m, got, tt.want)
		}
	}
}

func TestMountsToDockerArgs_skipsInvalid(t *testing.T) {
	home := t.TempDir()
	sub := filepath.Join(home, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &MountsConfig{Mounts: []Mount{
		{Host: sub},                 // 正常
		{Host: "/nonexistent/path"}, // 存在しない → スキップ
		{Host: home},                // home 露出 → スキップ
	}}
	args := mountsToDockerArgs(cfg, home)
	// 正常エントリだけが -v ペアで残る
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, sub+":"+sub) {
		t.Errorf("正常エントリが含まれていない: %v", args)
	}
	if strings.Contains(joined, "/nonexistent") {
		t.Errorf("存在しないエントリが含まれている: %v", args)
	}
	// スキップ後は -v <spec> が 1 組だけ
	if len(args) != 2 {
		t.Errorf("スキップ後の要素数 = %d, want 2 (1 エントリ = 2 要素): %v", len(args), args)
	}
}

func TestLoadExtraMountArgs(t *testing.T) {
	home := t.TempDir()
	// 何も無ければ空
	args, err := loadExtraMountArgs(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 0 {
		t.Errorf("初期状態で空でない: %v", args)
	}

	// エントリを追加
	sub := filepath.Join(home, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &MountsConfig{Mounts: []Mount{{Host: sub, Readonly: true}}}
	if err := saveMounts(home, cfg); err != nil {
		t.Fatal(err)
	}
	args, err = loadExtraMountArgs(home)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-v", sub + ":" + sub + ":ro"}
	if !slices.Equal(args, want) {
		t.Errorf("got=%v want=%v", args, want)
	}
}

func TestBuildPersistentRunArgs_includesExtraMounts(t *testing.T) {
	extra := []string{"-v", "/a:/a", "-v", "/b:/b:ro"}
	args := buildPersistentRunArgs("ccbox-x-11112222", "/h/.ccbox/home", "/work", nil, extra)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "/a:/a") || !strings.Contains(joined, "/b:/b:ro") {
		t.Errorf("extraMounts が反映されていない: %v", args)
	}
}

func TestParseArgs_mount(t *testing.T) {
	r := parseArgs([]string{"mount", "add", "/tmp/x", "--ro"})
	if r.subcommand != "mount" {
		t.Errorf("subcommand = %q, want mount", r.subcommand)
	}
	if !slices.Equal(r.claudeArgs, []string{"add", "/tmp/x", "--ro"}) {
		t.Errorf("claudeArgs = %v", r.claudeArgs)
	}
}
