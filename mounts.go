package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Mount は projects.yaml の 1 エントリ。
// Host はホスト側の絶対パス、Container はコンテナ側マウント先（省略時は Host と同じ）、
// Readonly は :ro を付けるかどうか。ccbox の隔離原則に合わせ、ホスト側パスは
// 常に絶対で保存する（相対だとカレントディレクトリ次第で意味が変わる）。
type Mount struct {
	Host      string `yaml:"host"`
	Container string `yaml:"container,omitempty"`
	Readonly  bool   `yaml:"readonly,omitempty"`
}

// MountsConfig は projects.yaml 全体。
type MountsConfig struct {
	Mounts []Mount `yaml:"mounts"`
}

// mountsConfigPath は ~/.ccbox/projects.yaml のパス。
func mountsConfigPath(home string) string {
	return filepath.Join(home, ".ccbox", "projects.yaml")
}

// loadMounts は projects.yaml を読む。ファイルが無ければ空設定を返す（エラーではない）。
func loadMounts(home string) (*MountsConfig, error) {
	path := mountsConfigPath(home)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &MountsConfig{}, nil
		}
		return nil, fmt.Errorf("%s を読めません: %w", path, err)
	}
	cfg := &MountsConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("%s のパースに失敗: %w", path, err)
	}
	return cfg, nil
}

// saveMounts は projects.yaml を書き出す。~/.ccbox は 0700 で作成、ファイルは 0600。
func saveMounts(home string, cfg *MountsConfig) error {
	base := filepath.Join(home, ".ccbox")
	if err := os.MkdirAll(base, 0700); err != nil {
		return fmt.Errorf("~/.ccbox を作成できません: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("YAML への変換に失敗: %w", err)
	}
	path := mountsConfigPath(home)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("%s を書き込めません: %w", path, err)
	}
	return nil
}

// findIndex は Host 一致のエントリの index を返す。無ければ -1。
func (c *MountsConfig) findIndex(host string) int {
	clean := filepath.Clean(host)
	for i, m := range c.Mounts {
		if filepath.Clean(m.Host) == clean {
			return i
		}
	}
	return -1
}

// addOrUpdate は Host 一致で既存を上書き、無ければ追加する。
func (c *MountsConfig) addOrUpdate(m Mount) {
	if idx := c.findIndex(m.Host); idx >= 0 {
		c.Mounts[idx] = m
		return
	}
	c.Mounts = append(c.Mounts, m)
}

// remove は Host 一致で削除する。削除したエントリが存在すれば true。
func (c *MountsConfig) remove(host string) bool {
	idx := c.findIndex(host)
	if idx < 0 {
		return false
	}
	c.Mounts = slices.Delete(c.Mounts, idx, idx+1)
	return true
}

// containerHome はコンテナ側のホームディレクトリ。ここへの上書き mount は
// ccbox の隔離設計を破壊するため、projects.yaml から指定できない。
const containerHome = "/home/ccbox"

// validateMount は 1 エントリを検査する。使い捨て実行と同じ安全チェックを各エントリに適用。
// projects.yaml 経由でも認証情報の露出を許さないため、ホームディレクトリやその祖先・
// isolationDenylist 相当のホスト側パスへの mount を拒否する。container 側も /home/ccbox
// 完全一致およびその配下（~/.ccbox/home のマウントを上書きする形）を拒否する。
func validateMount(m Mount, home string) error {
	if m.Host == "" {
		return errors.New("host は必須です")
	}
	if !filepath.IsAbs(m.Host) {
		return fmt.Errorf("host は絶対パスで指定してください: %s", m.Host)
	}
	if strings.Contains(m.Host, ":") {
		return fmt.Errorf("host に ':' が含まれるため docker の -v 構文で安全にマウントできません: %s", m.Host)
	}
	if m.Container != "" {
		if !filepath.IsAbs(m.Container) {
			return fmt.Errorf("container は絶対パスで指定してください: %s", m.Container)
		}
		if strings.Contains(m.Container, ":") {
			return fmt.Errorf("container に ':' が含まれるため docker の -v 構文で安全にマウントできません: %s", m.Container)
		}
		if err := validateContainerPath(m.Container); err != nil {
			return err
		}
	} else {
		// container 省略時は host と同じ絶対パスに mount するため、その値でも判定
		if err := validateContainerPath(m.Host); err != nil {
			return err
		}
	}
	if _, err := os.Stat(m.Host); err != nil {
		return fmt.Errorf("host パスが存在しないかアクセスできません: %s: %w", m.Host, err)
	}
	mountHost := m.Host
	if resolved, err := filepath.EvalSymlinks(m.Host); err == nil {
		mountHost = resolved
	}
	mountHome := home
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		mountHome = resolved
	}
	if isUnsafeMountDir(mountHost, mountHome) {
		return fmt.Errorf("host %s はホームディレクトリ全体を露出させるため許可されません", m.Host)
	}
	if isDeniedUXPath(mountHost, mountHome) {
		return fmt.Errorf("host %s は認証情報を含む隔離対象のため mount できません（isolationDenylist）", m.Host)
	}
	return nil
}

// validateContainerPath は container 側のマウント先が予約領域（コンテナホーム）に
// 触れていないかを判定する。/home/ccbox 完全一致・その配下は認証情報の永続化領域
// （~/.ccbox/home の上書き）となるため拒否する。
func validateContainerPath(p string) error {
	clean := filepath.Clean(p)
	if clean == containerHome {
		return fmt.Errorf("container %s はコンテナホームそのものへの上書きになるため許可されません", p)
	}
	rel, err := filepath.Rel(containerHome, clean)
	if err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
		return fmt.Errorf("container %s は /home/ccbox 配下の予約領域のため mount できません", p)
	}
	return nil
}

// mountToDockerArg は 1 エントリを docker の "-v" 引数値に変換する。
// container 省略時は host と同じパス（ccbox の同一絶対パスマウント原則）。
func mountToDockerArg(m Mount) string {
	container := m.Container
	if container == "" {
		container = m.Host
	}
	spec := m.Host + ":" + container
	if m.Readonly {
		spec += ":ro"
	}
	return spec
}

// mountsToDockerArgs は全エントリを検証して docker run 引数列（-v ペア列）を返す。
// 検証エラーがあればそのエントリだけスキップして stderr に警告し、他は続行する
// （1 エントリの不備で常駐コンテナ全体が起動できないと運用しづらいため）。
func mountsToDockerArgs(cfg *MountsConfig, home string) []string {
	var args []string
	for _, m := range cfg.Mounts {
		if err := validateMount(m, home); err != nil {
			fmt.Fprintf(os.Stderr, "警告: projects.yaml のエントリをスキップ: %v\n", err)
			continue
		}
		args = append(args, "-v", mountToDockerArg(m))
	}
	return args
}

// loadExtraMountArgs は projects.yaml から追加マウントの docker run 引数列を返す。
// projects.yaml が無ければ空リスト（エラーではない）。
func loadExtraMountArgs(home string) ([]string, error) {
	cfg, err := loadMounts(home)
	if err != nil {
		return nil, err
	}
	return mountsToDockerArgs(cfg, home), nil
}

// runMount は "ccbox mount ..." サブコマンドを処理する。
func runMount(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("使い方: ccbox mount <add|rm|list> ...")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("ホームディレクトリを取得できません: %w", err)
	}
	switch args[0] {
	case "add":
		return mountAdd(home, args[1:])
	case "rm", "remove":
		return mountRemove(home, args[1:])
	case "list", "ls":
		return mountList(home)
	default:
		return fmt.Errorf("不明なサブコマンドです: mount %s", args[0])
	}
}

// mountAdd は "ccbox mount add <host> [--container <path>] [--ro]" を処理する。
func mountAdd(home string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("使い方: ccbox mount add <host> [--container <path>] [--ro]")
	}
	m := Mount{Host: args[0]}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--container":
			if i+1 >= len(args) {
				return fmt.Errorf("--container に値が必要です")
			}
			m.Container = args[i+1]
			i++
		case "--ro", "--readonly":
			m.Readonly = true
		default:
			return fmt.Errorf("不明なフラグ: %s", args[i])
		}
	}
	// 絶対パスに正規化（`.` 等の相対指定を許可）
	if abs, err := filepath.Abs(m.Host); err == nil {
		m.Host = abs
	}
	if err := validateMount(m, home); err != nil {
		return err
	}
	cfg, err := loadMounts(home)
	if err != nil {
		return err
	}
	cfg.addOrUpdate(m)
	if err := saveMounts(home, cfg); err != nil {
		return err
	}
	fmt.Printf("追加しました: %s\n", mountToDockerArg(m))
	fmt.Println("注意: 既に常駐コンテナが起動している場合は `ccbox down && ccbox ssh` で作り直してください。")
	return nil
}

// mountRemove は "ccbox mount rm <host>" を処理する。
func mountRemove(home string, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("使い方: ccbox mount rm <host>")
	}
	host := args[0]
	if abs, err := filepath.Abs(host); err == nil {
		host = abs
	}
	cfg, err := loadMounts(home)
	if err != nil {
		return err
	}
	if !cfg.remove(host) {
		return fmt.Errorf("エントリが見つかりません: %s", host)
	}
	if err := saveMounts(home, cfg); err != nil {
		return err
	}
	fmt.Printf("削除しました: %s\n", host)
	fmt.Println("注意: 既に常駐コンテナが起動している場合は `ccbox down && ccbox ssh` で作り直してください。")
	return nil
}

// mountList は "ccbox mount list" を処理する。
func mountList(home string) error {
	cfg, err := loadMounts(home)
	if err != nil {
		return err
	}
	if len(cfg.Mounts) == 0 {
		fmt.Println("エントリなし。 ccbox mount add <host> で追加してください。")
		return nil
	}
	for _, m := range cfg.Mounts {
		ro := ""
		if m.Readonly {
			ro = " (readonly)"
		}
		container := m.Container
		if container == "" {
			container = m.Host
		}
		fmt.Printf("  %s  →  %s%s\n", m.Host, container, ro)
	}
	return nil
}
