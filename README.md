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

**ホームディレクトリ露出ガード:** ホームディレクトリ自身やその祖先（`~`、`/Users`、`/` など）で実行すると、`~/.ssh` やホスト側 `~/.claude` の認証情報がコンテナに露出して隔離が無意味になるため、エラーで中止する。リスクを理解した上で実行する場合は `CCBOX_ALLOW_UNSAFE_DIR=1` を設定する。

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

# codex を起動（引数はそのまま codex に渡る）
ccbox codex

# Mac App（Claude App / Codex App）からの SSH 接続
ccbox ssh              # カレントディレクトリを接続可能として登録
ccbox ps               # 常駐コンテナの一覧
ccbox down [パス]      # 常駐コンテナの停止・削除

# イメージ管理
ccbox build            # ccbox:latest をビルド
ccbox update           # --no-cache --pull で再ビルド（claude code を最新化）

# デバッグ
ccbox shell            # 同じマウント構成で bash を起動

# バージョン確認
ccbox version
```

## Mac App からの SSH 接続

`ccbox ssh` を実行すると、Claude App / Codex App のリモート接続機能からコンテナに接続できるようになる。

```sh
cd ~/git/myapp
ccbox ssh    # → エイリアス ccbox-myapp が ~/.ccbox/ssh/config に登録される
```

仕組み: `~/.ssh/config` に `Include ~/.ccbox/ssh/config` を 1 行追加し（初回に確認あり）、エントリの実体は ccbox が `~/.ccbox/ssh/config` で管理する。接続は `ProxyCommand ccbox ssh-proxy <パス>` → `docker exec` 経由の sshd（inetd モード）で行い、**ホストにもコンテナにもリッスンポートを一切作らない**。常駐コンテナは初回接続時に自動起動し、`ccbox down` で停止するまで残る。

codex の初回認証（認証情報は `~/.ccbox/home/.codex/` に永続化）:

```sh
ssh -t -L 1455:localhost:1455 ccbox-myapp codex login
# または: ssh -t ccbox-myapp codex login --device-auth
```

### トラブルシューティング

- **Claude App で `Permission denied` (server)**: アップロードされた `~/.ccbox/home/.claude/remote/srv/<hash>/server` に実行ビットが付かないことが稀にある。`chmod +x ~/.ccbox/home/.claude/remote/srv/*/server` で解消する
- **`disabling multiplexing` 警告**: ユーザー側 ssh 設定の ControlMaster の stale ソケット。`rm ~/.ssh/mux-*`（該当ファイル）で解消する。ccbox 管理エントリ自体は多重化を無効にしている
- **`timeout waiting for daemon` / `socket hang up`**: コンテナが古い構成で動いている可能性がある。`ccbox down && ccbox ssh` で作り直す

## 制限事項

- SSH 接続はコンテナ内 sshd を経由する。接続できるのはこの Mac のローカルユーザーのみ（ポート非公開、`docker exec` 経由）
- ローカルの `ccbox:latest` イメージは内容を検証せずに信頼して実行する。同名タグを別イメージで上書きされる環境では注意
- カレントディレクトリのパスに `:` が含まれる場合は、docker の `-v` 構文で安全に mount できないため明示エラーで中止する
- ネットワークは隔離しない（API 通信・パッケージ取得のため）。隔離対象はファイルシステムのみ

## ライセンス

MIT
