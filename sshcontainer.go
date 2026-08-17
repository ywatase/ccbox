package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// socketDirs は Unix ソケットが作られるディレクトリ。macOS バインドマウント
// （VirtioFS）上ではソケットへの chmod が EINVAL で失敗し、Claude のリモート
// デーモンと codex app-server が起動できないため、tmpfs で覆う必要がある。
// 詳細: docs/superpowers/specs/2026-07-07-codex-ssh-design.md の「発見1」
var socketDirs = []string{
	"/home/ccbox/.claude/remote/run",
	"/home/ccbox/.codex/app-server-control",
}

// buildPersistentRunArgs は SSH 接続用の常駐コンテナを起動する docker run 引数列。
// セキュリティフラグは使い捨て実行（buildRunArgs）と同じ方針。
// uxBinds は UX 設定の read-only bind mount 引数列。
// extraMounts は projects.yaml 由来の追加マウント引数列（-v ペア列）。
func buildPersistentRunArgs(name, ccboxHome, projectPath string, uxBinds, extraMounts []string) []string {
	args := []string{"run", "-d", "--name", name,
		"--label", "ccbox.managed=true",
		"--label", "ccbox.project=" + projectPath,
		"--init",
		"--security-opt", "no-new-privileges",
		"--pids-limit", "1024",
		"-v", ccboxHome + ":/home/ccbox",
		"-v", projectPath + ":" + projectPath,
		"-w", projectPath,
	}
	// projects.yaml 由来の追加マウント
	args = append(args, extraMounts...)
	// UX 設定の bind mount（~/.ccbox/home 上への重ね塗り）
	args = append(args, uxBinds...)
	for _, d := range socketDirs {
		args = append(args, "--mount", "type=tmpfs,destination="+d+",tmpfs-mode=0700")
	}
	return append(args, runtimeImage(), "sleep", "infinity")
}

// chownArgs は tmpfs（root 所有でマウントされる）を ccbox ユーザーに渡す。
// コンテナの起動・再起動のたびに tmpfs は初期化されるため毎回実行が必要。
func chownArgs(name string) []string {
	return append([]string{"exec", "-u", "root", name, "chown", "ccbox:"}, socketDirs...)
}

func sshProxyExecArgs(name string) []string {
	return []string{"exec", "-i", name, "/usr/sbin/sshd", "-i", "-f", "/home/ccbox/.ssh/sshd_config"}
}

func psArgs() []string {
	return []string{"ps", "-a", "--filter", "label=ccbox.managed=true",
		"--format", "table {{.Names}}\t{{.Status}}\t{{.Label \"ccbox.project\"}}"}
}

func downArgs(name string) []string {
	return []string{"rm", "-f", name}
}

// findDocker は docker CLI の絶対パスを返す。ProxyCommand として App の ssh から
// 起動される場合は PATH が最小（/usr/bin:/bin など）のため、既知の場所も探す。
func findDocker() (string, error) {
	if p, err := exec.LookPath("docker"); err == nil {
		return p, nil
	}
	candidates := []string{"/opt/homebrew/bin/docker", "/usr/local/bin/docker"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".docker", "bin", "docker"))
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("docker が見つかりません。PATH または /opt/homebrew/bin を確認してください")
}

// ensureProjectContainer は常駐コンテナを必要なら起動し、コンテナ名を返す。
// 出力は一切 stdout に出さない（ssh-proxy 経由では stdout が SSH トランスポートのため）。
// App は同一プロジェクトへ複数の SSH 接続を同時に張るため、docker run の名前衝突
// （並行接続が先にコンテナを作った場合）は 1 回だけ状態確認からやり直す。
// uxBinds は UX 設定 bind mount 引数列。
// extraMounts は projects.yaml 追加マウント引数列。
// 既に実行中/停止中のコンテナは再作成せずそのまま使うため、projects.yaml を変更した
// 場合は `ccbox down && ccbox ssh` で作り直す必要がある（README に明記）。
// 既存コンテナのイメージが runtimeImage() と一致しない場合は明示的にエラーを返す。
// silent recreate は stdout が SSH トランスポートである本経路では危険で、コンテナ内の
// in-container state を壊すリスクがあるため。
func ensureProjectContainer(dockerPath, projectPath, ccboxHome string, uxBinds, extraMounts []string) (string, error) {
	name := containerName(projectPath)
	for attempt := 0; ; attempt++ {
		// .State.Running と .Config.Image を 1 回の inspect でまとめて取得する。
		// タブ区切りは docker のタグ書式（[a-zA-Z0-9._:/-]）と衝突しないため安全。
		out, err := exec.Command(dockerPath, "inspect", "-f", "{{.State.Running}}\t{{.Config.Image}}", name).Output()
		switch {
		case err == nil:
			parts := strings.SplitN(strings.TrimSpace(string(out)), "\t", 2)
			running := parts[0] == "true"
			existingImage := ""
			if len(parts) == 2 {
				existingImage = parts[1]
			}
			wanted := runtimeImage()
			if existingImage != wanted {
				return "", fmt.Errorf("既存コンテナ %s のイメージ %q が要求 %q と一致しません。`ccbox down && ccbox ssh` で作り直してください", name, existingImage, wanted)
			}
			if running {
				// 実行中でも chown は毎回行う。ユーザーの docker restart で tmpfs が
				// root 所有に初期化されている可能性があり、chown は冪等で安価なため。
			} else {
				// 存在するが停止中 → 再開。tmpfs は初期化されるので chown もやり直す
				if err := runQuiet(dockerPath, "start", name); err != nil {
					return "", fmt.Errorf("コンテナ %s を再開できません: %w", name, err)
				}
			}
		default:
			if err := runQuiet(dockerPath, buildPersistentRunArgs(name, ccboxHome, projectPath, uxBinds, extraMounts)...); err != nil {
				if attempt == 0 {
					continue
				}
				return "", fmt.Errorf("コンテナ %s を起動できません: %w", name, err)
			}
		}
		if err := runQuiet(dockerPath, chownArgs(name)...); err != nil {
			return "", fmt.Errorf("ソケットディレクトリの所有者変更に失敗しました: %w", err)
		}
		return name, nil
	}
}

// runQuiet は docker コマンドを stdout を捨てて実行する（stderr は診断用に通す）。
func runQuiet(dockerPath string, args ...string) error {
	cmd := exec.Command(dockerPath, args...)
	cmd.Stdout = nil
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runSSHProxy は ProxyCommand 本体。常駐コンテナを保証してから自プロセスを
// docker exec sshd -i に置き換える（exec なので SSH のシグナル・EOF 伝搬が素直になる）。
// 引数は `<projectPath>` または `<projectPath> --image <tag>` の 2 形式。
// --image は ccbox ssh 登録時に CCBOX_IMAGE の値を永続化するために ProxyCommand に
// 埋め込まれる。ssh 起動時には環境変数が引き継がれないため。
func runSSHProxy(args []string) error {
	if len(args) != 1 && len(args) != 3 {
		return fmt.Errorf("使い方: ccbox ssh-proxy <プロジェクトの絶対パス> [--image <tag>]")
	}
	projectPath := args[0]
	if !filepath.IsAbs(projectPath) {
		return fmt.Errorf("プロジェクトパスは絶対パスで指定してください: %s", projectPath)
	}
	if len(args) == 3 {
		if args[1] != "--image" {
			return fmt.Errorf("使い方: ccbox ssh-proxy <プロジェクトの絶対パス> [--image <tag>]")
		}
		if err := validateImageTag(args[2]); err != nil {
			return err
		}
		// runtimeImage() は CCBOX_IMAGE を読むため、ここで env を設定するのが最短経路。
		// 呼び出し元 (App/ssh) の env は影響を受けない（子プロセスの env のみ変更）。
		if err := os.Setenv("CCBOX_IMAGE", args[2]); err != nil {
			return fmt.Errorf("CCBOX_IMAGE を設定できません: %w", err)
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("ホームディレクトリを取得できません: %w", err)
	}
	if err := validateProjectDir(projectPath, home); err != nil {
		return err
	}
	if err := validateSSHProjectPath(projectPath); err != nil {
		return err
	}

	dockerPath, err := findDocker()
	if err != nil {
		return err
	}
	if !imageExists() {
		return fmt.Errorf("%s イメージが見つかりません（Docker daemon が停止している可能性もあります）。docker info を確認し、必要なら ccbox build を実行してください", runtimeImage())
	}
	ccboxHome, err := ensureCcboxHome()
	if err != nil {
		return err
	}
	if strings.Contains(ccboxHome, ":") {
		return fmt.Errorf("パスに ':' が含まれるため docker の -v 構文で安全にマウントできません: %s", ccboxHome)
	}
	uxBinds := uxBindMountArgs(home, uxWhitelistDefault)
	extraMounts, err := loadExtraMountArgs(home)
	if err != nil {
		return err
	}
	name, err := ensureProjectContainer(dockerPath, projectPath, ccboxHome, uxBinds, extraMounts)
	if err != nil {
		return err
	}
	execArgs := append([]string{dockerPath}, sshProxyExecArgs(name)...)
	return syscall.Exec(dockerPath, execArgs, os.Environ())
}

// cmdPS は ccbox 管理の常駐コンテナを一覧表示する。
func cmdPS() error {
	dockerPath, err := findDocker()
	if err != nil {
		return err
	}
	cmd := exec.Command(dockerPath, psArgs()...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// cmdDown は常駐コンテナを停止・削除する。引数省略時はカレントディレクトリが対象。
func cmdDown(args []string) error {
	var projectPath string
	switch len(args) {
	case 0:
		pwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("カレントディレクトリを取得できません: %w", err)
		}
		projectPath = pwd
	case 1:
		abs, err := filepath.Abs(args[0])
		if err != nil {
			return err
		}
		projectPath = abs
	default:
		return fmt.Errorf("使い方: ccbox down [プロジェクトパス]")
	}

	dockerPath, err := findDocker()
	if err != nil {
		return err
	}
	name := containerName(projectPath)
	if err := runQuiet(dockerPath, downArgs(name)...); err != nil {
		return fmt.Errorf("コンテナ %s を削除できません（存在しない場合もこのエラーになります）: %w", name, err)
	}
	fmt.Printf("停止・削除しました: %s\n", name)
	return nil
}
