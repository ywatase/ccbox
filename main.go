package main

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// version は -ldflags "-X main.version=x.y.z" で上書き可能。
var version = "dev"

// imageTag は build のデフォルトタグ。CCBOX_IMAGE が実行時のみ効くのは、
// 「build 出力先」と「実行対象」を混ぜないため（ビルドは常に既知タグに集約）。
const imageTag = "ccbox:latest"

// runtimeImage は使い捨て・常駐コンテナで docker run に渡すイメージ名を返す。
// CCBOX_IMAGE 環境変数で上書き可能。ccbox build --tag ccbox:myextra で作成した
// 別イメージを CCBOX_IMAGE=ccbox:myextra ccbox のように呼び出すための機構。
func runtimeImage() string {
	if env := os.Getenv("CCBOX_IMAGE"); env != "" {
		return env
	}
	return imageTag
}

// imageTagAllowed は docker のイメージタグ・レジストリパスとして許容する文字集合。
// ssh_config の ProxyCommand へ埋め込む前に validate することで、$ ` " \ 等の
// シェル特殊文字によるインジェクションを防ぐ。docker の legal tag よりも保守的
// （: / . _ - は許可、# @ + 等は不許可）。
var imageTagAllowed = regexp.MustCompile(`^[A-Za-z0-9._:/-]+$`)

// validateImageTag は CCBOX_IMAGE / --image 引数として渡されたタグ文字列が
// 安全に扱えるかを検査する。ProxyCommand への埋め込み・docker CLI への引き渡し前に呼ぶ。
func validateImageTag(tag string) error {
	if tag == "" {
		return errors.New("イメージタグが空です")
	}
	if !imageTagAllowed.MatchString(tag) {
		return fmt.Errorf("イメージタグに使えない文字が含まれています: %q（許容: %s）", tag, imageTagAllowed.String())
	}
	return nil
}

// checkEnvImageTag は CCBOX_IMAGE を main の入口で検査する。
// 値は docker CLI や ssh_config の ProxyCommand に流れるため、何かに渡す前・
// エラーメッセージに載せる前に弾く。後段（イメージ不在エラー等）で弾くと、
// 不正なタグ文字列がコピペ可能なコマンド例として出力されてしまう。
func checkEnvImageTag() error {
	env := os.Getenv("CCBOX_IMAGE")
	if env == "" {
		return nil
	}
	if err := validateImageTag(env); err != nil {
		return fmt.Errorf("CCBOX_IMAGE: %w", err)
	}
	return nil
}

//go:embed Dockerfile
var embeddedDockerfile []byte

// dockerfileContent は embed した Dockerfile の内容を strings.Reader で返す。
func dockerfileContent() *strings.Reader {
	return strings.NewReader(string(embeddedDockerfile))
}

// dispatchResult は引数解析の結果を表す。
type dispatchResult struct {
	subcommand  string   // "build" / "update" / "shell" / "codex" / "ssh" / "ssh-proxy" / "ps" / "down" / "version" / "help" / "claude"
	claudeArgs  []string // claude/codex に渡す引数、または ssh-proxy/down の対象パス
	forceClaude bool     // -- による強制 claude 渡し
	extraPath   string   // build/update の --extra <path>（空=自動探索）
	tag         string   // build/update の --tag <name>（空=imageTag デフォルト）
	parseErr    error    // フラグ解析エラー。非 nil のとき main は usage 付きで exit 1
}

// parseArgs はサブコマンド判定を行う。
// 第1引数が既知のサブコマンドのときのみサブコマンド扱いし、それ以外はすべて claude に渡す。
func parseArgs(args []string) dispatchResult {
	if len(args) > 0 && args[0] == "--" {
		return dispatchResult{subcommand: "claude", claudeArgs: args[1:], forceClaude: true}
	}
	if len(args) == 0 {
		return dispatchResult{subcommand: "claude"}
	}
	switch args[0] {
	case "build":
		return parseBuildFlags("build", args[1:])
	case "update":
		return parseBuildFlags("update", args[1:])
	case "shell":
		return dispatchResult{subcommand: "shell"}
	case "codex":
		return dispatchResult{subcommand: "codex", claudeArgs: args[1:]}
	case "ssh":
		return dispatchResult{subcommand: "ssh"}
	case "ssh-proxy":
		return dispatchResult{subcommand: "ssh-proxy", claudeArgs: args[1:]}
	case "ps":
		return dispatchResult{subcommand: "ps"}
	case "down":
		return dispatchResult{subcommand: "down", claudeArgs: args[1:]}
	case "mount":
		return dispatchResult{subcommand: "mount", claudeArgs: args[1:]}
	case "version":
		return dispatchResult{subcommand: "version"}
	case "help", "-h", "--help":
		return dispatchResult{subcommand: "help"}
	default:
		return dispatchResult{subcommand: "claude", claudeArgs: args}
	}
}

// parseBuildFlags は build/update サブコマンドのフラグ（--extra <path>, --tag <name>）を解析する。
// 不明フラグ・値欠落は parseErr を保持して返す。呼び出し側は parseErr を確認し、
// 非 nil ならビルドを開始せず usage 付きで終了する。--extra を無視して黙って
// 自動探索に落とすと、ユーザーの意図に反して ~/.ccbox/extra.Dockerfile が
// 巻き込まれるため、値欠落は明示エラーにする。
func parseBuildFlags(sub string, args []string) dispatchResult {
	r := dispatchResult{subcommand: sub}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--extra":
			if i+1 >= len(args) {
				r.parseErr = fmt.Errorf("--extra には値（extra.Dockerfile のパス）が必要です")
				return r
			}
			r.extraPath = args[i+1]
			i++
		case "--tag":
			if i+1 >= len(args) {
				r.parseErr = fmt.Errorf("--tag には値（イメージタグ名）が必要です")
				return r
			}
			r.tag = args[i+1]
			i++
		default:
			r.parseErr = fmt.Errorf("不明なフラグ: %s", args[i])
			return r
		}
	}
	return r
}

func main() {
	r := parseArgs(os.Args[1:])
	if r.parseErr != nil {
		fmt.Fprintln(os.Stderr, "エラー:", r.parseErr)
		fmt.Fprintln(os.Stderr, "使い方: ccbox", r.subcommand, "[--extra <path>] [--tag <name>]")
		os.Exit(2)
	}
	if err := checkEnvImageTag(); err != nil {
		fmt.Fprintln(os.Stderr, "エラー:", err)
		os.Exit(2)
	}
	switch r.subcommand {
	case "build":
		runWithDockerCheck(false, func() error { return buildImage(false, r.extraPath, r.tag) })
	case "update":
		runWithDockerCheck(false, func() error { return buildImage(true, r.extraPath, r.tag) })
	case "shell":
		runWithDockerCheck(true, runShell)
	case "codex":
		runWithDockerCheck(true, func() error { return runCodex(r.claudeArgs) })
	case "ssh":
		runWithDockerCheck(true, cmdSSHRegister)
	case "ssh-proxy":
		// stdout は SSH トランスポートのため runWithDockerCheck（自動ビルドが
		// stdout に出力する）を経由しない。エラーは stderr のみ。
		if err := runSSHProxy(r.claudeArgs); err != nil {
			fmt.Fprintln(os.Stderr, "エラー:", err)
			os.Exit(1)
		}
	case "ps":
		runWithDockerCheck(false, cmdPS)
	case "down":
		runWithDockerCheck(false, func() error { return cmdDown(r.claudeArgs) })
	case "mount":
		// mount は Docker daemon に触らないので checkDocker をスキップ
		if err := runMount(r.claudeArgs); err != nil {
			fmt.Fprintln(os.Stderr, "エラー:", err)
			os.Exit(1)
		}
	case "version":
		fmt.Println("ccbox", version)
	case "help":
		printHelp()
	default:
		runWithDockerCheck(true, func() error { return runClaude(r.claudeArgs) })
	}
}

// runWithDockerCheck は docker の存在・起動確認を行ってから fn を実行する。
// autoBuild=true のときイメージが存在しなければ自動ビルドする。
// build/update サブコマンドは fn 自身がビルドするため autoBuild=false にして二重ビルドを避ける。
func runWithDockerCheck(autoBuild bool, fn func() error) {
	if err := checkDocker(); err != nil {
		fmt.Fprintln(os.Stderr, "エラー:", err)
		os.Exit(1)
	}

	if autoBuild && !imageExists() {
		ri := runtimeImage()
		if ri != imageTag {
			// CCBOX_IMAGE で指定されたカスタムイメージは自動ビルドできない。
			// build のデフォルトタグと一致しないため、明示的にビルドを促す。
			// %q で引用するのは、タグ文字列をそのまま出すとコピペ時にシェルへ
			// 解釈される形になるため（入口の validateImageTag と二重で防ぐ）。
			fmt.Fprintf(os.Stderr, "エラー: イメージ %q が見つかりません。\n", ri)
			fmt.Fprintf(os.Stderr, "ccbox build --tag %q [--extra <path>] で作成してください。\n", ri)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "ccbox:latest イメージが見つかりません。自動ビルドを開始します...")
		// 自動ビルドでも ~/.ccbox/extra.Dockerfile があれば拾う（拡張ユーザーの意図を尊重）
		if err := buildImage(false, "", ""); err != nil {
			fmt.Fprintln(os.Stderr, "エラー: イメージのビルドに失敗しました:", err)
			os.Exit(1)
		}
	}

	if err := fn(); err != nil {
		fmt.Fprintln(os.Stderr, "エラー:", err)
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Print(`使い方:
  ccbox [claude への引数...]  Claude Code をコンテナ内で実行する（イメージがなければ自動ビルド）
  ccbox codex [引数...]       codex をコンテナ内で実行する
  ccbox ssh                   カレントディレクトリを Mac App から SSH 接続可能として登録する
  ccbox ps                    SSH 用の常駐コンテナを一覧表示する
  ccbox down [パス]           常駐コンテナを停止・削除する（省略時はカレントディレクトリ）
  ccbox mount add <host> [--container <path>] [--ro]
                              常駐コンテナに追加マウントを登録する（~/.ccbox/projects.yaml）
  ccbox mount rm <host>       追加マウントを削除する
  ccbox mount list            追加マウント一覧を表示する
  ccbox build [--extra <path>] [--tag <name>]
                              ccbox:latest イメージをビルドする
                              --extra 省略時は ~/.ccbox/extra.Dockerfile があれば自動連結する
  ccbox update [--extra <path>] [--tag <name>]
                              --no-cache --pull で再ビルドする（claude/codex を最新化）
  ccbox shell                 同じマウント構成で bash を起動する（デバッグ用）
  ccbox version               バージョンを表示する
  ccbox help / -h / --help    このヘルプを表示する
  ccbox -- [claude への引数...] -- 以降を強制的に claude の引数として渡す

マウント構成:
  ~/.ccbox/home  →  コンテナの /home/ccbox（セッション・認証情報の永続化）
  <カレントディレクトリ>  →  コンテナ内の同一絶対パス（作業ディレクトリ）
  ~/.tmux.conf   →  /home/ccbox/.tmux.conf（read-only、存在時のみ）

環境変数:
  CCBOX_IMAGE            実行に使うイメージを切り替える（未ビルドなら自動ビルドせずエラー）
                         ccbox ssh 実行時の値は ssh_config の ProxyCommand に永続化される
  CCBOX_ALLOW_UNSAFE_DIR ホーム露出ガードを無効化する（=1、非推奨）

初回認証:
  claude: ccbox shell で bash を起動後、claude コマンドで OAuth ログイン
  codex:  ccbox ssh で登録後、ssh -t -L 1455:localhost:1455 <エイリアス> codex login
`)
}
