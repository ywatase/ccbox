# ccbox

Claude Code を Docker コンテナ内に隔離して実行する CLI。

## 目的

Claude Code が実行中にホストのファイルシステムを変更したり、`~/.claude` の認証情報を読み書きしたりするリスクを、Docker による隔離で最小化する。
作業ディレクトリ（成果物）はホストからそのまま見える。

## 隔離モデル

| マウント先（コンテナ） | ホスト側 | 説明 |
|---|---|---|
| `/home/ccbox` | `~/.ccbox/home` | Claude Code のセッション・認証情報を永続化 |
| `<カレントディレクトリ>` | `<カレントディレクトリ>` | 作業ディレクトリ（同一絶対パスで mount） |

**見えないもの（コンテナから読めないもの）:**
- ホストの `~/.claude`（ccbox のセッションとは完全分離）
- カレントディレクトリ以外のホストのファイル

カレントディレクトリをコンテナ内の **同一の絶対パス** に mount することで、Claude Code がセッション識別に使うパスエンコード（`~/.claude/projects/` 以下）がプロジェクト間で衝突しない。

## インストール

```sh
go install github.com/ywatase/ccbox@latest
```

または clone してビルド:

```sh
git clone https://github.com/ywatase/ccbox
cd ccbox
go build -o ccbox .
```

## 初回認証

初回は OAuth ログインが必要です。

```sh
# bash でコンテナに入る（イメージが無ければ自動ビルドされる）
ccbox shell

# コンテナ内で claude を起動して OAuth ログインを完了させる
claude
```

認証情報は `~/.ccbox/home/.claude/.credentials.json` に永続化されるため、次回以降は自動ログインされます。

## 使い方

```sh
# Claude Code を起動（引数はそのまま claude に渡る）
ccbox
ccbox "このコードをレビューして"
ccbox -- --help        # claude --help を実行（--help 単体は ccbox のヘルプになる）

# -- で強制的に claude の引数として渡す（サブコマンドと衝突する場合）
ccbox -- build

# イメージ管理
ccbox build            # ccbox:latest をビルド
ccbox update           # --no-cache --pull で再ビルド（claude code を最新化）

# デバッグ
ccbox shell            # 同じマウント構成で bash を起動

# バージョン確認
ccbox version
```

## 制限事項

- ローカルの `ccbox:latest` イメージは内容を検証せずに信頼して実行する。同名タグを別イメージで上書きされる環境では注意
- カレントディレクトリのパスに `:` が含まれると docker の `-v` 構文と衝突して動作しない
- ネットワークは隔離しない（API 通信・パッケージ取得のため）。隔離対象はファイルシステムのみ

## ライセンス

MIT
