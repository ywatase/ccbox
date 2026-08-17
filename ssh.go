package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// projectSlug はプロジェクトディレクトリ名を ssh エイリアス・コンテナ名に使える形に正規化する。
// docker のコンテナ名と ssh_config の Host 名の両方で安全な [a-z0-9-] のみを許可する。
func projectSlug(path string) string {
	base := strings.ToLower(filepath.Base(path))
	var b strings.Builder
	prevHyphen := false
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 20 {
		s = strings.Trim(s[:20], "-")
	}
	if s == "" {
		return "project"
	}
	return s
}

// projectHash はプロジェクトの絶対パスから決定的な短い識別子を作る。
// slug が同名のプロジェクト同士を区別する。
func projectHash(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])[:8]
}

// containerName は常駐コンテナ名。ssh-proxy がパスだけから再導出できるよう常にハッシュを含める。
func containerName(path string) string {
	return "ccbox-" + projectSlug(path) + "-" + projectHash(path)
}

// hostAlias はユーザーが ssh <alias> で使う名前。読みやすさ優先でハッシュなし。
// 別パスの同名プロジェクトと衝突した場合は呼び出し側が containerName に切り替える。
func hostAlias(path string) string {
	return "ccbox-" + projectSlug(path)
}

// validateSSHProjectPath は ssh config の ProxyCommand に埋め込んでも安全なパスか検査する。
// ProxyCommand はシェル経由で実行されるため、$ やバッククォートを含むパスは登録時に
// 拒否する（Go の %q は " と \ しかエスケープしない）。制御文字（改行等）は
// config へのエントリ注入になるため同様に拒否する。
func validateSSHProjectPath(path string) error {
	if strings.ContainsAny(path, "$`\"\\") {
		return fmt.Errorf("パスにシェルで解釈される文字（$ ` \" \\）が含まれるため SSH 登録できません: %s", path)
	}
	for _, r := range path {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("パスに制御文字が含まれるため SSH 登録できません: %q", path)
		}
	}
	return nil
}

const includeLine = "Include ~/.ccbox/ssh/config"

// renderHostEntry は ~/.ccbox/ssh/config に書く 1 ホスト分のエントリを生成する。
// ProxyCommand は App が起動する ssh から実行されるため絶対パスを引用符付きで埋め込む。
// ControlMaster no はユーザーの多重化設定との相乗りを防ぐ（コンテナ再作成時に
// stale master が残り、設定変更が反映されない事故を PoC で確認済み）。
// image が非空なら "--image <tag>" として ProxyCommand に含める。呼び出し側で
// validateImageTag を通過していることを前提とする（シェル特殊文字を含まない）。
func renderHostEntry(alias, ccboxPath, projectPath, image string) string {
	proxyCmd := fmt.Sprintf("  ProxyCommand %q ssh-proxy %q", ccboxPath, projectPath)
	if image != "" {
		proxyCmd += fmt.Sprintf(" --image %q", image)
	}
	return "Host " + alias + "\n" +
		"  User ccbox\n" +
		"  IdentityFile ~/.ccbox/ssh/id_ed25519\n" +
		"  UserKnownHostsFile ~/.ccbox/ssh/known_hosts\n" +
		"  StrictHostKeyChecking yes\n" +
		"  ForwardAgent no\n" +
		"  ForwardX11 no\n" +
		"  ControlMaster no\n" +
		"  ControlPath none\n" +
		proxyCmd + "\n"
}

// upsertHostEntry は管理ファイル内の同名エントリを置換、無ければ追記する。
// 同名 alias が別プロジェクトパスを指している場合は、一意な containerName を
// alias にしたエントリとして登録し直す（既存エントリは壊さない）。
func upsertHostEntry(configPath, alias, projectPath, entry string) (string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	blocks := splitHostBlocks(string(data))

	finalAlias := alias
	for _, b := range blocks {
		if blockAlias(b) == alias && !strings.Contains(b, fmt.Sprintf("ssh-proxy %q", projectPath)) {
			// 別パスが同じ alias を使用中 → 一意名へフォールバックして作り直す
			finalAlias = containerName(projectPath)
			entry = strings.Replace(entry, "Host "+alias+"\n", "Host "+finalAlias+"\n", 1)
			break
		}
	}

	var out []string
	replaced := false
	for _, b := range blocks {
		if blockAlias(b) == finalAlias {
			out = append(out, entry)
			replaced = true
		} else {
			out = append(out, b)
		}
	}
	if !replaced {
		out = append(out, entry)
	}
	content := "# ccbox managed - このファイルは ccbox が生成・管理します\n\n" +
		strings.Join(out, "\n")
	return finalAlias, os.WriteFile(configPath, []byte(content), 0600)
}

// splitHostBlocks は config を "Host " 行を境にブロック分割する。コメント行は捨てる。
func splitHostBlocks(s string) []string {
	var blocks []string
	var cur strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "Host ") {
			if cur.Len() > 0 {
				blocks = append(blocks, strings.TrimRight(cur.String(), "\n")+"\n")
			}
			cur.Reset()
		}
		if cur.Len() == 0 && !strings.HasPrefix(line, "Host ") {
			continue // ブロック外（ヘッダコメント・空行）は読み捨てて再生成する
		}
		cur.WriteString(line + "\n")
	}
	if strings.TrimSpace(cur.String()) != "" {
		blocks = append(blocks, strings.TrimRight(cur.String(), "\n")+"\n")
	}
	return blocks
}

func blockAlias(block string) string {
	first := strings.SplitN(block, "\n", 2)[0]
	return strings.TrimSpace(strings.TrimPrefix(first, "Host "))
}

// sshConfigHasInclude は ~/.ssh/config が ccbox の Include を含むか調べる。
// ファイルが存在しない場合は false（エラーではない）。
func sshConfigHasInclude(userConfigPath string) (bool, error) {
	data, err := os.ReadFile(userConfigPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, ".ccbox/ssh/config") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			return true, nil
		}
	}
	return false, nil
}

// prependInclude は Include 行をユーザー config の先頭に挿入する。
// 末尾追記だと先行する Host ブロックのスコープに入ってしまうため必ず先頭。
func prependInclude(userConfigPath string) error {
	data, err := os.ReadFile(userConfigPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(userConfigPath), 0700); err != nil {
		return err
	}
	content := includeLine + "\n\n" + string(data)
	return os.WriteFile(userConfigPath, []byte(content), 0600)
}

// sshdConfig は非 root・inetd モード用。パスワード認証は無効、公開鍵のみ。
// AllowTcpForwarding は App のポートフォワーディング（codex login 含む）に必要。
const sshdConfig = `HostKey /home/ccbox/.ssh/host_ed25519
PubkeyAuthentication yes
AuthorizedKeysFile /home/ccbox/.ssh/authorized_keys
PasswordAuthentication no
KbdInteractiveAuthentication no
UsePAM no
AllowTcpForwarding yes
AllowAgentForwarding no
Subsystem sftp internal-sftp
`

// codexConfigSeed はコンテナ自体を隔離境界とみなし、codex 自身の sandbox
// （Docker 内では bubblewrap が動かない）を無効化する既定値。
const codexConfigSeed = `# ccbox: コンテナ自体が隔離境界のため codex 自身の sandbox は無効化する
sandbox_mode = "danger-full-access"
`

// ensureSSHAssets は SSH 接続に必要な鍵・設定一式を冪等に生成する。
// クライアント側は ~/.ccbox/ssh、コンテナから見える側は ~/.ccbox/home/.ssh に置く。
func ensureSSHAssets(home string) error {
	sshDir := filepath.Join(home, ".ccbox", "ssh")
	homeSSH := filepath.Join(home, ".ccbox", "home", ".ssh")
	codexDir := filepath.Join(home, ".ccbox", "home", ".codex")
	for _, d := range []string{sshDir, homeSSH, codexDir} {
		if err := os.MkdirAll(d, 0700); err != nil {
			return fmt.Errorf("%s を作成できません: %w", d, err)
		}
		// 鍵材料を含むため、既存ディレクトリの権限も 0700 に揃える
		if err := os.Chmod(d, 0700); err != nil {
			return fmt.Errorf("%s の権限を設定できません: %w", d, err)
		}
	}

	clientKey := filepath.Join(sshDir, "id_ed25519")
	if err := ensureKeyPair(clientKey, "ccbox-client"); err != nil {
		return err
	}
	hostKey := filepath.Join(homeSSH, "host_ed25519")
	if err := ensureKeyPair(hostKey, "ccbox-host"); err != nil {
		return err
	}

	pub, err := os.ReadFile(clientKey + ".pub")
	if err != nil {
		return fmt.Errorf("クライアント公開鍵を読めません（鍵ペアが不完全な場合は %s と %s.pub を削除して再実行してください）: %w", clientKey, clientKey, err)
	}
	if err := os.WriteFile(filepath.Join(homeSSH, "authorized_keys"), pub, 0600); err != nil {
		return fmt.Errorf("authorized_keys を書き込めません: %w", err)
	}

	// known_hosts を事前生成して初回接続の指紋確認プロンプトを出さない。
	// エイリアスは全て ccbox-* なのでワイルドカード 1 行で足りる。
	hostPub, err := os.ReadFile(hostKey + ".pub")
	if err != nil {
		return fmt.Errorf("ホスト公開鍵を読めません（鍵ペアが不完全な場合は %s と %s.pub を削除して再実行してください）: %w", hostKey, hostKey, err)
	}
	fields := strings.Fields(string(hostPub))
	if len(fields) < 2 {
		return fmt.Errorf("ホスト公開鍵の形式が不正です: %s", hostKey+".pub")
	}
	kh := "ccbox-* " + fields[0] + " " + fields[1] + "\n"
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(kh), 0600); err != nil {
		return fmt.Errorf("known_hosts を書き込めません: %w", err)
	}

	if err := os.WriteFile(filepath.Join(homeSSH, "sshd_config"), []byte(sshdConfig), 0600); err != nil {
		return fmt.Errorf("sshd_config を書き込めません: %w", err)
	}

	codexToml := filepath.Join(codexDir, "config.toml")
	switch _, err := os.Stat(codexToml); {
	case err == nil:
		// 既存のユーザー設定は尊重して触らない
	case os.IsNotExist(err):
		if err := os.WriteFile(codexToml, []byte(codexConfigSeed), 0600); err != nil {
			return fmt.Errorf("codex 設定の雛形を書き込めません: %w", err)
		}
	default:
		return fmt.Errorf("%s を確認できません: %w", codexToml, err)
	}
	return nil
}

// ensureKeyPair は鍵が無ければ ssh-keygen（macOS 標準）で ed25519 鍵を生成する。
func ensureKeyPair(path, comment string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-C", comment, "-q", "-f", path)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ssh-keygen による鍵生成に失敗しました (%s): %w", path, err)
	}
	return nil
}

// cmdSSHRegister はカレントディレクトリを Mac App から SSH 接続可能として登録する。
func cmdSSHRegister() error {
	pwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("カレントディレクトリを取得できません: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("ホームディレクトリを取得できません: %w", err)
	}
	if err := validateProjectDir(pwd, home); err != nil {
		return err
	}
	if err := validateSSHProjectPath(pwd); err != nil {
		return err
	}

	if _, err := ensureCcboxHome(); err != nil {
		return err
	}
	if err := ensureSSHAssets(home); err != nil {
		return err
	}

	ccboxPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("ccbox 自身のパスを取得できません: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(ccboxPath); err == nil {
		ccboxPath = resolved
	}

	// CCBOX_IMAGE を ProxyCommand に永続化する。env は SSH セッションを跨がないため、
	// 登録時に文字列として ssh_config に埋め込むことで App/ssh からの起動時にも効かせる。
	registeredImage := os.Getenv("CCBOX_IMAGE")
	if registeredImage != "" {
		if err := validateImageTag(registeredImage); err != nil {
			return fmt.Errorf("CCBOX_IMAGE: %w", err)
		}
	}

	alias := hostAlias(pwd)
	entry := renderHostEntry(alias, ccboxPath, pwd, registeredImage)
	managedConfig := filepath.Join(home, ".ccbox", "ssh", "config")
	finalAlias, err := upsertHostEntry(managedConfig, alias, pwd, entry)
	if err != nil {
		return fmt.Errorf("%s を更新できません: %w", managedConfig, err)
	}

	userConfig := filepath.Join(home, ".ssh", "config")
	hasInclude, err := sshConfigHasInclude(userConfig)
	if err != nil {
		return fmt.Errorf("%s を確認できません: %w", userConfig, err)
	}
	if !hasInclude {
		if isTTY() && confirm(fmt.Sprintf("~/.ssh/config の先頭に %q を追記しますか?", includeLine)) {
			if err := prependInclude(userConfig); err != nil {
				return fmt.Errorf("~/.ssh/config を更新できません: %w", err)
			}
			fmt.Println("~/.ssh/config に Include 行を追記しました。")
		} else {
			fmt.Printf("~/.ssh/config の先頭に次の 1 行を追加してください（Host ブロックより前に置くこと）:\n\n  %s\n\n", includeLine)
		}
	}

	fmt.Printf(`登録しました: %s

Mac App からの接続:
  Claude App / Codex App の接続設定に %q が表示されます。
  プロジェクトフォルダには %s を選択してください。

codex の初回認証（未認証の場合）:
  ssh -t -L 1455:localhost:1455 %s codex login

コンテナの停止:
  ccbox down
`, finalAlias, finalAlias, pwd, finalAlias)
	return nil
}

// confirm は y/N プロンプトを表示して y のときだけ true を返す。
func confirm(prompt string) bool {
	fmt.Printf("%s [y/N]: ", prompt)
	var answer string
	fmt.Scanln(&answer)
	return strings.EqualFold(strings.TrimSpace(answer), "y")
}
