package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/term"
)

// checkDocker は docker CLI が PATH にあり、daemon が応答しているか確認する。
func checkDocker() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker が PATH に見つかりません。Docker Desktop などをインストールしてください")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		return fmt.Errorf("Docker daemon に接続できません。Docker が起動しているか確認してください")
	}
	return nil
}

// imageExists は ccbox:latest イメージの存在を確認する。
func imageExists() bool {
	err := exec.Command("docker", "image", "inspect", imageTag).Run()
	return err == nil
}

// buildImage は Dockerfile を stdin 経由で docker build に渡してイメージをビルドする。
// noCache=true のとき --no-cache --pull を付与する。
func buildImage(noCache bool) error {
	args := []string{"build", "-t", imageTag}
	if noCache {
		args = append(args, "--no-cache", "--pull")
	}
	// bind mount したホストのファイルを読み書きできるよう、コンテナユーザーを
	// ホストと同じ UID/GID で作成する（Windows など取得不能な環境ではデフォルトに任せる）。
	if uid := os.Getuid(); uid >= 0 {
		args = append(args, "--build-arg", fmt.Sprintf("CCBOX_UID=%d", uid))
	}
	if gid := os.Getgid(); gid >= 0 {
		args = append(args, "--build-arg", fmt.Sprintf("CCBOX_GID=%d", gid))
	}
	// コンテキスト不要なので stdin から Dockerfile を渡す（-f - -）
	args = append(args, "-")

	cmd := exec.Command("docker", args...)
	cmd.Stdin = dockerfileContent()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runClaude は ccbox:latest コンテナ内で claude を実行する。
// extraArgs は claude に渡す追加引数。
func runClaude(extraArgs []string) error {
	return runContainer("claude", extraArgs)
}

// runShell は ccbox:latest コンテナ内で bash を起動する。
func runShell() error {
	return runContainer("bash", nil)
}

// runContainer は指定コマンドを同一マウント構成で実行する共通関数。
func runContainer(entryCmd string, extraArgs []string) error {
	ccboxHome, err := ensureCcboxHome()
	if err != nil {
		return err
	}

	pwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("カレントディレクトリを取得できません: %w", err)
	}

	term := os.Getenv("TERM")
	if term == "" {
		term = "xterm-256color"
	}

	// -i は常に付与し、-t は stdin が実端末のときのみ付与する。
	// パイプや /dev/null 接続時に -t を付けると docker が "not a TTY" で失敗するため。
	args := []string{"run", "--rm", "--init", "-i"}
	if isTTY() {
		args = append(args, "-t")
	}

	args = append(args,
		"-v", ccboxHome+":/home/ccbox",
		"-v", pwd+":"+pwd,
		"-w", pwd,
		"-e", "TERM="+term,
		imageTag,
		entryCmd,
	)
	args = append(args, extraArgs...)

	cmd := exec.Command("docker", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
}

// isTTY は os.Stdin が実端末かどうかを判定する。
// ModeCharDevice 判定は /dev/null もキャラクタデバイスのため誤判定する。
func isTTY() bool {
	return isTerminalFile(os.Stdin)
}

func isTerminalFile(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// ensureCcboxHome は ~/.ccbox/home が存在しなければ作成して返す。
// 認証情報（.claude/.credentials.json）を含むため 0700 で作成し、既存ディレクトリも揃える。
func ensureCcboxHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("ホームディレクトリを取得できません: %w", err)
	}
	base := filepath.Join(home, ".ccbox")
	dir := filepath.Join(base, "home")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("~/.ccbox/home を作成できません: %w", err)
	}
	if err := os.Chmod(base, 0700); err != nil {
		return "", fmt.Errorf("~/.ccbox の権限を設定できません: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return "", fmt.Errorf("~/.ccbox/home の権限を設定できません: %w", err)
	}
	return dir, nil
}
