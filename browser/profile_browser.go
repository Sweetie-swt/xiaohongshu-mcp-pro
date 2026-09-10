package browser

import (
	"context"
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
	browser      *rod.Browser
	launcher     *launcher.Launcher
	profileDir   string
	profileLease chan struct{}
	closed       bool
}

var (
	profileBrowserMu     sync.Mutex
	activeProfileBrowser = make(map[string]int)
	profileLeaseMu       sync.Mutex
	profileLeases        = make(map[string]chan struct{})
)

var singletonFileNames = []string{
	"SingletonLock",
	"SingletonCookie",
	"SingletonSocket",
}

const (
	profileAcquireTimeout = 30 * time.Second
	profileLaunchTimeout  = 30 * time.Second
	profileCloseTimeout   = 5 * time.Second
	profileExitTimeout    = 5 * time.Second
)

// NewProfileBrowser 创建带持久化 profile 目录的浏览器实例。
// profileDir 在浏览器关闭后保留，下次启动自动加载已有 session。
func NewProfileBrowser(headless bool, profileDir string, binPath string, proxy string) *ProfileBrowser {
	ctx, cancel := context.WithTimeout(context.Background(), profileLaunchTimeout)
	defer cancel()
	b, err := NewProfileBrowserWithContext(ctx, headless, profileDir, binPath, proxy)
	if err != nil {
		panic(err)
	}
	return b
}

// NewProfileBrowserWithContext 创建带持久化 profile 目录的浏览器实例，
// 并让 profile 获取、Chrome 启动都受 ctx 控制。调用方必须调用 Close。
func NewProfileBrowserWithContext(ctx context.Context, headless bool, profileDir string, binPath string, proxy string) (*ProfileBrowser, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	profileDir = normalizedProfileDir(profileDir)
	acquireCtx, acquireCancel := context.WithTimeout(ctx, profileAcquireTimeout)
	lease, err := acquireProfileLease(acquireCtx, profileDir)
	acquireCancel()
	if err != nil {
		return nil, fmt.Errorf("acquire Chrome profile lease %s: %w", profileDir, err)
	}
	releaseLease := true
	defer func() {
		if releaseLease {
			releaseProfileLease(lease)
		}
	}()

	logSingletonFiles(profileDir, "启动前")
	logrus.Infof("创建持久化 Chrome profile: %s", profileDir)
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		logrus.Warnf("创建 profile 目录失败: %v", err)
	}

	launchCtx, cancel := context.WithTimeout(ctx, profileLaunchTimeout)
	defer cancel()
	l, url, err := launchProfile(launchCtx, headless, profileDir, binPath, proxy)
	if err != nil {
		return nil, err
	}
	b := rod.New().ControlURL(url)
	err = b.Connect()
	if err != nil {
		logrus.Warnf("Chrome profile CDP 连接失败: profile_dir=%s error=%v", profileDir, err)
		pid := l.PID()
		l.Kill()
		_ = waitProcessExit(pid, profileExitTimeout)
		return nil, fmt.Errorf("connect Chrome profile browser: %w", err)
	}
	profileBrowserMu.Lock()
	activeProfileBrowser[profileDir]++
	profileBrowserMu.Unlock()
	releaseLease = false
	logrus.Infof("Chrome profile browser created: profile_dir=%s active=%d", profileDir, activeProfileBrowserCount(profileDir))

	return &ProfileBrowser{
		browser:      b,
		launcher:     l,
		profileDir:   profileDir,
		profileLease: lease,
	}, nil
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

func launchProfile(ctx context.Context, headless bool, profileDir string, binPath string, proxy string) (*launcher.Launcher, string, error) {
	l := newProfileLauncher(headless, profileDir, binPath, proxy).Context(ctx)
	url, err := l.Launch()
	if err == nil {
		return l, url, nil
	}

	logrus.Warnf("Chrome profile 首次启动失败: profile_dir=%s error=%v", profileDir, err)
	if !isStaleProfileLockError(err) {
		return nil, "", err
	}
	active := activeProfileBrowserCount(profileDir)
	if active > 0 {
		logrus.Warnf("Chrome profile 锁错误但当前服务仍持有活跃浏览器，跳过清理: profile_dir=%s active=%d", profileDir, active)
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
	retryLauncher := newProfileLauncher(headless, profileDir, binPath, proxy).Context(ctx)
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

func acquireProfileLease(ctx context.Context, profileDir string) (chan struct{}, error) {
	profileLeaseMu.Lock()
	lease, ok := profileLeases[profileDir]
	if !ok {
		lease = make(chan struct{}, 1)
		lease <- struct{}{}
		profileLeases[profileDir] = lease
	}
	profileLeaseMu.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-lease:
		logrus.Infof("Chrome profile lease acquired: profile_dir=%s", profileDir)
		return lease, nil
	}
}

func releaseProfileLease(lease chan struct{}) {
	if lease == nil {
		return
	}
	select {
	case lease <- struct{}{}:
	default:
	}
}

func activeProfileBrowserCount(profileDir string) int {
	profileBrowserMu.Lock()
	defer profileBrowserMu.Unlock()
	return activeProfileBrowser[profileDir]
}

// NewPage 创建启用 stealth 模式的新页面
func (b *ProfileBrowser) NewPage() *rod.Page {
	return stealth.MustPage(b.browser)
}

// NewPageWithContext 创建启用 stealth 模式的新页面，并把页面创建绑定到 ctx。
func (b *ProfileBrowser) NewPageWithContext(ctx context.Context) (*rod.Page, error) {
	if b == nil || b.browser == nil {
		return nil, fmt.Errorf("Chrome profile browser is not available")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	page, err := stealth.Page(b.browser.Context(ctx))
	if err != nil {
		return nil, fmt.Errorf("create Chrome profile page: %w", err)
	}
	return page, nil
}

// Close 关闭浏览器，等待 Chrome 进程完全退出后再返回。
// 必须等进程退出而非仅关闭 CDP 连接，否则 profile 数据可能尚未写入磁盘，
// 下一个使用同一 profileDir 的浏览器启动时会读到不完整的 session。
func (b *ProfileBrowser) Close() {
	if b == nil {
		return
	}
	profileBrowserMu.Lock()
	if b.closed {
		profileBrowserMu.Unlock()
		return
	}
	b.closed = true
	pid := 0
	if b.launcher != nil {
		pid = b.launcher.PID()
	}
	chrome := b.browser
	l := b.launcher
	profileDir := b.profileDir
	lease := b.profileLease
	profileBrowserMu.Unlock()

	logrus.Infof("Chrome profile cleanup start: profile_dir=%s", profileDir)
	closeCtx, cancel := context.WithTimeout(context.Background(), profileCloseTimeout)
	closeErr := closeRodBrowser(chrome, closeCtx)
	cancel()
	if closeErr != nil {
		logrus.Warnf("Chrome profile CDP close failed: profile_dir=%s error=%v", profileDir, closeErr)
		if l != nil {
			l.Kill()
		}
	}
	if !waitProcessExit(pid, profileExitTimeout) && l != nil {
		logrus.Warnf("Chrome 进程 %d 未在 %s 内退出，强制结束", pid, profileExitTimeout)
		l.Kill()
		_ = waitProcessExit(pid, time.Second)
	}

	profileBrowserMu.Lock()
	if activeProfileBrowser[b.profileDir] > 0 {
		activeProfileBrowser[b.profileDir]--
		if activeProfileBrowser[b.profileDir] == 0 {
			delete(activeProfileBrowser, b.profileDir)
		}
	}
	active := activeProfileBrowser[b.profileDir]
	profileBrowserMu.Unlock()
	releaseProfileLease(lease)
	logrus.Infof("Chrome profile cleanup end: profile_dir=%s active=%d", profileDir, active)
}

func closeRodBrowser(b *rod.Browser, ctx context.Context) (err error) {
	if b == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("close Chrome browser panic: %v", recovered)
		}
	}()
	return b.Context(ctx).Close()
}

// waitProcessExit 轮询 /proc/{pid} 直到 Chrome 进程退出。
func waitProcessExit(pid int, timeout time.Duration) bool {
	if pid <= 0 {
		return true
	}
	if runtime.GOOS != "linux" {
		return true
	}
	procPath := fmt.Sprintf("/proc/%d", pid)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(procPath); os.IsNotExist(err) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
