# 設計: ccbox への codex 追加 + Mac App からの SSH 接続対応

日付: 2026-07-07
ステータス: 承認済み・Phase 0 PoC 完了（Claude App / Codex App とも接続成功を実機確認）

## 目的

1. ccbox コンテナ内で codex CLI を実行できるようにする
2. Mac の Codex App / Claude App から SSH でコンテナに接続し、
   リモート開発機能（app-server、ポートフォワーディング、レビュー）をフルに使えるようにする

## 前提（調査結果）

- Codex App のリモート SSH 接続（v26.415〜、アルファ）は Mac の `~/.ssh/config` の
  具体的な Host エイリアスを一覧表示し、接続解決を OpenSSH に委ねる。
  ワイルドカードのみのエントリは無視される
- 接続後、App が SSH 経由でリモートの `codex` CLI（app-server）をログインシェルで起動し、
  SSH の標準入出力上で通信する。app-server 用の追加ポート公開は不要
- リモート要件: `codex` CLI がインストール済み・認証済みで、ログインシェルの PATH にあること
- 公式要件: app-server を共有/公開ネットワークに露出させないこと
- Mac App の ssh_config 対応状況（ユーザー検証済み）:
  - Claude App: ProxyCommand 可 / ProxyJump・RemoteCommand 不可
  - Codex App: ProxyJump・ProxyCommand 可 / RemoteCommand 未検証

## 決定事項

| 論点 | 決定 |
|---|---|
| 接続方式 | ProxyCommand + `docker exec` 経由の sshd inetd モード（ポート非公開） |
| ssh/config 管理 | `~/.ssh/config` に `Include ~/.ccbox/ssh/config` を初回のみ確認付きで追記。エントリ本体は ccbox が全自動管理 |
| コンテナ停止 | 手動のみ（`ccbox down`）。アイドル自動停止はしない |
| codex sandbox | `sandbox_mode = "danger-full-access"` を雛形 config で既定化（コンテナ自体が隔離境界） |
| codex 配布 | npm（公式配布）。サプライチェーン対策として release cooldown を適用（下記） |

## アーキテクチャ

```
Mac (Codex App / Claude App / ssh CLI)
  └─ ~/.ssh/config … Include ~/.ccbox/ssh/config
       └─ Host ccbox-myapp
            ProxyCommand ccbox ssh-proxy /path/to/myapp
                 └─ 常駐コンテナ（無ければ自動起動、sleep infinity で待機）
                      └─ docker exec -i … /usr/sbin/sshd -i （接続ごとに inetd モード起動）
                           └─ codex app-server / bash / ポートフォワーディング
```

- リッスンポートはホスト・コンテナともゼロ。SSH トランスポートは `docker exec` の標準入出力
- 既存の使い捨て実行（`ccbox` / `ccbox shell`）は変更なし。常駐コンテナは SSH 用に別枠で管理
  （`~/.ccbox/home` マウントは共有）
- 常駐コンテナには既存 `buildRunArgs` と同等のセキュリティフラグ
  （`--security-opt no-new-privileges`、`--pids-limit 1024`、マウントガード）を適用

## Dockerfile の変更

- `openssh-server` を apt 追加（sshd バイナリのみ使用、サービスは起動しない）
- codex CLI を npm でインストール。サプライチェーン対策として `--before` による
  release cooldown（7日）を適用する:

  ```dockerfile
  RUN npm install -g @openai/codex \
      --before="$(date -u -d '7 days ago' '+%Y-%m-%dT%H:%M:%SZ')"
  ```

  公開から 7 日未満のバージョンをインストール対象から除外し、
  乗っ取り直後の悪意あるリリースを踏むリスクを下げる。`ccbox update` で最新化される点は claude と同じ

**制約**: `/home/ccbox` は実行時にホストの `~/.ccbox/home` で覆われるため、
ビルド時にホームへ置いたファイルは実行時に見えない。設定ファイルはすべて ccbox CLI がホスト側で生成する。

## SSH 基盤（ccbox CLI がホスト側で生成・永続化）

| ファイル | 用途 |
|---|---|
| `~/.ccbox/ssh/id_ed25519(.pub)` | クライアント鍵（全プロジェクト共通、初回に生成） |
| `~/.ccbox/ssh/known_hosts` | ホスト公開鍵から事前生成（TOFU プロンプトを出さない） |
| `~/.ccbox/ssh/config` | Host エントリ群（ccbox が自動管理） |
| `~/.ccbox/home/.ssh/host_ed25519` | sshd ホスト鍵（コンテナ内から見える位置） |
| `~/.ccbox/home/.ssh/authorized_keys` | クライアント公開鍵 |
| `~/.ccbox/home/.ssh/sshd_config` | 非 root・inetd モード用設定 |
| `~/.ccbox/home/.codex/config.toml` | 無ければ雛形生成（sandbox 無効化） |

sshd_config の要点: 公開鍵認証のみ（パスワード禁止）、`AllowTcpForwarding yes`、
`Subsystem sftp internal-sftp`。鍵生成は Mac 標準の `ssh-keygen` を利用。

## サブコマンド

| コマンド | 動作 |
|---|---|
| `ccbox codex [args...]` | 使い捨てコンテナで codex を実行（`ccbox` の codex 版） |
| `ccbox ssh` | カレントディレクトリを SSH 接続可能として登録。鍵類の初回生成 → Host エントリ追記 → Include 行の確認付き追記 → エイリアス表示 |
| `ccbox ssh-proxy <絶対パス>` | （ProxyCommand 専用・内部用）常駐コンテナが無ければ起動し `docker exec -i` で sshd -i に接続 |
| `ccbox ps` | 常駐コンテナの一覧 |
| `ccbox down [パス]` | 常駐コンテナの停止・削除（省略時カレントディレクトリ） |

- エイリアス・コンテナ名は `ccbox-<ディレクトリ名>`、衝突時はパスの短ハッシュを付加
- ProxyCommand には ccbox と docker の絶対パスを書く（App の ssh は PATH が最小のため）
- 新サブコマンド語を claude に渡したい場合は従来通り `ccbox --` でエスケープ

## 初回認証フロー（codex）

codex login はブラウザ認証で `localhost:1455` を使うため、SSH ポートフォワーディングで解決する:

```sh
ccbox ssh                                  # プロジェクト登録
ssh -L 1455:localhost:1455 ccbox-myapp     # Mac から接続
codex login                                # 表示された URL を Mac のブラウザで開く
```

認証情報は `~/.ccbox/home/.codex/` に永続化され、全プロジェクトで共有（claude と同じモデル）。

## エラー処理・セキュリティ

- `ccbox ssh` にもホームディレクトリ露出ガード（`isUnsafeMountDir`）を適用
- `~/.ssh/config` への Include 追記は初回のみ・確認プロンプト付き
- パスの `:` は既存同様エラー。空白は config 生成時に引用符で対応
- `docker exec` 経由のため接続可能なのは Mac のローカルユーザーのみ。ネットワーク露出ゼロ

## 検証計画

- **Phase 0（手動 PoC）**: 手書き config + 手動起動コンテナで以下を確認してから Go 実装に入る
  1. `ssh` CLI で ProxyCommand + `docker exec sshd -i` 接続が通るか
  2. Codex App が ProxyCommand エントリを認識し app-server を起動できるか（要 codex login）
  3. Claude App からも接続できるか
  4. ポートフォワーディング（`-L`）が動くか
- Go 実装: エイリアス生成・config 生成・引数解析をユニットテストでカバー（既存スタイル踏襲）

## Phase 0 PoC の結果（2026-07-07 実施、全項目成功）

`ssh` CLI・Claude App・Codex App のすべてから接続成功。ただし以下の問題を踏み、
設計に追加要件が入った。

### 発見1: VirtioFS 上の Unix ソケットへの chmod は EINVAL で失敗する（最重要）

macOS バインドマウント（VirtioFS）上では、Unix ドメインソケットの作成・接続はできるが
**ソケットファイルへの chmod が `EINVAL` で失敗する**。これにより:

- Claude App: リモートデーモンが `~/.claude/remote/run/<id>/rpc.sock` の chmod に失敗して死亡
  （症状: `timeout waiting for daemon to accept`）
- Codex App: app-server が `~/.codex/app-server-control/` のソケット chmod に失敗して死亡
  （症状: `socket hang up` / ログに `Error: Invalid argument (os error 22)`）

**対策（実装要件）**: 常駐コンテナ起動時にソケットが作られるディレクトリを tmpfs で覆う。

```sh
--mount type=tmpfs,destination=/home/ccbox/.claude/remote/run,tmpfs-mode=0700
--mount type=tmpfs,destination=/home/ccbox/.codex/app-server-control,tmpfs-mode=0700
```

tmpfs は root 所有でマウントされるため、起動直後に
`docker exec -u root <name> chown ccbox:ccbox <両ディレクトリ>` を実行する。
ソケット・トークン・ロックは実行時状態なので tmpfs（非永続）で問題ない。
auth.json や sqlite 等の永続状態はバインドマウント側に残る。

### 発見2: ユーザーの ControlMaster 設定が接続に相乗りする

ユーザーの ssh 設定に `ControlMaster auto` + `ControlPersist` があると、
ターミナル・Claude App・Codex App の接続が 1 本の sshd セッションに多重化される。
動作はするが、(a) sshd_config の変更が既存マスターに反映されない、
(b) コンテナ再作成後に stale なマスターソケットが残り
「ControlSocket ... already exists, disabling multiplexing」警告が出る、という運用上の注意がある。

**対策（実装要件）**: ccbox 管理の Host エントリに `ControlMaster no` と
`ControlPath none` を明示し、ccbox 接続を多重化の対象外にする
（コンテナのライフサイクルと ssh マスターの生存期間がずれるため）。

### 発見3: Claude App がアップロードする server バイナリの実行ビット

初回接続時、Claude App が sftp でアップロードした
`~/.claude/remote/srv/<hash>/server` に実行ビットが付かず
`Permission denied` になった（隣の `ccd-cli` には付いていた）。
手動 `chmod +x` 後は再発しておらず、原因は App 側の挙動か VirtioFS の
タイミング問題か特定できていない。**再発時の既知の対処として README/トラブルシューティングに記載する**。

### その他の確認事項

- 非 root sshd（inetd モード、ユーザー所有ホスト鍵）は問題なく動作
- `codex login` は `ssh -t -L 1455:localhost:1455 <host> codex login` で完結
  （`codex login --device-auth` でも可）
- VirtioFS 上でも flock・共有 mmap・sqlite WAL・ソケット往復・バイナリ実行は正常動作
  （壊れるのはソケットへの chmod のみ）
- npm の release cooldown は `--before="$(date -u -d '7 days ago' ...)"` で機能
  （PoC 時点: latest 0.142.5 に対し 0.142.4 が入った）
