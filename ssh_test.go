package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectSlug(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/Users/foo/git/myapp", "myapp"},
		{"/Users/foo/My App_2", "my-app-2"},
		{"/Users/foo/日本語дир", "project"},
		{"/Users/foo/-weird--name-", "weird-name"},
		{"/Users/foo/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "aaaaaaaaaaaaaaaaaaaa"}, // 20文字に切る
	}
	for _, tt := range tests {
		if got := projectSlug(tt.path); got != tt.want {
			t.Errorf("projectSlug(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestContainerNameDeterministic(t *testing.T) {
	a := containerName("/Users/foo/myapp")
	b := containerName("/Users/foo/myapp")
	if a != b {
		t.Errorf("containerName は決定的であるべき: %q != %q", a, b)
	}
	c := containerName("/Users/bar/myapp")
	if a == c {
		t.Errorf("パスが違えば containerName も変わるべき: %q == %q", a, c)
	}
	if len(projectHash("/x")) != 8 {
		t.Errorf("projectHash は8文字: got %q", projectHash("/x"))
	}
}

func TestHostAlias(t *testing.T) {
	if got := hostAlias("/Users/foo/myapp"); got != "ccbox-myapp" {
		t.Errorf("hostAlias = %q, want ccbox-myapp", got)
	}
}

func TestRenderHostEntry(t *testing.T) {
	got := renderHostEntry("ccbox-myapp", "/usr/local/bin/ccbox", "/Users/foo/my app")
	for _, want := range []string{
		"Host ccbox-myapp\n",
		"  User ccbox\n",
		"  IdentityFile ~/.ccbox/ssh/id_ed25519\n",
		"  UserKnownHostsFile ~/.ccbox/ssh/known_hosts\n",
		"  StrictHostKeyChecking yes\n",
		"  ForwardAgent no\n",
		"  ForwardX11 no\n",
		"  ControlMaster no\n",
		"  ControlPath none\n",
		`  ProxyCommand "/usr/local/bin/ccbox" ssh-proxy "/Users/foo/my app"` + "\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("renderHostEntry に %q が含まれない:\n%s", want, got)
		}
	}
}

func TestValidateSSHProjectPath(t *testing.T) {
	tests := []struct {
		path   string
		wantOK bool
	}{
		{"/Users/foo/myapp", true},
		{"/Users/foo/my app", true},
		{"/Users/foo/a$(rm -rf ~)", false},
		{"/Users/foo/a`id`", false},
		{"/Users/foo/a\"b", false},
		{"/Users/foo/a\\b", false},
		{"/Users/foo/a\nHost evil", false},
	}
	for _, tt := range tests {
		err := validateSSHProjectPath(tt.path)
		if tt.wantOK && err != nil {
			t.Errorf("validateSSHProjectPath(%q) = %v, want nil", tt.path, err)
		}
		if !tt.wantOK && err == nil {
			t.Errorf("validateSSHProjectPath(%q) = nil, want error", tt.path)
		}
	}
}

func TestUpsertHostEntry(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")

	// 新規追加
	e1 := renderHostEntry("ccbox-myapp", "/bin/ccbox", "/p/myapp")
	alias, err := upsertHostEntry(cfg, "ccbox-myapp", "/p/myapp", e1)
	if err != nil || alias != "ccbox-myapp" {
		t.Fatalf("新規追加に失敗: alias=%q err=%v", alias, err)
	}

	// 同一パスの再登録は置換（重複しない）
	if _, err := upsertHostEntry(cfg, "ccbox-myapp", "/p/myapp", e1); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg)
	if strings.Count(string(data), "Host ccbox-myapp\n") != 1 {
		t.Errorf("再登録でエントリが重複した:\n%s", data)
	}

	// 別パスで同名 → containerName にフォールバック
	e2 := renderHostEntry("ccbox-myapp", "/bin/ccbox", "/q/myapp")
	alias2, err := upsertHostEntry(cfg, "ccbox-myapp", "/q/myapp", e2)
	if err != nil {
		t.Fatal(err)
	}
	if alias2 != containerName("/q/myapp") {
		t.Errorf("衝突時は containerName へ: got %q want %q", alias2, containerName("/q/myapp"))
	}
}

func TestIncludeManagement(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")

	// ファイルが無い場合は false
	has, err := sshConfigHasInclude(cfg)
	if err != nil || has {
		t.Fatalf("空状態: has=%v err=%v", has, err)
	}

	// prepend で先頭に入る
	os.WriteFile(cfg, []byte("Host example\n  User foo\n"), 0600)
	if err := prependInclude(cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg)
	if !strings.HasPrefix(string(data), "Include ~/.ccbox/ssh/config\n") {
		t.Errorf("Include が先頭にない:\n%s", data)
	}
	if !strings.Contains(string(data), "Host example") {
		t.Errorf("既存内容が消えた:\n%s", data)
	}
	has, _ = sshConfigHasInclude(cfg)
	if !has {
		t.Error("prepend 後に検出できない")
	}
}

func TestEnsureSSHAssets(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen が無い環境")
	}
	home := t.TempDir()
	if err := ensureSSHAssets(home); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		".ccbox/ssh/id_ed25519", ".ccbox/ssh/id_ed25519.pub", ".ccbox/ssh/known_hosts",
		".ccbox/home/.ssh/host_ed25519", ".ccbox/home/.ssh/authorized_keys",
		".ccbox/home/.ssh/sshd_config",
		".ccbox/home/.codex/config.toml",
	} {
		if _, err := os.Stat(filepath.Join(home, rel)); err != nil {
			t.Errorf("%s が生成されていない: %v", rel, err)
		}
	}

	// known_hosts は ccbox-* パターン + ホスト公開鍵
	kh, _ := os.ReadFile(filepath.Join(home, ".ccbox/ssh/known_hosts"))
	if !strings.HasPrefix(string(kh), "ccbox-* ssh-ed25519 ") {
		t.Errorf("known_hosts の形式が不正: %q", kh)
	}

	// sshd_config は PoC で動作検証済みの内容と一字一句同じであること（セキュリティ設定の退行防止）
	sc, _ := os.ReadFile(filepath.Join(home, ".ccbox/home/.ssh/sshd_config"))
	if string(sc) != sshdConfig {
		t.Errorf("sshd_config の内容が定数と一致しない:\n%s", sc)
	}

	// authorized_keys はクライアント公開鍵と一致
	pub, _ := os.ReadFile(filepath.Join(home, ".ccbox/ssh/id_ed25519.pub"))
	ak, _ := os.ReadFile(filepath.Join(home, ".ccbox/home/.ssh/authorized_keys"))
	if string(pub) != string(ak) {
		t.Error("authorized_keys がクライアント公開鍵と一致しない")
	}

	// sandbox 設定が seed される
	toml, _ := os.ReadFile(filepath.Join(home, ".ccbox/home/.codex/config.toml"))
	if !strings.Contains(string(toml), `sandbox_mode = "danger-full-access"`) {
		t.Errorf("codex config が不正: %q", toml)
	}

	// 冪等: 2回目で鍵が変わらない
	before, _ := os.ReadFile(filepath.Join(home, ".ccbox/ssh/id_ed25519"))
	if err := ensureSSHAssets(home); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(home, ".ccbox/ssh/id_ed25519"))
	if string(before) != string(after) {
		t.Error("2回目の実行で鍵が再生成された")
	}

	// 既存の config.toml は上書きしない
	custom := filepath.Join(home, ".ccbox/home/.codex/config.toml")
	os.WriteFile(custom, []byte("# user custom\n"), 0600)
	ensureSSHAssets(home)
	got, _ := os.ReadFile(custom)
	if string(got) != "# user custom\n" {
		t.Error("ユーザーの config.toml が上書きされた")
	}
}
