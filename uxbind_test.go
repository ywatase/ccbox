package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestUXBindMountArgs_absent(t *testing.T) {
	home := t.TempDir()
	// .tmux.conf を作らない → スキップされて空リストが返る
	got := uxBindMountArgs(home, []string{".tmux.conf"})
	if len(got) != 0 {
		t.Errorf("want empty, got %v", got)
	}
}

func TestUXBindMountArgs_present(t *testing.T) {
	home := t.TempDir()
	tmux := filepath.Join(home, ".tmux.conf")
	if err := os.WriteFile(tmux, []byte("set -g mouse on\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := uxBindMountArgs(home, []string{".tmux.conf"})
	want := []string{"-v", tmux + ":/home/ccbox/.tmux.conf:ro"}
	if !slices.Equal(got, want) {
		t.Errorf("got=%v want=%v", got, want)
	}
}

func TestUXBindMountArgs_directorySkipped(t *testing.T) {
	home := t.TempDir()
	// .tmux.conf をディレクトリとして作成 → 個別ファイルのみ許可なのでスキップ
	if err := os.MkdirAll(filepath.Join(home, ".tmux.conf"), 0755); err != nil {
		t.Fatal(err)
	}
	got := uxBindMountArgs(home, []string{".tmux.conf"})
	if len(got) != 0 {
		t.Errorf("ディレクトリは bind mount しないはず: %v", got)
	}
}

func TestUXBindMountArgs_symlinkToDeniedRejected(t *testing.T) {
	// ホワイトリスト外のファイル名でも、シンボリックリンクの解決先が禁止リスト内なら拒否。
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(sshDir, "id_rsa")
	if err := os.WriteFile(secret, []byte("SECRET"), 0600); err != nil {
		t.Fatal(err)
	}
	// .tmux.conf を .ssh/id_rsa へのシンボリックリンクとして作成（攻撃を模擬）
	link := filepath.Join(home, ".tmux.conf")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}

	got := uxBindMountArgs(home, []string{".tmux.conf"})
	if len(got) != 0 {
		t.Errorf("symlink 経由の禁止パスは拒否すべき: %v", got)
	}
}

func TestUXBindMountArgs_symlinkToSafePathAllowed(t *testing.T) {
	// 禁止リスト外へのシンボリックリンクは許可される。
	home := t.TempDir()
	safeDir := filepath.Join(home, "dotfiles")
	if err := os.MkdirAll(safeDir, 0755); err != nil {
		t.Fatal(err)
	}
	safeFile := filepath.Join(safeDir, "tmux.conf")
	if err := os.WriteFile(safeFile, []byte("set -g mouse on\n"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".tmux.conf")
	if err := os.Symlink(safeFile, link); err != nil {
		t.Fatal(err)
	}

	got := uxBindMountArgs(home, []string{".tmux.conf"})
	want := []string{"-v", link + ":/home/ccbox/.tmux.conf:ro"}
	if !slices.Equal(got, want) {
		t.Errorf("got=%v want=%v", got, want)
	}
}

func TestIsWithin(t *testing.T) {
	// 素朴な strings.HasPrefix(rel, "..") だと base/..name を「外」と誤判定して
	// 封じ込めが破れる。".." 完全一致と ".." + セパレータのみを外側として扱う。
	tests := []struct {
		name   string
		base   string
		target string
		want   bool
	}{
		{"直下のファイル", "/a/b", "/a/b/c", true},
		{"深い階層", "/a/b", "/a/b/c/d/e", true},
		{"'..' で始まる名前も配下", "/a/b", "/a/b/..secret", true},
		{"'..' で始まる名前の深い階層", "/a/b", "/a/b/..d/e", true},
		{"'...' で始まる名前も配下", "/a/b", "/a/b/...x", true},
		{"base 自身は配下でない", "/a/b", "/a/b", false},
		{"親は配下でない", "/a/b", "/a", false},
		{"祖先は配下でない", "/a/b/c", "/a", false},
		{"兄弟は配下でない", "/a/b", "/a/c", false},
		{"名前が前方一致する兄弟は配下でない", "/a/b", "/a/bc", false},
		{"無関係", "/a/b", "/x/y", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWithin(tt.base, tt.target); got != tt.want {
				t.Errorf("isWithin(%q, %q) = %v, want %v", tt.base, tt.target, got, tt.want)
			}
		})
	}
}

func TestIsDeniedUXPath_dotdotPrefixedNameInsideDenylist(t *testing.T) {
	// denylist ディレクトリ配下で名前が ".." で始まるものも封じ込める。
	// 旧実装は filepath.Rel の結果 "..secret" を HasPrefix(rel, "..") で
	// 「~/.ssh の外」と誤判定し、bind mount を通してしまっていた。
	home := "/Users/x"
	for _, p := range []string{
		filepath.Join(home, ".ssh", "..secret"),
		filepath.Join(home, ".ssh", "..d", "key"),
		filepath.Join(home, ".gnupg", "..x"),
		filepath.Join(home, ".config", "gh", "..y"),
	} {
		if !isDeniedUXPath(p, home) {
			t.Errorf("isDeniedUXPath(%q) = false, want true（denylist 配下）", p)
		}
	}
}

func TestUXBindMountArgs_symlinkToDotdotNameInDenylistRejected(t *testing.T) {
	// end-to-end: ~/.tmux.conf → ~/.ssh/..secret が bind mount されないこと。
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(sshDir, "..secret")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(home, ".tmux.conf")); err != nil {
		t.Fatal(err)
	}
	if got := uxBindMountArgs(home, []string{".tmux.conf"}); len(got) != 0 {
		t.Errorf("~/.ssh/..secret への symlink が bind mount された: %v", got)
	}
}

// newSymlinkedDenylistHome は denylist 項目自体（~/.ssh）が home 外の実体への
// シンボリックリンクになっている構成を作る。実体は home を配下に含まない場所に置き、
// isUnsafeMountDir に先に捕まらないようにして denylist 判定そのものを検証する。
func newSymlinkedDenylistHome(t *testing.T) (home, realSSH string) {
	t.Helper()
	root := t.TempDir()
	home = filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0755); err != nil {
		t.Fatal(err)
	}
	realSSH = filepath.Join(root, "vault", "real-ssh")
	if err := os.MkdirAll(realSSH, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realSSH, "id_rsa"), []byte("PRIVATE KEY"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realSSH, filepath.Join(home, ".ssh")); err != nil {
		t.Fatal(err)
	}
	return home, realSSH
}

func TestIsDeniedUXPath_denylistItemIsSymlink(t *testing.T) {
	// denylist 項目自体が symlink（~/.ssh -> /vault/real-ssh）の場合、
	// 候補パスは実体に解決されるため、字句上のパスとだけ比較すると対応が失われる。
	// 字句・実体の両方を拒否対象に含めることで突き合わせる。
	home, realSSH := newSymlinkedDenylistHome(t)
	for _, p := range []string{
		realSSH,                               // 実体そのもの
		filepath.Join(realSSH, "id_rsa"),      // 実体の配下
		filepath.Join(home, ".ssh"),           // 字句上のパス
		filepath.Join(home, ".ssh", "id_rsa"), // 字句上のパスの配下
	} {
		if !isDeniedUXPath(p, home) {
			t.Errorf("isDeniedUXPath(%q) = false, want true", p)
		}
	}
}

func TestUXBindMountArgs_symlinkIntoSymlinkedDenylistRejected(t *testing.T) {
	// end-to-end: ~/.ssh 自体が外部 symlink でも、その配下へのリンクを bind しない。
	home, _ := newSymlinkedDenylistHome(t)
	if err := os.Symlink(filepath.Join(home, ".ssh", "id_rsa"), filepath.Join(home, ".tmux.conf")); err != nil {
		t.Fatal(err)
	}
	if got := uxBindMountArgs(home, []string{".tmux.conf"}); len(got) != 0 {
		t.Errorf("symlink 化された ~/.ssh 配下が bind mount された: %v", got)
	}
}

func TestDeniedAbsPaths_includesLexicalAndResolved(t *testing.T) {
	home, realSSH := newSymlinkedDenylistHome(t)
	got := deniedAbsPaths(home)
	// macOS では t.TempDir() の /var/... が EvalSymlinks で /private/var/... に
	// 正規化されるため、いずれかの表記で含まれていればよい。
	for _, w := range []string{filepath.Join(home, ".ssh"), realSSH} {
		found := false
		for _, v := range pathVariants(w) {
			if slices.Contains(got, v) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("deniedAbsPaths に %q（またはその実パス）が含まれない: %v", w, got)
		}
	}
	// 存在しない項目でも字句上のパスは残る（将来作られた時点で拒否するため）
	if !slices.Contains(got, filepath.Join(home, ".gnupg")) {
		t.Errorf("存在しない denylist 項目の字句パスが欠落: %v", got)
	}
	// 重複しない
	seen := map[string]int{}
	for _, p := range got {
		seen[p]++
		if seen[p] > 1 {
			t.Errorf("deniedAbsPaths に重複: %q", p)
		}
	}
}

func TestIsDeniedUXPath(t *testing.T) {
	home := "/Users/x"
	tests := []struct {
		name     string
		resolved string
		want     bool
	}{
		{"~/.ssh そのもの", filepath.Join(home, ".ssh"), true},
		{"~/.ssh/id_rsa", filepath.Join(home, ".ssh", "id_rsa"), true},
		{"~/.ssh 深い階層", filepath.Join(home, ".ssh", "keys", "prod"), true},
		{"~/.gnupg 配下", filepath.Join(home, ".gnupg", "secring.gpg"), true},
		{"~/.aws/credentials", filepath.Join(home, ".aws", "credentials"), true},
		{"~/.config/gh", filepath.Join(home, ".config", "gh"), true},
		{"~/.config/gh/hosts.yml", filepath.Join(home, ".config", "gh", "hosts.yml"), true},
		{"~/.config/glab-cli/config.yml", filepath.Join(home, ".config", "glab-cli", "config.yml"), true},
		{"~/.gitconfig", filepath.Join(home, ".gitconfig"), true},
		{"~/.claude 配下", filepath.Join(home, ".claude", "session.json"), true},
		{"~/.codex 配下", filepath.Join(home, ".codex", "config.toml"), true},
		{"~/.tmux.conf は安全", filepath.Join(home, ".tmux.conf"), false},
		{"~/dotfiles/tmux.conf は安全", filepath.Join(home, "dotfiles", "tmux.conf"), false},
		{"~/.config は安全（.config/gh 以外）", filepath.Join(home, ".config"), false},
		{"~/.config/other/foo は安全", filepath.Join(home, ".config", "other", "foo"), false},
		// 名前が似ているだけの偽陽性を避ける
		{"~/.gnupg-like は安全（.gnupg のプレフィックスマッチにしない）",
			filepath.Join(home, ".gnupg-like"), false},
		{"~/.sshd は安全（.ssh のプレフィックスマッチにしない）",
			filepath.Join(home, ".sshd"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDeniedUXPath(tt.resolved, home)
			if got != tt.want {
				t.Errorf("isDeniedUXPath(%q, %q) = %v, want %v",
					tt.resolved, home, got, tt.want)
			}
		})
	}
}

func TestBuildRunArgs_includesUXBinds(t *testing.T) {
	uxBinds := []string{"-v", "/Users/x/.tmux.conf:/home/ccbox/.tmux.conf:ro"}
	args := buildRunArgs("bash", nil, "/h/.ccbox/home", "/work", "xterm", true, uxBinds)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "/Users/x/.tmux.conf:/home/ccbox/.tmux.conf:ro") {
		t.Errorf("uxBinds が反映されていない: %v", args)
	}
	// UX bind は基本 bind の後・-w/-e/image より前に来るべき
	uxIdx := strings.Index(joined, "/Users/x/.tmux.conf")
	wIdx := strings.Index(joined, "-w /work")
	if uxIdx < 0 || wIdx < 0 || uxIdx >= wIdx {
		t.Errorf("UX bind の位置が不正: uxIdx=%d wIdx=%d joined=%q", uxIdx, wIdx, joined)
	}
}

func TestBuildPersistentRunArgs_includesUXBinds(t *testing.T) {
	uxBinds := []string{"-v", "/Users/x/.tmux.conf:/home/ccbox/.tmux.conf:ro"}
	args := buildPersistentRunArgs("ccbox-x-11112222", "/h/.ccbox/home", "/work", uxBinds, nil)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "/Users/x/.tmux.conf:/home/ccbox/.tmux.conf:ro") {
		t.Errorf("uxBinds が反映されていない: %v", args)
	}
}

func TestUXBindMountArgs_multipleEntries(t *testing.T) {
	home := t.TempDir()
	// 2 つ用意して両方が bind mount される
	for _, f := range []string{".tmux.conf", ".inputrc"} {
		if err := os.WriteFile(filepath.Join(home, f), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	got := uxBindMountArgs(home, []string{".tmux.conf", ".inputrc"})
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, filepath.Join(home, ".tmux.conf")+":/home/ccbox/.tmux.conf:ro") {
		t.Errorf(".tmux.conf のエントリが無い: %v", got)
	}
	if !strings.Contains(joined, filepath.Join(home, ".inputrc")+":/home/ccbox/.inputrc:ro") {
		t.Errorf(".inputrc のエントリが無い: %v", got)
	}
}
