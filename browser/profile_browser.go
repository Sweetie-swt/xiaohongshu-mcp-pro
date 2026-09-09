package browser

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/stealth"
	"github.com/sirupsen/logrus"
)

// ProfileBrowser 使用持久化 Chrome profile 目录的浏览器。
// 相比 CDP cookie 注入，Chrome 原生 profile 能跨进程、跨子域正确保持会话。
type ProfileBrowser struct {
	browser    *rod.Browser
	launcher   *launcher.Launcher
	profileDir string
	closed     bool
}

var (
	profileBrowserMu     sync.Mutex
	activeProfileBrowser = make(map[string]int)
)

var singletonFileNames = []string{
	"SingletonLock",
	"SingletonCookie",
	"SingletonSocket",
}

// NewProfileBrowser 创建带持久化 profile 目录的浏览器实例。
// profileDir 在浏览器关闭后保留，下次启动自动加载已有 session。
func NewProfileBrowser(headless bool, profileDir string, binPath string, proxy string) *ProfileBrowser {
	profileDir = normalizedProfileDir(profileDir)
	profileBrowserMu.Lock()
	defer profileBrowserMu.Unlock()

	logSingletonFiles(profileDir, "启动前")
	logrus.Infof("创建持久化 Chrome profile: %s", profileDir)
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		logrus.Warnf("创建 profile 目录失败: %v", err)
	}

	l, url, err := launchProfile(headless, profileDir, binPath, proxy)
	if err != nil {
		panic(err)
	}
	b := rod.New().ControlURL(url).MustConnect()
	activeProfileBrowser[profileDir]++

	return &ProfileBrowser{
		browser:    b,
		launcher:   l,
		profileDir: profileDir,
	}
}

func newProfileLauncher(headless bool, profileDir string, binPath string, proxy string) *launcher.Launcher {
	l := launcher.New().
		Headless(headless).
		UserDataDir(profileDir).
		Set("--no-sandbox").
		Set("user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")

	if binPath != "" {
		l = l.Bin(binPath)
	}
	if proxy != "" {
		l = l.Proxy(proxy)
	}
	return l
}

func launchProfile(headless bool, profileDir string, binPath string, proxy string) (*launcher.Launcher, string, error) {
	l := newProfileLauncher(headless, profileDir, binPath, proxy)
	url, err := l.Launch()
	if err == nil {
		return l, url, nil
	}

	logrus.Warnf("Chrome profile 首次启动失败: profile_dir=%s error=%v", profileDir, err)
	if !isStaleProfileLockError(err) {
		return nil, "", err
	}
	if activeProfileBrowser[profileDir] > 0 {
		logrus.Warnf("Chrome profile 锁错误但当前服务仍持有活跃浏览器，跳过清理: profile_dir=%s active=%d", profileDir, activeProfileBrowser[profileDir])
		return nil, "", err
	}

	stale, reason, inspectErr := staleSingletonLock(profileDir)
	if inspectErr != nil {
		logrus.Warnf("Chrome profile SingletonLock 无法判断是否 stale: profile_dir=%s reason=%s error=%v", profileDir, reason, inspectErr)
		return nil, "", err
	}
	logrus.Infof("Chrome profile SingletonLock stale 判定: profile_dir=%s stale=%t reason=%s", profileDir, stale, reason)
	if !stale {
		return nil, "", err
	}

	removed, cleanupErr := removeStaleSingletonFiles(profileDir)
	if cleanupErr != nil {
		logrus.Warnf("清理 stale Chrome singleton 文件失败: profile_dir=%s removed=%s error=%v", profileDir, strings.Join(removed, ","), cleanupErr)
		return nil, "", err
	}
	if len(removed) == 0 {
		logrus.Warnf("已判断为 stale，但没有可清理的 singleton 文件，跳过 Chrome relaunch: profile_dir=%s", profileDir)
		return nil, "", err
	}
	logrus.Infof("已清理 stale Chrome singleton 文件: profile_dir=%s files=%s", profileDir, strings.Join(removed, ","))

	logrus.Infof("Chrome profile 将进行一次 relaunch: profile_dir=%s", profileDir)
	retryLauncher := newProfileLauncher(headless, profileDir, binPath, proxy)
	retryURL, retryErr := retryLauncher.Launch()
	if retryErr != nil {
		return nil, "", fmt.Errorf("Chrome relaunch after stale profile lock cleanup failed: %w", retryErr)
	}
	logrus.Infof("Chrome profile relaunch 成功: profile_dir=%s", profileDir)
	return retryLauncher, retryURL, nil
}

func normalizedProfileDir(profileDir string) string {
	absPath, err := filepath.Abs(profileDir)
	if err != nil {
		return filepath.Clean(profileDir)
	}
	return filepath.Clean(absPath)
}

func logSingletonFiles(profileDir string, stage string) {
	present := make([]string, 0, len(singletonFileNames))
	for _, name := range singletonFileNames {
		if _, err := os.Lstat(filepath.Join(profileDir, name)); err == nil {
			present = append(present, name)
		}
	}
	logrus.Infof("Chrome profile singleton 文件[%s]: profile_dir=%s found=%s", stage, profileDir, strings.Join(present, ","))
}

func isStaleProfileLockError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"profile appears to be in use",
		"profile is already in use",
		"singletonlock",
		"singletoncookie",
		"singletonsocket",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func staleSingletonLock(profileDir string) (bool, string, error) {
	lockPath := filepath.Join(profileDir, "SingletonLock")
	info, err := os.Lstat(lockPath)
	if os.IsNotExist(err) {
		return false, "SingletonLock 不存在", nil
	}
	if err != nil {
		return false, "读取 SingletonLock 失败", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return false, "SingletonLock 不是可验证所有者的符号链接，保守跳过清理", nil
	}

	target, err := os.Readlink(lockPath)
	if err != nil {
		return false, "读取 SingletonLock 所有者失败", err
	}
	host, pid, ok := parseSingletonOwner(target)
	if !ok {
		return false, fmt.Sprintf("无法解析 SingletonLock 所有者 %q，保守跳过清理", target), nil
	}
	currentHost, err := os.Hostname()
	if err != nil {
		return false, "读取当前容器 hostname 失败", err
	}
	if !strings.EqualFold(host, currentHost) {
		return true, fmt.Sprintf("SingletonLock 所有者为旧 hostname=%s pid=%d，当前 hostname=%s", host, pid, currentHost), nil
	}
	if pid == os.Getpid() {
		return false, fmt.Sprintf("SingletonLock 属于当前服务进程 pid=%d", pid), nil
	}
	alive, known := profileProcessAlive(pid)
	if !known {
		return false, fmt.Sprintf("无法确认当前 hostname 上 pid=%d 是否存活，保守跳过清理", pid), nil
	}
	if alive {
		return false, fmt.Sprintf("SingletonLock 所属进程仍存活 hostname=%s pid=%d", host, pid), nil
	}
	return true, fmt.Sprintf("SingletonLock 所属进程已退出 hostname=%s pid=%d", host, pid), nil
}

func parseSingletonOwner(target string) (string, int, bool) {
	base := filepath.Base(target)
	separator := strings.LastIndexByte(base, '-')
	if separator <= 0 || separator == len(base)-1 {
		return "", 0, false
	}
	pid, err := strconv.Atoi(base[separator+1:])
	if err != nil || pid <= 0 {
		return "", 0, false
	}
	return base[:separator], pid, true
}

func profileProcessAlive(pid int) (alive bool, known bool) {
	if pid <= 0 {
		return false, true
	}
	if pid == os.Getpid() {
		return true, true
	}
	if runtime.GOOS != "linux" {
		return false, false
	}
	_, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
	if err == nil {
		return true, true
	}
	if os.IsNotExist(err) {
		return false, true
	}
	return false, false
}

func removeStaleSingletonFiles(profileDir string) ([]string, error) {
	removed := make([]string, 0, len(singletonFileNames))
	var failures []string
	for _, name := range singletonFileNames {
		path := filepath.Join(profileDir, name)
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		if err := os.Remove(path); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		removed = append(removed, name)
	}
	if len(failures) > 0 {
		return removed, fmt.Errorf("singleton 文件清理失败: %s", strings.Join(failures, "; "))
	}
	return removed, nil
}

// NewPage 创建启用 stealth 模式的新页面
func (b *ProfileBrowser) NewPage() *rod.Page {
	return stealth.MustPage(b.browser)
}

// Close 关闭浏览器，等待 Chrome 进程完全退出后再返回。
// 必须等进程退出而非仅关闭 CDP 连接，否则 profile 数据可能尚未写入磁盘，
// 下一个使用同一 profileDir 的浏览器启动时会读到不完整的 session。
func (b *ProfileBrowser) Close() {
	if b == nil {
		return
	}
	profileBrowserMu.Lock()
	defer profileBrowserMu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	pid := b.launcher.PID()
	_ = b.browser.Close() // 忽略 error（Chrome 关闭时 CDP 连接会断开）

	// 等待 Chrome 进程退出（检查 /proc/{pid}）
	waitProcessExit(pid)
	if activeProfileBrowser[b.profileDir] > 0 {
		activeProfileBrowser[b.profileDir]--
		if activeProfileBrowser[b.profileDir] == 0 {
			delete(activeProfileBrowser, b.profileDir)
		}
	}
}

// waitProcessExit 轮询 /proc/{pid} 直到 Chrome 进程退出（最多等 15 秒）。
// 超时后强制 SIGKILL，确保 profile SingletonLock 被释放。
func waitProcessExit(pid int) {
	if pid <= 0 {
		return
	}
	procPath := fmt.Sprintf("/proc/%d", pid)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(procPath); os.IsNotExist(err) {
			return // 进程已退出
		}
		time.Sleep(100 * time.Millisecond)
	}
	// 超时：强制杀掉，否则下次启动同一 profile 会遇到 SingletonLock 冲突
	logrus.Warnf("Chrome 进程 %d 未在 15s 内退出，强制 SIGKILL", pid)
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
