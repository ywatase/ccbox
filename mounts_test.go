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

func TestValidateMount_isolationDenylist(t *testing.T) {
	// host 側の isolation denylist に触れる mount を拒否する。実在するパスとして
	// 一時ホーム配下に .ssh 等を作成し、それを host に指定した場合に拒否される。
	home := t.TempDir()
	for _, denied := range []string{".ssh", ".gnupg", ".aws", ".claude", ".codex", ".gitconfig"} {
		target := filepath.Join(home, denied)
		if denied == ".gitconfig" {
			if err := os.WriteFile(target, []byte(""), 0600); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.MkdirAll(target, 0700); err != nil {
				t.Fatal(err)
			}
		}
		t.Run("source "+denied, func(t *testing.T) {
			err := validateMount(Mount{Host: target}, home)
			if err == nil || !strings.Contains(err.Error(), "isolationDenylist") {
				t.Errorf("validateMount(%q) err = %v, want isolationDenylist reject", target, err)
			}
		})
	}
}

func TestValidateMount_isolationDenylist_subpath(t *testing.T) {
	// denylist ディレクトリ配下（~/.ssh/subdir）も拒否
	home := t.TempDir()
	sub := filepath.Join(home, ".ssh", "keys")
	if err := os.MkdirAll(sub, 0700); err != nil {
		t.Fatal(err)
	}
	err := validateMount(Mount{Host: sub}, home)
	if err == nil || !strings.Contains(err.Error(), "isolationDenylist") {
		t.Errorf("~/.ssh 配下も拒否すべき: err=%v", err)
	}
}

func TestValidateMount_isolationDenylist_viaSymlink(t *testing.T) {
	// シンボリックリンク経由で denylist に解決される場合も拒否
	home := t.TempDir()
	secret := filepath.Join(home, ".ssh", "id_rsa")
	if err := os.MkdirAll(filepath.Dir(secret), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("SECRET"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "innocuous")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	err := validateMount(Mount{Host: link}, home)
	if err == nil || !strings.Contains(err.Error(), "isolationDenylist") {
		t.Errorf("symlink 経由 denylist を拒否すべき: err=%v", err)
	}
}

func TestValidateMount_ancestorContainingDenylist(t *testing.T) {
	// source を denylist の項目そのものではなく、その項目を配下に含む祖先ディレクトリに
	// 指定した場合も拒否する（~/.config → 配下に .config/gh がある）。
	home := t.TempDir()
	// .config/gh を作成 → .config 全体を mount すると gh も露出する
	if err := os.MkdirAll(filepath.Join(home, ".config", "gh"), 0755); err != nil {
		t.Fatal(err)
	}

	err := validateMount(Mount{Host: filepath.Join(home, ".config")}, home)
	if err == nil {
		t.Fatal("~/.config の mount が許可された（.config/gh を配下に含むので拒否すべき）")
	}
	if !strings.Contains(err.Error(), "配下に") {
		t.Errorf("エラー文言に「配下に」が含まれない: %v", err)
	}
}

func TestValidateMount_ancestorContainingDenylist_viaSymlink(t *testing.T) {
	// シンボリックリンク経由で祖先を指した場合も拒否する。
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "gh"), 0755); err != nil {
		t.Fatal(err)
	}
	// ~/link → ~/.config （innocent 名で denylist 祖先を指す）
	link := filepath.Join(home, "link")
	if err := os.Symlink(filepath.Join(home, ".config"), link); err != nil {
		t.Fatal(err)
	}
	err := validateMount(Mount{Host: link}, home)
	if err == nil {
		t.Fatal("symlink 経由の祖先 mount が許可された")
	}
	if !strings.Contains(err.Error(), "配下に") {
		t.Errorf("エラー文言に「配下に」が含まれない: %v", err)
	}
}

func TestValidateMount_ancestorNotContainingDenylist_allowed(t *testing.T) {
	// 名前が denylist の祖先っぽくても、実際に denylist 項目を配下に持たなければ許可。
	home := t.TempDir()
	// .config/nvim だけ作る（.config/gh は作らない）
	sub := filepath.Join(home, ".config", "nvim")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	// ~/.config/nvim を mount → denylist を配下に含まない → 許可
	if err := validateMount(Mount{Host: sub}, home); err != nil {
		t.Errorf("denylist を含まない ~/.config/nvim が拒否された: %v", err)
	}
}

func TestIsolationDenylistCoveredBy(t *testing.T) {
	// 単体判定のテスト。EvalSymlinks 前提だが t.TempDir 直下で構築するので symlink 経由は別テスト。
	tests := []struct {
		name string
		host string // home からの相対
		want bool
	}{
		{".config は gh を含むので拒否", ".config", true},
		{".config/nvim は含まないので許可", ".config/nvim", false},
		{"workspace は無関係なので許可", "workspace", false},
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "gh"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isolationDenylistCoveredBy(filepath.Join(home, tt.host), home)
			if got != tt.want {
				t.Errorf("isolationDenylistCoveredBy(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestValidateMount_containerHomeReserved(t *testing.T) {
	home := t.TempDir()
	sub := filepath.Join(home, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		container string
		wantErr   string
	}{
		{"/home/ccbox そのもの", "/home/ccbox", "コンテナホームそのもの"},
		{"/home/ccbox/.ssh", "/home/ccbox/.ssh", "予約領域"},
		{"/home/ccbox/foo", "/home/ccbox/foo", "予約領域"},
		{"/home/ccbox/.ccbox/home（Path Traversal 風）",
			"/home/ccbox/.ccbox/home", "予約領域"},
		{"末尾スラッシュ", "/home/ccbox/", "コンテナホームそのもの"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMount(Mount{Host: sub, Container: tt.container}, home)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want contains %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateMount_containerHomeReserved_defaultFromHost(t *testing.T) {
	// container 省略時は host と同じ絶対パスに mount するため、host が /home/ccbox 配下でも拒否
	home := t.TempDir()
	// host = /home/ccbox/... のようなパスを作れないため、代わりに絶対 /home/ccbox を host に指定
	err := validateMount(Mount{Host: "/home/ccbox"}, home)
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	// container 側の検査で先に落ちる or host が存在しないで落ちる、どちらでも denylist 実現
}

func TestValidateMount_allowsRegularPath(t *testing.T) {
	home := t.TempDir()
	sub := filepath.Join(home, "workspace")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	// container 省略、通常パスは許可
	if err := validateMount(Mount{Host: sub}, home); err != nil {
		t.Errorf("通常パスが拒否された: %v", err)
	}
	// container 明示、/home/ccbox から離れた場所は許可
	if err := validateMount(Mount{Host: sub, Container: "/opt/data"}, home); err != nil {
		t.Errorf("/opt/data への mount が拒否された: %v", err)
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
