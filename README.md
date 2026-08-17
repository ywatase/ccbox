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

### 認証・状態の独立永続化

コンテナ内の認証・状態は `~/.ccbox/home` 配下に集約し、**ホスト側の同名設定は一切参照しない**。ホストの `~/.gitconfig` の credential.helper 経由で GitHub 権限が漏れる・ホストの `~/.ssh` を丸ごとコンテナに露出させる、といった隔離破壊を防ぐため。

| 種別 | 例 | 永続化先 |
|---|---|---|
| 認証情報 | `.claude/.credentials.json`, `.codex/`, `.config/gh`, `.config/glab-cli` | `~/.ccbox/home/` 配下（各同名パス） |
| SSH 鍵材料 | Mac App からの接続に使う鍵ペア | `~/.ccbox/ssh/`（クライアント側）と `~/.ccbox/home/.ssh/`（コンテナ側） |
| git identity | `.gitconfig` | `~/.ccbox/home/.gitconfig`（コンテナ内で `git config --global user.email` を別 identity に設定すればコンテナ発コミットが識別可能） |

**運用上の含意:**
- ホスト側で `gh auth login` 済みでも、コンテナ内では別途認証が必要（`ssh` 経由で `gh auth login` するか、fine-grained PAT を張る）
- ホスト側 `~/.gitconfig` の `[include]` や `credential.helper` の設定は無視される。コンテナ内 git ではコンテナ内 `~/.gitconfig` のみ有効

`~/.ccbox/` ディレクトリ以外に ccbox が書き込むホスト側パスは、`~/.ssh/config` 先頭に追記する `Include ~/.ccbox/ssh/config` の 1 行のみ（`ccbox ssh` の初回に確認あり）。

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

# 拡張ビルド（tmux/gh/glab などを追加インストール）
ccbox build --extra ~/.ccbox/extra.Dockerfile
ccbox build --tag ccbox:myextra    # 別タグにビルド

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

## 拡張ビルドレイヤー

`~/.ccbox/extra.Dockerfile` があれば、`ccbox build` / `ccbox update` は本体 Dockerfile の後ろに自動連結してビルドする。tmux/gh/glab など追加パッケージを ccbox の Dockerfile を fork せずに乗せるための機構。

**制約:**
- ビルドコンテキストは張らない（本体と同じ `docker build -` のまま）。extra.Dockerfile 内で `COPY <hostfile>` はできない。追加ファイルは `RUN curl -fsSL <URL> -o /tmp/x` のようにオンライン取得すること
- extra.Dockerfile の内容は ccbox 側で検証しない（ユーザー責任）。ホストの root 相当を要求するようなセットアップを入れないこと
- 明示指定 `--extra <path>` の場合はファイル存在必須（無ければエラー）。自動探索 `--extra` 省略時は無ければ本体のみでビルドする

**例:**

```dockerfile
# ~/.ccbox/extra.Dockerfile
USER root
RUN apt-get update && apt-get install -y --no-install-recommends \
      tmux inotify-tools python3-venv jq vim xxd keychain locales \
    && sed -i '/en_US.UTF-8/s/^# //g' /etc/locale.gen && locale-gen \
    && rm -rf /var/lib/apt/lists/*
USER ccbox
```

複数の拡張を使い分けたい場合は `ccbox build --tag ccbox:myextra` のようにタグを分ける（本体ビルドはデフォルトタグのまま）。実行時は `CCBOX_IMAGE` 環境変数でどのタグを使うか切り替える:

```sh
ccbox build --tag ccbox:myextra --extra ~/.ccbox/extra.myextra.Dockerfile

# 使い捨て実行: 環境変数がそのプロセスに効く
CCBOX_IMAGE=ccbox:myextra ccbox
CCBOX_IMAGE=ccbox:myextra ccbox shell

# SSH 登録: 登録時のイメージ指定が ssh_config に永続化される
CCBOX_IMAGE=ccbox:myextra ccbox ssh
```

**SSH 経路でのイメージ指定は登録時に固定される。** Mac App や `ssh` コマンドが `ProxyCommand` を起動する時点ではシェルの環境変数が引き継がれないため、`ccbox ssh` 実行時の `CCBOX_IMAGE` を `~/.ccbox/ssh/config` の `ProxyCommand ... --image <tag>` として書き込む。

```sshconfig
Host ccbox-myapp
  ...
  ProxyCommand "/usr/local/bin/ccbox" ssh-proxy "/Users/x/myapp" --image "ccbox:myextra"
```

イメージを変えたいときは `CCBOX_IMAGE` を変えて `ccbox ssh` を再実行して登録を上書きする。既存の常駐コンテナが別イメージで動いている場合は、それを検出して次のエラーで中止する（コンテナ内の状態を壊さないよう自動再作成はしない）:

```
既存コンテナ ccbox-myapp-xxxxxxxx のイメージ "ccbox:latest" が要求 "ccbox:myextra" と一致しません。
`ccbox down && ccbox ssh` で作り直してください
```

**その他の制約:**
- `CCBOX_IMAGE` で指定したイメージが未ビルドの場合は自動ビルドせずエラー終了する（デフォルトタグのみ自動ビルド対象）
- タグ名は `[A-Za-z0-9._:/-]` のみ許可。`ProxyCommand` はシェル経由で実行されるため、シェル特殊文字を含むタグは登録時に拒否する

## マルチプロジェクトマウント（常駐コンテナのみ）

`ccbox ssh` で起動する常駐コンテナには、`~/.ccbox/projects.yaml` 経由で追加のディレクトリを bind mount できる。隣接プロジェクトの参照や、`~/Desktop` などのスクリーンショット共有ディレクトリをコンテナに見せるための機構。

```sh
# ホスト側の絶対パス（同一絶対パスで mount される）
ccbox mount add /Users/x/git/github.com/other-org/other-repo

# 別のコンテナパスを明示指定 / read-only
ccbox mount add /Users/x/Desktop --container /Users/x/Desktop --ro

# 現在の登録一覧
ccbox mount list

# 削除
ccbox mount rm /Users/x/Desktop
```

**制約:**
- 使い捨て実行（`ccbox` / `ccbox shell` / `ccbox codex`）には反映されない。カレントディレクトリだけを mount する原則を維持
- 各エントリはホームディレクトリ露出ガード・`:` チェック・存在確認をパスした場合のみ有効化される。不正なエントリはロード時に stderr へ警告を出しつつスキップし、他のエントリは有効化される
- **projects.yaml を変更した後は `ccbox down && ccbox ssh` で常駐コンテナを作り直すこと。**既存コンテナは再作成せずそのまま使うため、追加/削除は次回起動時にしか反映されない

## UX 設定の read-only bind mount

ホストの `~/.tmux.conf` は自動でコンテナ側 `/home/ccbox/.tmux.conf` に **read-only** で bind mount される。ホームディレクトリ全体を露出させずに、tmux のキーバインドや外観設定だけをコンテナと共有するための機構。

**対象（デフォルト）:** `~/.tmux.conf`

**禁止リスト（bind mount しない）:**
- `~/.ssh/*` / `~/.gnupg/*` / `~/.aws/*`
- `~/.config/gh` / `~/.config/glab-cli`
- `~/.gitconfig`（credential.helper 経由で GitHub 権限が漏れる可能性）

シンボリックリンクの解決先が禁止リストに触れる場合も拒否する。存在しないファイルは黙ってスキップされる。将来的にホワイトリスト追加のための `~/.ccbox/config.yaml` を導入予定（現状はデフォルトのみ）。

## 制限事項

- SSH 接続はコンテナ内 sshd を経由する。接続できるのはこの Mac のローカルユーザーのみ（ポート非公開、`docker exec` 経由）
- 実行対象のローカルイメージ（既定 `ccbox:latest`、`CCBOX_IMAGE` 指定時はそのタグ）は内容を検証せずに信頼して実行する。同名タグを別イメージで上書きされる環境では注意
- カレントディレクトリのパスに `:` が含まれる場合は、docker の `-v` 構文で安全に mount できないため明示エラーで中止する
- ネットワークは隔離しない（API 通信・パッケージ取得のため）。隔離対象はファイルシステムのみ

## ライセンス

MIT
