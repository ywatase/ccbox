package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	pwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("カレントディレクトリを取得できません: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("ホームディレクトリを取得できません: %w", err)
	}

	mountPwd := pwd
	if resolved, err := filepath.EvalSymlinks(pwd); err == nil {
		mountPwd = resolved
	}
	mountHome := home
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		mountHome = resolved
	}
	if isUnsafeMountDir(mountPwd, mountHome) {
		if os.Getenv("CCBOX_ALLOW_UNSAFE_DIR") != "1" {
			return errors.New("ホームディレクトリ全体がコンテナに露出するため、このディレクトリでは実行できません。意図的に実行する場合は CCBOX_ALLOW_UNSAFE_DIR=1 を設定してください")
		}
		fmt.Fprintln(os.Stderr, "警告: ホームディレクトリ全体がコンテナに露出するディレクトリで実行しています。")
	}

	ccboxHome, err := ensureCcboxHome()
	if err != nil {
		return err
	}

	term := os.Getenv("TERM")
	if term == "" {
		term = "xterm-256color"
	}

	args := buildRunArgs(entryCmd, extraArgs, ccboxHome, pwd, term, isTTY())

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

// isUnsafeMountDir は pwd をマウントするとホームディレクトリ全体が露出するかを判定する。
// pwd がホームと一致またはその祖先の場合、~/.ssh やホスト側 ~/.claude の認証情報が
// コンテナ内から読めてしまい隔離の意味がなくなるため、危険と判定する。
func isUnsafeMountDir(pwd, home string) bool {
	cleanPwd := filepath.Clean(pwd)
	cleanHome := filepath.Clean(home)
	if cleanPwd == cleanHome {
		return true
	}
	rel, err := filepath.Rel(cleanPwd, cleanHome)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// buildRunArgs は docker run の引数列を構築する。
// -i は常に付与し、-t は stdin が実端末（tty=true）のときのみ付与する。
// パイプや /dev/null 接続時に -t を付けると docker が "not a TTY" で失敗するため。
//
// TTY モードでは --sig-proxy=false を明示する。Ctrl+C/Ctrl+Z などの端末制御文字由来の
// シグナルは PTY の line discipline 経由でコンテナ内プロセスに直接届くため転送は不要で、
// macOS ではホスト側の SIGIO（ターミナルや zsh プラグインの非同期 I/O 通知）が
// docker CLI 経由でコンテナに転送されると、Linux 側 SIGIO のデフォルト動作で
// bash/claude が即死する（exit 157 = 128+29）ため。
// 代償として、ccbox/docker CLI プロセスへ直接送られた SIGTERM 等もコンテナに転送されなくなる。
// 非 TTY モードでは Ctrl+C の転送に sig-proxy が必要なのでデフォルト（有効）のままにする。
func buildRunArgs(entryCmd string, extraArgs []string, ccboxHome, pwd, term string, tty bool) []string {
	args := []string{"run", "--rm", "--init", "-i"}
	if tty {
		args = append(args, "-t", "--sig-proxy=false")
	}
	args = append(args,
		"-v", ccboxHome+":/home/ccbox",
		"-v", pwd+":"+pwd,
		"-w", pwd,
		"-e", "TERM="+term,
		imageTag,
		entryCmd,
	)
	return append(args, extraArgs...)
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
