package xiaohongshu

import (
	"encoding/json"
	"fmt"
	"regexp"
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

type consumerOTPPageDiagnostics struct {
	URL                         string   `json:"url"`
	Title                       string   `json:"title"`
	PhoneInputFound             bool     `json:"phone_input_found"`
	SendButtonFound             bool     `json:"send_button_found"`
	SendButtonText              string   `json:"send_button_text"`
	SendButtonDisabled          bool     `json:"send_button_disabled"`
	SendButtonAriaDisabled      bool     `json:"send_button_aria_disabled"`
	SendButtonHasDisabledClass  bool     `json:"send_button_has_disabled_class"`
	AgreementFound              int      `json:"agreement_found"`
	AgreementClicked            int      `json:"agreement_clicked"`
	AgreementCheckedTransitions int      `json:"agreement_checked_transitions"`
	AgreementRemainingUnchecked int      `json:"agreement_remaining_unchecked"`
	AgreementLabels             []string `json:"agreement_labels"`
	VisibleMessages             []string `json:"visible_messages"`
	CountdownVisible            bool     `json:"countdown_visible"`
}

type consumerAgreementDiagnostics struct {
	Found              int      `json:"found"`
	Clicked            int      `json:"clicked"`
	CheckedTransitions int      `json:"checked_transitions"`
	RemainingUnchecked int      `json:"remaining_unchecked"`
	Labels             []string `json:"labels"`
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
	beforeInput := a.collectConsumerOTPPageDiagnostics()
	a.logConsumerOTPPageDiagnostics("填写手机号前", beforeInput)

	phoneInput, err := pp.ElementByJS(rod.Eval(consumerPhoneInputScript))
	if err != nil {
		shot, _ := pp.Screenshot(false, nil)
		saveDebugShot("consumer-login-no-phone-input", shot)
		return &OTPSendResult{Screenshot: shot, Status: OTPSendFailed, Message: "consumer 验证码发送失败：未找到手机号输入框"}, err
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
	afterInput := a.collectConsumerOTPPageDiagnostics()
	a.logConsumerOTPPageDiagnostics("填写手机号后", afterInput)
	if err != nil || !set.Value.Bool() {
		shot, _ := pp.Screenshot(false, nil)
		saveDebugShot("consumer-login-phone-input", shot)
		return &OTPSendResult{Screenshot: shot, Status: OTPSendFailed, Message: "consumer 验证码发送失败：手机号输入未生效"}, errors.New("consumer 手机号输入未生效")
	}

	agreement, customAgreement, agreementErr := a.ensureConsumerAgreementChecked()
	a.logConsumerAgreementDiagnostics("协议处理后", agreement)
	if customAgreement != nil {
		a.logConsumerCustomAgreementResult(customAgreement)
		if customAgreement.Found {
			message := fmt.Sprintf("consumer custom agreement diagnostic-only: custom_agreement_found=true custom_agreement_clicked=%t custom_agreement_transition=%t；未点击获取验证码，未发送短信。",
				customAgreement.Clicked, customAgreement.Transition)
			if !customAgreement.Transition {
				message += " diagnostic inconclusive。"
			}
			if customAgreement.Error != "" {
				message += " " + customAgreement.Error
			}
			return &OTPSendResult{Status: OTPSendFailed, Message: message}, errors.New("consumer 自定义协议控件已观测，已进入 diagnostic-only 分支")
		}
	}
	if agreement.Found == 0 {
		candidateCount, diagnosticErr := a.logConsumerAgreementDOMDiagnostic()
		customFound := false
		customClicked := false
		customTransition := false
		customError := ""
		if customAgreement != nil {
			customFound = customAgreement.Found
			customClicked = customAgreement.Clicked
			customTransition = customAgreement.Transition
			customError = customAgreement.Error
		}
		message := fmt.Sprintf("consumer 协议诊断完成：未识别到协议控件，命中结构区域=%d custom_agreement_found=%t custom_agreement_clicked=%t custom_agreement_transition=%t；未点击获取验证码，未发送短信。",
			candidateCount, customFound, customClicked, customTransition)
		if customError != "" {
			message += " " + customError
		}
		if diagnosticErr != nil {
			message += " DOM 诊断失败：" + diagnosticErr.Error()
		}
		return &OTPSendResult{Status: OTPSendFailed, Message: message}, errors.New("consumer 协议控件未识别，已进入 diagnostic-only 分支")
	}
	if agreementErr != nil {
		shot, _ := pp.Screenshot(false, nil)
		saveDebugShot("consumer-login-agreement-check", shot)
		return &OTPSendResult{Screenshot: shot, Status: OTPSendFailed, Message: "consumer 验证码发送失败：" + agreementErr.Error()}, agreementErr
	}
	afterAgreement := a.collectConsumerOTPPageDiagnostics()
	a.logConsumerOTPPageDiagnostics("协议处理后页面状态", afterAgreement)

	beforeClick := a.collectConsumerOTPPageDiagnostics()
	a.logConsumerOTPPageDiagnostics("点击获取验证码前", beforeClick)

	sendButton, err := pp.ElementByJS(rod.Eval(consumerSendOTPButtonScript))
	if err != nil {
		shot, _ := pp.Screenshot(false, nil)
		saveDebugShot("consumer-login-no-otp-button", shot)
		return &OTPSendResult{Screenshot: shot, Status: OTPSendFailed, Message: "consumer 验证码发送失败：未找到获取验证码按钮"}, err
	}
	defer sendButton.Release()
	clickErr := sendButton.Click(proto.InputMouseButtonLeft, 1)
	afterClick := a.collectConsumerOTPPageDiagnostics()
	a.logConsumerOTPClickDiagnostics("点击获取验证码后立即", afterClick, clickErr == nil, beforeClick)
	if clickErr != nil {
		shot, _ := pp.Screenshot(false, nil)
		saveDebugShot("consumer-login-otp-click", shot)
		return &OTPSendResult{Screenshot: shot, Status: OTPSendFailed, Message: "consumer 验证码发送失败：点击获取验证码失败"}, clickErr
	}

	status, message := waitForConsumerOTPSendResult(a.page, 10*time.Second)
	final := a.collectConsumerOTPPageDiagnostics()
	a.logConsumerOTPPageDiagnostics("最终发送结果", final)
	shot, shotErr := a.page.Screenshot(false, nil)
	if shotErr != nil {
		logrus.Warnf("consumer OTP 结果截图失败: %v", shotErr)
	}
	if status == OTPSendUncertain || status == OTPSendFailed {
		saveDebugShot("consumer-login-otp-send-result", shot)
	}
	if status == OTPSendFailed {
		return &OTPSendResult{Screenshot: shot, Status: status, Message: message}, errors.New(message)
	}
	return &OTPSendResult{Screenshot: shot, Status: status, Message: message}, nil
}

func (a *ConsumerLoginAction) collectConsumerOTPPageDiagnostics() consumerOTPPageDiagnostics {
	d := consumerOTPPageDiagnostics{}
	if info, err := a.page.Info(); err == nil {
		d.URL = info.URL
		d.Title = info.Title
	} else {
		logrus.Warnf("consumer OTP 诊断读取 page URL/title 失败: %v", err)
	}

	result, err := a.page.Eval(consumerOTPPageDiagnosticsScript)
	if err != nil {
		logrus.Warnf("consumer OTP 诊断读取 DOM 失败: %v", err)
	} else if err := json.Unmarshal([]byte(result.Value.String()), &d); err != nil {
		logrus.Warnf("consumer OTP 诊断解析 DOM 结果失败: %v", err)
	}
	agreement, agreementErr := a.readConsumerAgreementDiagnostics(false)
	if agreementErr != nil {
		logrus.Warnf("consumer OTP 协议诊断读取失败: %v", agreementErr)
	} else {
		d.AgreementFound = agreement.Found
		d.AgreementClicked = agreement.Clicked
		d.AgreementCheckedTransitions = agreement.CheckedTransitions
		d.AgreementRemainingUnchecked = agreement.RemainingUnchecked
		d.AgreementLabels = agreement.Labels
	}
	d.URL = firstNonEmpty(d.URL, pageURL(a.page))
	return d
}

func (a *ConsumerLoginAction) readConsumerAgreementDiagnostics(clickUnchecked bool) (consumerAgreementDiagnostics, error) {
	result, err := a.page.Eval(consumerAgreementScript, clickUnchecked)
	if err != nil {
		return consumerAgreementDiagnostics{}, err
	}
	var diagnostics consumerAgreementDiagnostics
	if err := json.Unmarshal([]byte(result.Value.String()), &diagnostics); err != nil {
		return consumerAgreementDiagnostics{}, err
	}
	return diagnostics, nil
}

func (a *ConsumerLoginAction) ensureConsumerAgreementChecked() (consumerAgreementDiagnostics, *consumerCustomAgreementResult, error) {
	diagnostics, err := a.readConsumerAgreementDiagnostics(true)
	if err != nil {
		return diagnostics, nil, errors.Wrap(err, "读取 consumer 登录协议 checkbox 失败")
	}
	if diagnostics.Found == 0 {
		custom, customErr := a.ensureConsumerCustomAgreementChecked()
		return diagnostics, custom, customErr
	}
	if diagnostics.RemainingUnchecked > 0 {
		return diagnostics, nil, errors.New("已识别到未勾选的 consumer 用户协议/隐私政策 checkbox，但勾选后仍未选中")
	}
	return diagnostics, nil, nil
}

func (a *ConsumerLoginAction) logConsumerAgreementDiagnostics(stage string, diagnostics consumerAgreementDiagnostics) {
	logrus.Infof("consumer OTP 协议诊断[%s]: found=%d clicked=%d checked_transitions=%d remaining_unchecked=%d labels=%s",
		stage,
		diagnostics.Found,
		diagnostics.Clicked,
		diagnostics.CheckedTransitions,
		diagnostics.RemainingUnchecked,
		sanitizeConsumerDiagnosticText(strings.Join(diagnostics.Labels, " | ")))
}

func (a *ConsumerLoginAction) logConsumerOTPPageDiagnostics(stage string, diagnostics consumerOTPPageDiagnostics) {
	messages := make([]string, 0, len(diagnostics.VisibleMessages))
	for _, message := range diagnostics.VisibleMessages {
		messages = append(messages, sanitizeConsumerDiagnosticText(message))
	}
	labels := make([]string, 0, len(diagnostics.AgreementLabels))
	for _, label := range diagnostics.AgreementLabels {
		labels = append(labels, sanitizeConsumerDiagnosticText(label))
	}
	logrus.Infof("consumer OTP 诊断[%s]: URL=%s title=%s phone_input_found=%t send_button_found=%t send_button_text=%s send_button_disabled=%t aria_disabled=%t disabled_class=%t agreement_found=%d agreement_clicked=%d agreement_checked_transitions=%d agreement_remaining_unchecked=%d agreement_labels=%s countdown=%t visible_messages=%s",
		stage,
		sanitizeConsumerDiagnosticText(diagnostics.URL),
		sanitizeConsumerDiagnosticText(diagnostics.Title),
		diagnostics.PhoneInputFound,
		diagnostics.SendButtonFound,
		sanitizeConsumerDiagnosticText(diagnostics.SendButtonText),
		diagnostics.SendButtonDisabled,
		diagnostics.SendButtonAriaDisabled,
		diagnostics.SendButtonHasDisabledClass,
		diagnostics.AgreementFound,
		diagnostics.AgreementClicked,
		diagnostics.AgreementCheckedTransitions,
		diagnostics.AgreementRemainingUnchecked,
		sanitizeConsumerDiagnosticText(strings.Join(labels, " | ")),
		diagnostics.CountdownVisible,
		sanitizeConsumerDiagnosticText(strings.Join(messages, " | ")))
}

func (a *ConsumerLoginAction) logConsumerOTPClickDiagnostics(stage string, diagnostics consumerOTPPageDiagnostics, clicked bool, before consumerOTPPageDiagnostics) {
	logrus.Infof("consumer OTP 诊断[%s]: click_dispatched=%t button_state_changed=%t",
		stage, clicked, consumerOTPButtonStateChanged(before, diagnostics))
	a.logConsumerOTPPageDiagnostics(stage, diagnostics)
}

func consumerOTPButtonStateChanged(before, after consumerOTPPageDiagnostics) bool {
	return before.SendButtonText != after.SendButtonText ||
		before.SendButtonDisabled != after.SendButtonDisabled ||
		before.SendButtonAriaDisabled != after.SendButtonAriaDisabled ||
		before.SendButtonHasDisabledClass != after.SendButtonHasDisabledClass ||
		before.CountdownVisible != after.CountdownVisible
}

var consumerDiagnosticNumberPattern = regexp.MustCompile(`[0-9][0-9\s-]{3,}[0-9]`)

const consumerAgreementPatternSource = `我已阅读并同意|用户协议|隐私政策|隐私协议|隐私条款|服务条款`

var consumerAgreementPattern = regexp.MustCompile(consumerAgreementPatternSource)

func isConsumerAgreementTextRelevant(value string) bool {
	return consumerAgreementPattern.MatchString(value)
}

func sanitizeConsumerDiagnosticText(value string) string {
	value = consumerDiagnosticNumberPattern.ReplaceAllString(value, "<digits-redacted>")
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 500 {
		return value[:500]
	}
	return value
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
			if status, message, matched := consumerOTPSendStatusFromPageValue(result.Value.String()); matched {
				return status, message
			}
		}
		if !time.Now().Before(deadline) {
			return OTPSendUncertain, "consumer 验证码发送状态无法从页面确认；登录会话已保留。如果手机实际收到验证码，请继续调用 consumer_verify_otp。"
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func consumerOTPSendStatusFromPageValue(value string) (OTPSendStatus, string, bool) {
	if strings.HasPrefix(value, "sent:") {
		return OTPSendConfirmed, "consumer 验证码已发送，请调用 consumer_verify_otp。", true
	}
	if strings.HasPrefix(value, "failed:") {
		message := strings.TrimPrefix(value, "failed:")
		if message == "" {
			message = "页面未能发送 consumer 验证码"
		}
		return OTPSendFailed, message, true
	}
	return "", "", false
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

const consumerAgreementScript = `(clickUnchecked) => {
  const visible = (el) => {
    if (!el) return false;
    const style = window.getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || 1) > 0 && rect.width > 0 && rect.height > 0;
  };
  const protocolWords = /(` + consumerAgreementPatternSource + `)/;
  const text = (el) => (el ? (el.innerText || el.textContent || '') : '')
    .replace(/\s+/g, ' ').trim();
  const labels = [];
  let found = 0;
  let clicked = 0;
  let checkedTransitions = 0;
  let remainingUnchecked = 0;
  const boxes = Array.from(document.querySelectorAll('input[type="checkbox"],[role="checkbox"]'));
  for (const box of boxes) {
    if (!visible(box)) continue;
    let associated = '';
    if (box.id) {
      const label = Array.from(document.querySelectorAll('label'))
        .find(item => item.htmlFor === box.id);
      if (label) associated = text(label);
    }
    if (!associated && box.closest('label')) associated = text(box.closest('label'));
    if (!associated && box.getAttribute('aria-label')) {
      associated = String(box.getAttribute('aria-label') || '').trim();
    }
    if (!associated && box.getAttribute('aria-labelledby')) {
      associated = String(box.getAttribute('aria-labelledby') || '').split(/\s+/)
        .map(id => document.getElementById(id))
        .map(node => text(node)).filter(Boolean).join(' ');
    }
    if (!associated) {
      let parent = box.parentElement;
      for (let i = 0; i < 2 && parent; i++, parent = parent.parentElement) {
        const candidate = text(parent);
        if (candidate && candidate.length <= 160 && protocolWords.test(candidate)) {
          associated = candidate;
          break;
        }
      }
    }
    if (!associated || !protocolWords.test(associated)) continue;
    found++;
    labels.push(associated);
    const checkedBefore = box.type === 'checkbox'
      ? box.checked : box.getAttribute('aria-checked') === 'true';
    const disabled = box.disabled || box.getAttribute('aria-disabled') === 'true';
    if (clickUnchecked && !checkedBefore && !disabled) {
      box.click();
      clicked++;
    }
    const checkedAfter = box.type === 'checkbox'
      ? box.checked : box.getAttribute('aria-checked') === 'true';
    if (!checkedBefore && checkedAfter) checkedTransitions++;
    if (!checkedAfter) remainingUnchecked++;
  }
  return JSON.stringify({found, clicked, checked_transitions: checkedTransitions,
    remaining_unchecked: remainingUnchecked, labels});
}`

const consumerOTPPageDiagnosticsScript = `() => {
  const visible = (el) => {
    if (!el) return false;
    const style = window.getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || 1) > 0 && rect.width > 0 && rect.height > 0;
  };
  const text = (el) => (el ? (el.innerText || el.textContent || '') : '')
    .replace(/\s+/g, ' ').trim();
  const phoneInputFound = Array.from(document.querySelectorAll('input')).some((input) => {
    const hint = [input.placeholder, input.name, input.type, input.autocomplete]
      .map(value => String(value || '').toLowerCase()).join(' ');
    return visible(input) && /手机|手机号|telephone|tel/.test(hint) &&
      !/验证码|one-time/.test(hint);
  });
  const directSend = Array.from(document.querySelectorAll('button,[role="button"],a,span,div'))
    .find(el => visible(el) && (text(el) === '获取验证码' || text(el) === '发送验证码'));
  let send = directSend;
  if (directSend) send = directSend.closest('button,[role="button"],a') || directSend;
  if (!send || !visible(send)) {
    send = Array.from(document.querySelectorAll('button,[role="button"],a,span,div'))
      .find(el => visible(el) && /重新发送|重新获取|获取验证码\s*\d+\s*秒/i.test(text(el)));
  }
  const sendText = text(send);
  const disabledClass = !!(send && /(^|\s)(disabled|is-disabled)(\s|$)/i.test(String(send.className || '')));
  const ariaDisabled = !!(send && send.getAttribute('aria-disabled') === 'true');
  const disabled = !!(send && (send.disabled || send.hasAttribute('disabled') || ariaDisabled || disabledClass));
  const selectors = [
    '[role="dialog"]', 'dialog', '[role="alert"]', '[aria-live="assertive"]',
    '[aria-live="polite"]', '[class*="toast"]', '[class*="message"]',
    '[class*="notice"]', '[class*="warning"]', '[class*="error"]'
  ];
  const messages = [];
  const seen = new Set();
  for (const selector of selectors) {
    for (const el of document.querySelectorAll(selector)) {
      if (!visible(el)) continue;
      const value = text(el);
      if (!value || value.length > 500 || seen.has(value)) continue;
      seen.add(value);
      messages.push(value);
    }
  }
  return JSON.stringify({
    phone_input_found: phoneInputFound,
    send_button_found: !!send,
    send_button_text: sendText,
    send_button_disabled: disabled,
    send_button_aria_disabled: ariaDisabled,
    send_button_has_disabled_class: disabledClass,
    visible_messages: messages,
    countdown_visible: /重新发送|重新获取|获取验证码\s*\d+\s*秒/i.test(sendText)
  });
}`

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
