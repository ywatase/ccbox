package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// uxWhitelistDefault はデフォルトで bind mount する UX 設定ファイル。
// ホーム相対パス。将来 ~/.ccbox/config.yaml でユーザーが追加できるようにする（Phase 2 で対応）。
var uxWhitelistDefault = []string{".tmux.conf"}

// isolationDenylist はコンテナへの露出を許さないホスト側パス（ホーム相対）。
// UX bind mount と projects.yaml マウントの両方で source の denylist として使う。
// EvalSymlinks で解決した実パスに対して判定するため、シンボリックリンクによる回避を防ぐ。
// ディレクトリはその配下（サブディレクトリ・ファイル）も含めて禁止する。
// .claude / .codex は ~/.ccbox/home 側で永続化されるため、ホスト側の同名を
// コンテナに公開すると隔離が破壊される。
var isolationDenylist = []string{
	".ssh",
	".gnupg",
	".aws",
	".config/gh",
	".config/glab-cli",
	".gitconfig",
	".claude",
	".codex",
}

// uxBindMountArgs は UX 設定のホワイトリスト bind mount を docker run 引数列で返す。
// 存在しないファイルはスキップ（エラーにしない）。禁止リストに解決されるパスもスキップ
// して警告を stderr に出す（他のホワイトリスト項目の bind は続行）。
// mount 先はコンテナ側 /home/ccbox/<相対パス>、read-only。
func uxBindMountArgs(home string, whitelist []string) []string {
	var args []string
	for _, rel := range whitelist {
		hostPath := filepath.Join(home, rel)
		info, err := os.Stat(hostPath) // symlink を追跡
		if err != nil {
			if !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "警告: UX bind mount %s の確認に失敗: %v\n", hostPath, err)
			}
			continue
		}
		if info.IsDir() {
			fmt.Fprintf(os.Stderr, "警告: UX bind mount %s はディレクトリのためスキップ（個別ファイルのみ許可）\n", hostPath)
			continue
		}
		// symlink を追跡した実パスで禁止リスト判定（追跡前だと ~/link → ~/.ssh/id_rsa の bypass を許してしまう）
		resolved, err := filepath.EvalSymlinks(hostPath)
		if err != nil {
			// 解決失敗は保守的にスキップ
			fmt.Fprintf(os.Stderr, "警告: UX bind mount %s の symlink 解決に失敗（スキップ）: %v\n", hostPath, err)
			continue
		}
		if isDeniedUXPath(resolved, home) {
			fmt.Fprintf(os.Stderr, "警告: UX bind mount %s は禁止パス %s に解決されるためスキップ\n", hostPath, resolved)
			continue
		}
		args = append(args, "-v", hostPath+":/home/ccbox/"+rel+":ro")
	}
	return args
}

// isWithin は target が base の真の配下（base 自身は含まない）かを判定する。
// パス封じ込め判定はこの 1 箇所に集約する。素朴な strings.HasPrefix(rel, "..") は
// ".." で始まる正規のディレクトリ名（例: base/..secret）を「base の外」と誤判定して
// 封じ込めを破るため、".." 完全一致と ".." + セパレータのみを外側として扱う。
// base / target は絶対パスかつ EvalSymlinks 済みであることを前提とする。
func isWithin(base, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(target))
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// isolationDenylistCoveredBy は mountHost が isolationDenylist の何れかの項目を
// 配下に含む祖先ディレクトリなら true を返す。UX bind は単一ファイル限定なので
// この判定は mount source（ディレクトリ許容）にのみ意味を持つ。
// mountHost / mountHome は EvalSymlinks 済みであることを前提とする。
// denylist 項目の実在は問わないパス形状の判定である（~/.config は .config/gh が
// 無くても拒否する）。将来 denylist 項目が作られた時点で露出しないよう保守的に倒す。
// 例: mountHost=~/.config は .config/gh を配下に持つ形なので true（拒否）
//
//	mountHost=~/.config/nvim は denied を配下に含まないので false（許可）
func isolationDenylistCoveredBy(mountHost, mountHome string) bool {
	denied := deniedAbsPaths(mountHome)
	for _, host := range pathVariants(mountHost) {
		for _, d := range denied {
			if isWithin(host, d) {
				return true
			}
		}
	}
	return false
}

// isDeniedUXPath は candidate が禁止リストに触れるかを判定する。
// candidate は EvalSymlinks 済みで渡すのが本来だが、解決漏れが即座に検査の
// すり抜けになるため、関数側でも解決してから突き合わせる（冪等）。
func isDeniedUXPath(candidate, home string) bool {
	denied := deniedAbsPaths(home)
	for _, c := range pathVariants(candidate) {
		for _, d := range denied {
			if c == d || isWithin(d, c) {
				return true
			}
		}
	}
	return false
}

// pathVariants は p の字句上のパスと EvalSymlinks 解決後の実パスを返す（重複時は 1 つ）。
// macOS の /var → /private/var のようなシステム側の正規化や、呼び出し側の解決漏れを
// 吸収して、どちらの表記で来ても同じ判定になるようにする。
func pathVariants(p string) []string {
	clean := filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return dedupSlice(clean, filepath.Clean(resolved))
	}
	return []string{clean}
}

// deniedAbsPaths は isolationDenylist を絶対パスへ展開する。
// 各項目について「字句上のパス」と「EvalSymlinks で解決した実パス」の両方を返す。
// denylist 項目そのものがシンボリックリンク（例: ~/.ssh -> /vault/real-ssh）の場合、
// mount source 側は実パスに解決されるため、字句上のパスとだけ比較すると対応が失われて
// 隔離を回避できてしまう。実パス側も拒否対象に含めることで両者を突き合わせる。
// 項目が存在しない場合は EvalSymlinks が失敗するが、字句上のパスは常に返すため
// 「将来その項目が作られた時点で拒否する」保守的な挙動は維持される。
func deniedAbsPaths(home string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, base := range denylistBases(home) {
		for _, denied := range isolationDenylist {
			for _, v := range pathVariants(filepath.Join(base, denied)) {
				add(v)
			}
		}
	}
	return out
}

// denylistBases は denylist を展開する基点となるホームディレクトリ候補を返す。
// home 側も EvalSymlinks で解決した値を併せて返すのは、macOS では
// /var/folders/... → /private/var/folders/... のようにシステム側のパス正規化が入り、
// 解決済み実パスと未解決 home の単純比較では bypass 可能になるため。
func denylistBases(home string) []string {
	realHome := home
	if r, err := filepath.EvalSymlinks(home); err == nil {
		realHome = r
	}
	return dedupSlice(home, realHome)
}

func dedupSlice(a, b string) []string {
	if a == b {
		return []string{a}
	}
	return []string{a, b}
}
