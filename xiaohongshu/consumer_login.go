package xiaohongshu

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	consumerLoginURL   = "https://www.xiaohongshu.com/login"
	consumerExploreURL = "https://www.xiaohongshu.com/explore"
)

// ConsumerLoginAction handles the www consumer phone-login modal. It shares
// the existing OTP result state with CreatorLoginAction, but keeps its own
// page because the two login flows use different origins and sessions.
type ConsumerLoginAction struct {
	page *rod.Page
}

func NewConsumerLogin(page *rod.Page) *ConsumerLoginAction {
	return &ConsumerLoginAction{page: page}
}

// NavigateToLogin opens the consumer phone-login surface without starting an
// OTP request. It first tries the canonical login route and falls back to the
// explore page's visible login control when the route redirects.
func (a *ConsumerLoginAction) NavigateToLogin() ([]byte, error) {
	if a == nil || a.page == nil {
		return nil, errors.New("consumer 登录页面不可用")
	}
	pp := a.page.Timeout(30 * time.Second)
	if err := pp.Navigate(consumerLoginURL); err != nil {
		logrus.Warnf("consumer 登录路由打开失败，尝试 explore 登录入口: %v", err)
		if err := pp.Navigate(consumerExploreURL); err != nil {
			return nil, errors.Wrap(err, "导航到 consumer 登录页失败")
		}
	}
	if err := pp.WaitLoad(); err != nil {
		logrus.Warnf("consumer 登录页加载超时（non-fatal）: %v", err)
	}
	if evidence, evidenceErr := ReadConsumerLoginEvidence(pp); evidenceErr == nil && evidence.Present {
		return screenshotWithError(a.page, "consumer-login-already-established", errors.New("consumer session 已建立，无需重新发送验证码"))
	}

	if _, err := pp.ElementByJS(rod.Eval(consumerPhoneInputScript)); err != nil {
		if err := openConsumerLoginModal(pp); err != nil {
			return screenshotWithError(a.page, "consumer-login-entry", errors.Wrap(err, "未找到 consumer 手机号登录入口"))
		}
	}

	if _, err := pp.Timeout(15 * time.Second).ElementByJS(rod.Eval(consumerPhoneInputScript)); err != nil {
		return screenshotWithError(a.page, "consumer-login-phone-input", errors.Wrap(err, "未找到 consumer 手机号输入框"))
	}
	return a.page.Screenshot(false, nil)
}

// SendOTP fills the consumer phone field and presses the visible 获取验证码
// control. The result deliberately uses the same confirmed/uncertain state
// semantics as the existing Creator flow.
func (a *ConsumerLoginAction) SendOTP(phone string) (*OTPSendResult, error) {
	if a == nil || a.page == nil {
		return nil, errors.New("consumer 登录页面不可用")
	}
	pp := a.page.Timeout(20 * time.Second)
	phoneInput, err := pp.ElementByJS(rod.Eval(consumerPhoneInputScript))
	if err != nil {
		return &OTPSendResult{Status: OTPSendFailed, Message: "consumer 验证码发送失败：未找到手机号输入框"}, err
	}
	defer phoneInput.Release()

	set, err := phoneInput.Eval(`(value) => {
        const input = this;
        if (!input) return false;
        const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
        setter.call(input, value);
        input.dispatchEvent(new Event('input', {bubbles: true}));
        input.dispatchEvent(new Event('change', {bubbles: true}));
        input.dispatchEvent(new Event('blur', {bubbles: true}));
        return true;
    }`, phone)
	if err != nil || !set.Value.Bool() {
		return &OTPSendResult{Status: OTPSendFailed, Message: "consumer 验证码发送失败：手机号输入未生效"}, errors.New("consumer 手机号输入未生效")
	}

	sendButton, err := pp.ElementByJS(rod.Eval(consumerSendOTPButtonScript))
	if err != nil {
		return &OTPSendResult{Status: OTPSendFailed, Message: "consumer 验证码发送失败：未找到获取验证码按钮"}, err
	}
	defer sendButton.Release()
	if err := sendButton.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return &OTPSendResult{Status: OTPSendFailed, Message: "consumer 验证码发送失败：点击获取验证码失败"}, err
	}

	status, message := waitForConsumerOTPSendResult(a.page, 10*time.Second)
	shot, shotErr := a.page.Screenshot(false, nil)
	if shotErr != nil {
		logrus.Warnf("consumer OTP 结果截图失败: %v", shotErr)
	}
	if status == OTPSendFailed {
		return &OTPSendResult{Screenshot: shot, Status: status, Message: message}, errors.New(message)
	}
	return &OTPSendResult{Screenshot: shot, Status: status, Message: message}, nil
}

func (a *ConsumerLoginAction) VerifyOTP(otp string) (*OTPVerificationResult, error) {
	if a == nil || a.page == nil {
		return nil, errors.New("consumer 登录页面不可用")
	}
	pp := a.page.Timeout(20 * time.Second)
	otpInput, err := pp.ElementByJS(rod.Eval(consumerOTPInputScript))
	if err != nil {
		return nil, errors.Wrap(err, "未找到 consumer 验证码输入框")
	}
	defer otpInput.Release()
	if err := otpInput.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return nil, errors.Wrap(err, "点击 consumer 验证码输入框失败")
	}
	if err := otpInput.SelectAllText(); err != nil {
		logrus.Warnf("consumer OTP SelectAllText failed (non-fatal): %v", err)
	}
	if err := otpInput.Input(otp); err != nil {
		return nil, errors.Wrap(err, "输入 consumer 验证码失败")
	}

	loginButton, err := pp.ElementByJS(rod.Eval(consumerLoginButtonScript))
	if err != nil {
		return nil, errors.Wrap(err, "未找到 consumer 登录按钮")
	}
	defer loginButton.Release()
	if err := loginButton.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return nil, errors.Wrap(err, "点击 consumer 登录按钮失败")
	}
	time.Sleep(3 * time.Second)

	if gate, gateErr := ReadConsumerAuthGate(a.page); gateErr == nil && gate.Present && gate.Kind == "verification" {
		shot, shotErr := a.page.Screenshot(false, nil)
		if shotErr != nil {
			logrus.Warnf("consumer 安全验证弹窗截图失败: %v", shotErr)
		}
		return &OTPVerificationResult{
			Status:                 OTPVerificationSecurityVerificationNeeded,
			SecurityVerificationQR: shot,
		}, nil
	}

	evidence, evidenceErr := WaitForConsumerLoginEvidence(a.page, 5*time.Second)
	if evidenceErr == nil && evidence.Present {
		return &OTPVerificationResult{Status: OTPVerificationSucceeded}, nil
	}
	if gate, gateErr := ReadConsumerAuthGate(a.page); gateErr == nil && gate.Present {
		return nil, fmt.Errorf("consumer 登录未完成：认证门槛仍可见（%s）", gate.Description())
	}
	return nil, errors.New("consumer 登录未完成：未检测到正向登录证据")
}

func (a *ConsumerLoginAction) WaitForSecurityVerification(timeout time.Duration) error {
	if a == nil || a.page == nil {
		return errors.New("consumer 登录页面不可用")
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		evidence, err := ReadConsumerLoginEvidence(a.page)
		if err == nil && evidence.Present {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("consumer 安全验证超时（%s），请重新开始 consumer 登录", timeout)
		}
		time.Sleep(2 * time.Second)
	}
}

func openConsumerLoginModal(page *rod.Page) error {
	trigger, err := page.ElementByJS(rod.Eval(consumerLoginTriggerScript))
	if err != nil {
		return err
	}
	defer trigger.Release()
	return trigger.Click(proto.InputMouseButtonLeft, 1)
}

func waitForConsumerOTPSendResult(page *rod.Page, timeout time.Duration) (OTPSendStatus, string) {
	deadline := time.Now().Add(timeout)
	for {
		result, err := page.Eval(consumerOTPSendResultScript)
		if err == nil {
			value := result.Value.String()
			if strings.HasPrefix(value, "sent:") {
				return OTPSendConfirmed, "consumer 验证码已发送，请调用 consumer_verify_otp。"
			}
			if strings.HasPrefix(value, "failed:") {
				message := strings.TrimPrefix(value, "failed:")
				if message == "" {
					message = "页面未能发送 consumer 验证码"
				}
				return OTPSendFailed, message
			}
		}
		if !time.Now().Before(deadline) {
			return OTPSendUncertain, "consumer 验证码发送状态无法从页面确认；登录会话已保留。如果手机实际收到验证码，请继续调用 consumer_verify_otp。"
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func screenshotWithError(page *rod.Page, name string, err error) ([]byte, error) {
	if page == nil {
		return nil, err
	}
	shot, shotErr := page.Screenshot(false, nil)
	if shotErr == nil {
		saveDebugShot(name, shot)
	}
	return shot, err
}

const consumerPhoneInputScript = `() => {
  const visible = (node) => {
    if (!node) return false;
    const style = window.getComputedStyle(node);
    const rect = node.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || 1) > 0 && rect.width > 0 && rect.height > 0;
  };
  for (const input of document.querySelectorAll('input')) {
    const hint = [input.placeholder, input.name, input.type, input.autocomplete]
      .map(value => String(value || '').toLowerCase()).join(' ');
    if (visible(input) && /手机|手机号|telephone|tel/.test(hint) && !/验证码|one-time/.test(hint)) return input;
  }
  return null;
}`

const consumerOTPInputScript = `() => {
  const visible = (node) => {
    if (!node) return false;
    const style = window.getComputedStyle(node);
    const rect = node.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || 1) > 0 && rect.width > 0 && rect.height > 0;
  };
  for (const input of document.querySelectorAll('input')) {
    const hint = [input.placeholder, input.name, input.autocomplete, input.inputMode]
      .map(value => String(value || '').toLowerCase()).join(' ');
    if (visible(input) && (/验证码|校验码|one-time|otp/.test(hint) || input.maxLength === 6)) return input;
  }
  return null;
}`

const consumerSendOTPButtonScript = `() => {
  const visible = (node) => {
    if (!node) return false;
    const style = window.getComputedStyle(node);
    const rect = node.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || 1) > 0 && rect.width > 0 && rect.height > 0;
  };
  const nodes = document.querySelectorAll('button, [role="button"], a, span, div');
  for (const node of nodes) {
    const text = String(node.innerText || node.textContent || '').replace(/\s+/g, '').trim();
    if (visible(node) && (text === '获取验证码' || text === '发送验证码')) return node;
  }
  return null;
}`

const consumerLoginButtonScript = `() => {
  const visible = (node) => {
    if (!node) return false;
    const style = window.getComputedStyle(node);
    const rect = node.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || 1) > 0 && rect.width > 0 && rect.height > 0;
  };
  for (const node of document.querySelectorAll('button, [role="button"], a')) {
    const text = String(node.innerText || node.textContent || '').replace(/\s+/g, '').trim();
    if (visible(node) && text === '登录') return node;
  }
  return null;
}`

const consumerLoginTriggerScript = `() => {
  const visible = (node) => {
    if (!node) return false;
    const style = window.getComputedStyle(node);
    const rect = node.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || 1) > 0 && rect.width > 0 && rect.height > 0;
  };
  for (const preferredText of ['手机号登录', '登录']) {
    for (const node of document.querySelectorAll('button, [role="button"], a, span, div')) {
      const text = String(node.innerText || node.textContent || '').replace(/\s+/g, '').trim();
      if (visible(node) && text === preferredText) return node;
    }
  }
  return null;
}`

const consumerOTPSendResultScript = `() => {
  const text = document.body ? String(document.body.innerText || '').replace(/\s+/g, ' ') : '';
  if (/验证码已发送|重新发送|重新获取|获取验证码\s*\d+\s*秒/.test(text)) return 'sent:';
  const failure = text.match(/手机号不正确|验证码发送失败|操作频繁|请稍后再试/);
  if (failure) return 'failed:' + failure[0];
  return 'pending:';
}`
