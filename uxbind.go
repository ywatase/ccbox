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

// uxDenylist は絶対に bind mount させない秘密漏洩リスクのあるパス。
// EvalSymlinks で解決した実パスに対して判定するため、シンボリックリンクによる回避を防ぐ。
// ホーム相対パス、ディレクトリはその配下も含めて禁止する。
var uxDenylist = []string{
	".ssh",
	".gnupg",
	".aws",
	".config/gh",
	".config/glab-cli",
	".gitconfig",
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

// isDeniedUXPath は resolvedPath（EvalSymlinks 済み絶対パス）が禁止リストに触れるかを判定する。
// home 側も EvalSymlinks で解決してから比較する。macOS では /var/folders/... → /private/var/folders/...
// のようにシステム側のパス正規化が入り、単純な文字列比較では bypass 可能になるため。
func isDeniedUXPath(resolvedPath, home string) bool {
	cleanResolved := filepath.Clean(resolvedPath)
	realHome := home
	if r, err := filepath.EvalSymlinks(home); err == nil {
		realHome = r
	}
	for _, denied := range uxDenylist {
		for _, base := range dedupSlice(home, realHome) {
			deniedAbs := filepath.Clean(filepath.Join(base, denied))
			if cleanResolved == deniedAbs {
				return true
			}
			rel, err := filepath.Rel(deniedAbs, cleanResolved)
			if err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
				return true
			}
		}
	}
	return false
}

func dedupSlice(a, b string) []string {
	if a == b {
		return []string{a}
	}
	return []string{a, b}
}
