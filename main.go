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
	subcommand  string   // "build" / "update" / "shell" / "version" / "help" / "claude"
	claudeArgs  []string // claude に渡す引数（subcommand=="claude" のとき）
	forceClaude bool     // -- による強制 claude 渡し
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
		return dispatchResult{subcommand: "build"}
	case "update":
		return dispatchResult{subcommand: "update"}
	case "shell":
		return dispatchResult{subcommand: "shell"}
	case "version":
		return dispatchResult{subcommand: "version"}
	case "help", "-h", "--help":
		return dispatchResult{subcommand: "help"}
	default:
		return dispatchResult{subcommand: "claude", claudeArgs: args}
	}
}

func main() {
	r := parseArgs(os.Args[1:])
	switch r.subcommand {
	case "build":
		runWithDockerCheck(false, func() error { return buildImage(false) })
	case "update":
		runWithDockerCheck(false, func() error { return buildImage(true) })
	case "shell":
		runWithDockerCheck(true, runShell)
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
		if err := buildImage(false); err != nil {
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
  ccbox build                 ccbox:latest イメージをビルドする
  ccbox update                --no-cache --pull で再ビルドする（claude code を最新化）
  ccbox shell                 同じマウント構成で bash を起動する（デバッグ用）
  ccbox version               バージョンを表示する
  ccbox help / -h / --help    このヘルプを表示する
  ccbox -- [claude への引数...] -- 以降を強制的に claude の引数として渡す

マウント構成:
  ~/.ccbox/home  →  コンテナの /home/ccbox（セッション・認証情報の永続化）
  <カレントディレクトリ>  →  コンテナ内の同一絶対パス（作業ディレクトリ）

初回認証:
  ccbox shell で bash を起動後、claude コマンドを実行して OAuth ログインを行ってください。
  認証情報は ~/.ccbox/home に永続化されます。
`)
}
