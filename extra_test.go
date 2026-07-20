package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBuildFlags_extraAndTag(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		sub           string
		wantExtraPath string
		wantTag       string
	}{
		{"引数なし", []string{}, "build", "", ""},
		{"--extra のみ", []string{"--extra", "/path/to/extra.Dockerfile"},
			"build", "/path/to/extra.Dockerfile", ""},
		{"--tag のみ", []string{"--tag", "ccbox:shogun"},
			"build", "", "ccbox:shogun"},
		{"--extra と --tag 両方", []string{"--extra", "/e", "--tag", "t"},
			"build", "/e", "t"},
		{"順序反転", []string{"--tag", "t", "--extra", "/e"},
			"update", "/e", "t"},
		{"未知フラグは無視", []string{"--unknown", "x", "--extra", "/e"},
			"build", "/e", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseBuildFlags(tt.sub, tt.args)
			if got.subcommand != tt.sub {
				t.Errorf("subcommand = %q, want %q", got.subcommand, tt.sub)
			}
			if got.extraPath != tt.wantExtraPath {
				t.Errorf("extraPath = %q, want %q", got.extraPath, tt.wantExtraPath)
			}
			if got.tag != tt.wantTag {
				t.Errorf("tag = %q, want %q", got.tag, tt.wantTag)
			}
		})
	}
}

func TestParseArgs_buildWithFlags(t *testing.T) {
	// build/update とフラグ解析の統合。既存の parseArgs 経由でフラグが伝わることを確認する。
	r := parseArgs([]string{"build", "--extra", "/x", "--tag", "y"})
	if r.subcommand != "build" || r.extraPath != "/x" || r.tag != "y" {
		t.Errorf("parseArgs(build --extra /x --tag y) = %+v", r)
	}
	r = parseArgs([]string{"update", "--tag", "t"})
	if r.subcommand != "update" || r.tag != "t" {
		t.Errorf("parseArgs(update --tag t) = %+v", r)
	}
}

func TestResolveExtraDockerfile_defaultAbsent(t *testing.T) {
	// HOME を temp に差し替え、~/.ccbox/extra.Dockerfile が存在しない場合。
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, ok, err := resolveExtraDockerfile("")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if ok {
		t.Errorf("ok = true, want false（拡張なし）")
	}
	if path != "" {
		t.Errorf("path = %q, want empty", path)
	}
}

func TestResolveExtraDockerfile_defaultPresent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".ccbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(dir, "extra.Dockerfile")
	if err := os.WriteFile(extra, []byte("USER root\nRUN true\n"), 0600); err != nil {
		t.Fatal(err)
	}

	path, ok, err := resolveExtraDockerfile("")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !ok {
		t.Errorf("ok = false, want true")
	}
	if path != extra {
		t.Errorf("path = %q, want %q", path, extra)
	}
}

func TestResolveExtraDockerfile_explicitMissing(t *testing.T) {
	// 明示指定は存在必須。
	_, _, err := resolveExtraDockerfile("/nonexistent/extra.Dockerfile")
	if err == nil {
		t.Fatal("err = nil, want error")
	}
	if !strings.Contains(err.Error(), "--extra") {
		t.Errorf("エラー文言に --extra が含まれない: %v", err)
	}
}

func TestResolveExtraDockerfile_explicitPresent(t *testing.T) {
	dir := t.TempDir()
	extra := filepath.Join(dir, "custom.Dockerfile")
	if err := os.WriteFile(extra, []byte("RUN true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path, ok, err := resolveExtraDockerfile(extra)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !ok || path != extra {
		t.Errorf("path=%q ok=%v", path, ok)
	}
}

func TestComposeDockerfile_noExtra(t *testing.T) {
	// HOME を temp（extra なし）に差し替え、本体だけが返ることを確認。
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := composeDockerfile("")
	if err != nil {
		t.Fatal(err)
	}
	if got != string(embeddedDockerfile) {
		t.Errorf("extra なしのとき本体そのままであるべき（先頭 100 文字）: got=%q", got[:min(100, len(got))])
	}
	if strings.Contains(got, "ccbox extra layer") {
		t.Error("extra なしなのにマーカーが含まれている")
	}
}

func TestComposeDockerfile_withExtra(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".ccbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	extraContent := "USER root\nRUN apt-get install -y tmux\nUSER ccbox\n"
	if err := os.WriteFile(filepath.Join(dir, "extra.Dockerfile"), []byte(extraContent), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := composeDockerfile("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "ccbox extra layer") {
		t.Error("extra あるのにマーカーが含まれていない")
	}
	if !strings.Contains(got, extraContent) {
		t.Error("extra の内容が連結されていない")
	}
	// 本体が先、extra が後の順序であること
	baseIdx := strings.Index(got, "FROM node:22")
	extraIdx := strings.Index(got, "ccbox extra layer")
	if baseIdx < 0 || extraIdx < 0 || baseIdx >= extraIdx {
		t.Errorf("本体が extra より先に来ていない: base=%d extra=%d", baseIdx, extraIdx)
	}
}

func TestComposeDockerfile_extraWithoutTrailingNewline(t *testing.T) {
	// extra.Dockerfile の末尾に改行が無い場合も連結後に安全に改行が入ること。
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".ccbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// 末尾に改行なし
	if err := os.WriteFile(filepath.Join(dir, "extra.Dockerfile"), []byte("RUN true"), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := composeDockerfile("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Error("連結後の末尾が改行で終わっていない")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
