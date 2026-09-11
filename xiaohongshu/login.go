package xiaohongshu

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-rod/rod"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type LoginAction struct {
	page *rod.Page
}

func NewLogin(page *rod.Page) *LoginAction {
	return &LoginAction{page: page}
}

// CheckLoginStatus 检查 www 消费端是否已有可用的 web_session cookie。
// 仅存在 creator 域 cookie 不代表 www 已登录。
func (a *LoginAction) CheckLoginStatus(ctx context.Context) (bool, error) {
	// 先导航一次，让浏览器加载当前 cookies
	pp := a.page.Context(ctx)
	if err := pp.Navigate("https://www.xiaohongshu.com/explore"); err != nil {
		return false, errors.Wrap(err, "navigate failed")
	}
	time.Sleep(1 * time.Second)

	status, err := ReadConsumerCookieStatus(pp)
	if err != nil {
		return false, err
	}
	if !status.HasValidConsumerSession() {
		return false, nil
	}

	// www 可能在消费端 session 未被服务端接受时显示认证门槛。此检查
	// 只读页面 DOM，不执行任何登录或写操作。
	gate, gateErr := ReadConsumerAuthGate(pp)
	if gateErr != nil {
		return false, errors.Wrap(gateErr, "read consumer auth state failed")
	}
	if gate.Present {
		logrus.Warnf("check_login_status: consumer auth gate visible: %s", gate.Description())
		return false, nil
	}
	evidence, evidenceErr := WaitForConsumerLoginEvidence(pp, 5*time.Second)
	if evidenceErr != nil {
		return false, errors.Wrap(evidenceErr, "read consumer login evidence failed")
	}
	if !evidence.Present {
		logrus.Warn("check_login_status: consumer cookie is present but no positive www login evidence was found")
		return false, nil
	}
	return true, nil
}

func (a *LoginAction) Login(ctx context.Context) error {
	pp := a.page.Context(ctx)

	// 导航到小红书首页，这会触发二维码弹窗
	pp.MustNavigate("https://www.xiaohongshu.com/explore").MustWaitLoad()

	// 等待一小段时间让页面完全加载
	time.Sleep(2 * time.Second)

	// 检查是否已经登录
	if exists, _, _ := pp.Has(".main-container .user .link-wrapper .channel"); exists {
		// 已经登录，直接返回
		return nil
	}

	// 等待扫码成功提示或者登录完成
	// 这里我们等待登录成功的元素出现，这样更简单可靠
	pp.MustElement(".main-container .user .link-wrapper .channel")

	return nil
}

func (a *LoginAction) FetchQrcodeImage(ctx context.Context) (string, bool, error) {
	// 导航阶段：独立 20s 超时
	navCtx, navCancel := context.WithTimeout(ctx, 20*time.Second)
	defer navCancel()

	pp := a.page.Context(navCtx)
	if err := pp.Navigate("https://www.xiaohongshu.com/explore"); err != nil {
		return "", false, errors.Wrap(err, "navigate failed")
	}
	if err := pp.WaitLoad(); err != nil {
		// WaitLoad 超时不致命，继续尝试
		logrus.Warnf("waitload timeout (non-fatal): %v", err)
	}
	time.Sleep(2 * time.Second)

	// 检查是否已登录
	pp2 := a.page.Context(ctx)
	if exists, _, _ := pp2.Has(".main-container .user .link-wrapper .channel"); exists {
		return "", true, nil
	}

	// 等待二维码元素：独立 45s 超时（JS 异步渲染需要时间）
	qrCtx, qrCancel := context.WithTimeout(ctx, 45*time.Second)
	defer qrCancel()

	pp3 := a.page.Context(qrCtx)
	el, err := pp3.Element(".qrcode-img")
	if err != nil {
		// 失败时截图，保存到 /tmp 方便诊断
		if img, serr := a.page.Screenshot(false, nil); serr == nil {
			path := fmt.Sprintf("/tmp/xhs-login-debug-%d.png", time.Now().Unix())
			_ = os.WriteFile(path, img, 0644)
			logrus.Warnf("qrcode element not found, screenshot saved to %s", path)
		}
		// 同时记录页面 HTML 前 2000 字节
		if body, herr := a.page.MustElement("body").HTML(); herr == nil && len(body) > 0 {
			preview := body
			if len(preview) > 2000 {
				preview = preview[:2000]
			}
			logrus.Warnf("page body preview: %s", preview)
		}
		return "", false, errors.Wrap(err, "qrcode element not found")
	}

	// 等待 src 填充（JS 异步写入 base64，元素出现时 src 可能还是空）
	var srcVal string
	for i := 0; i < 15; i++ {
		src, attrErr := el.Attribute("src")
		if attrErr == nil && src != nil && len(*src) > 30 {
			srcVal = *src
			break
		}
		time.Sleep(1 * time.Second)
	}
	if srcVal == "" {
		return "", false, errors.New("qrcode src is empty after waiting")
	}

	return srcVal, false, nil
}

// WaitForLogin 轮询浏览器 cookies，检测到可供 www 使用的 web_session
// 才表示消费端登录成功。
func (a *LoginAction) WaitForLogin(ctx context.Context) bool {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			status, err := ReadConsumerCookieStatus(a.page)
			if err != nil {
				continue
			}
			if status.HasValidConsumerSession() {
				return true
			}
		}
	}
}
