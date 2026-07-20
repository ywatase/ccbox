package main

import (
	_ "embed"
	"fmt"
	"os"
	"strings"
)

// version は -ldflags "-X main.version=x.y.z" で上書き可能。
var version = "dev"

const imageTag = "ccbox:latest"

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
	case "version":
		return dispatchResult{subcommand: "version"}
	case "help", "-h", "--help":
		return dispatchResult{subcommand: "help"}
	default:
		return dispatchResult{subcommand: "claude", claudeArgs: args}
	}
}

// parseBuildFlags は build/update サブコマンドのフラグ（--extra <path>, --tag <name>）を解析する。
// 不明フラグや値欠落は "help" にフォールバックせず、error 相当として build/update をそのまま返す
// （実行時に buildImage 側でパスの存在確認等が走る）。
func parseBuildFlags(sub string, args []string) dispatchResult {
	r := dispatchResult{subcommand: sub}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--extra":
			if i+1 >= len(args) {
				// 値が無い場合は help にフォールバックせず、そのまま返す（呼び出し側で扱う想定）
				r.extraPath = ""
				return r
			}
			r.extraPath = args[i+1]
			i++
		case "--tag":
			if i+1 >= len(args) {
				return r
			}
			r.tag = args[i+1]
			i++
		}
	}
	return r
}

func main() {
	r := parseArgs(os.Args[1:])
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

初回認証:
  claude: ccbox shell で bash を起動後、claude コマンドで OAuth ログイン
  codex:  ccbox ssh で登録後、ssh -t -L 1455:localhost:1455 <エイリアス> codex login
`)
}
