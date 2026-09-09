package xiaohongshu

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const creatorLoginURL = "https://creator.xiaohongshu.com/login"

// CreatorLoginAction creator 手机号登录流程
type CreatorLoginAction struct {
	page *rod.Page
}

func NewCreatorLogin(page *rod.Page) *CreatorLoginAction {
	return &CreatorLoginAction{page: page}
}

// NavigateToLogin 导航到 creator 登录页，截图返回给用户确认
func (a *CreatorLoginAction) NavigateToLogin() ([]byte, error) {
	pp := a.page.Timeout(20 * time.Second)
	if err := pp.Navigate(creatorLoginURL); err != nil {
		return nil, errors.Wrap(err, "导航到 creator 登录页失败")
	}
	if err := pp.WaitLoad(); err != nil {
		logrus.Warnf("creator 登录页加载超时（non-fatal）: %v", err)
	}
	time.Sleep(2 * time.Second)
	return pp.Screenshot(false, nil)
}

// OTPSendResult 是手机号验证码发送阶段的页面结果。
// 即使返回 error，也尽量保留截图和页面诊断对应的消息供调用方展示。
type OTPSendResult struct {
	Screenshot []byte
	Message    string
}

type otpPageDiagnostics struct {
	URL                   string   `json:"url"`
	Title                 string   `json:"title"`
	PhoneInputFound       bool     `json:"phone_input_found"`
	SendButtonFound       bool     `json:"send_button_found"`
	SendButtonText        string   `json:"send_button_text"`
	SendButtonDisabled    bool     `json:"send_button_disabled"`
	SendButtonClass       string   `json:"send_button_class"`
	VisibleMessages       []string `json:"visible_messages"`
	VisiblePageText       string   `json:"visible_page_text"`
	ProtocolCheckboxFound int      `json:"protocol_checkbox_found"`
	ProtocolCheckboxClick int      `json:"protocol_checkbox_clicked"`
	ProtocolLabels        []string `json:"protocol_labels"`
}

type protocolCheckboxDiagnostics struct {
	Found              int      `json:"found"`
	Clicked            int      `json:"clicked"`
	RemainingUnchecked int      `json:"remaining_unchecked"`
	Labels             []string `json:"labels"`
}

// SendOTP 填写手机号并点击发送验证码，返回页面确认结果和截图。
func (a *CreatorLoginAction) SendOTP(phone string) (*OTPSendResult, error) {
	pp := a.page.Timeout(15 * time.Second)

	beforeInput := a.collectOTPPageDiagnostics()
	a.logOTPPageDiagnostics("填写手机号前", beforeInput)

	// 用 JS native setter 设置手机号，确保触发 Vue 响应式事件
	// creator 登录页的 input 没有 type="tel"，且普通 Input() 不触发 Vue 的双向绑定
	set, err := a.page.Eval(fmt.Sprintf(`() => {
		const inp = document.querySelector("input[placeholder*='手机']");
		if (!inp) return false;
		const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
		setter.call(inp, %q);
		inp.dispatchEvent(new Event('input',  { bubbles: true }));
		inp.dispatchEvent(new Event('change', { bubbles: true }));
		inp.dispatchEvent(new Event('blur',   { bubbles: true }));
		return true;
	}`, phone))
	if err != nil || !set.Value.Bool() {
		shot, _ := pp.Screenshot(false, nil)
		saveDebugShot("creator-login-no-phone-input", shot)
		return &OTPSendResult{
			Screenshot: shot,
			Message:    "验证码发送失败：未找到手机号输入框",
		}, errors.New("未找到手机号输入框")
	}
	time.Sleep(1 * time.Second) // 等待 Vue 响应式更新按钮状态

	afterInput := a.collectOTPPageDiagnostics()
	a.logOTPPageDiagnostics("填写手机号后", afterInput)

	// 只在 checkbox 的关联文本明确包含用户协议/隐私协议时勾选，
	// 不对页面上的无关 checkbox 做猜测性点击。
	if err := a.ensureCreatorAgreementChecked(); err != nil {
		shot, _ := pp.Screenshot(false, nil)
		saveDebugShot("creator-login-agreement-check", shot)
		return &OTPSendResult{
			Screenshot: shot,
			Message:    "验证码发送失败：" + err.Error(),
		}, err
	}

	beforeClick := a.collectOTPPageDiagnostics()
	a.logOTPPageDiagnostics("点击发送验证码前", beforeClick)

	// 用 JS 匹配直接文本节点为"发送验证码"的元素并点击
	// 避免匹配到包含该文字的父容器
	clicked, err := a.page.Eval(`() => {
		for (const el of document.querySelectorAll('*')) {
			const direct = Array.from(el.childNodes)
				.filter(n => n.nodeType === 3)
				.map(n => n.textContent.trim())
				.join('');
			if (direct === '发送验证码') {
				el.click();
				return true;
			}
		}
		return false;
	}`)
	if err != nil || !clicked.Value.Bool() {
		shot, _ := pp.Screenshot(false, nil)
		saveDebugShot("creator-login-no-otp-btn", shot)
		return &OTPSendResult{
			Screenshot: shot,
			Message:    "验证码发送失败：未找到发送验证码按钮",
		}, errors.New("未找到发送验证码按钮")
	}
	afterClick := a.collectOTPPageDiagnostics()
	logrus.Infof("creator OTP 诊断[发送验证码点击结果]: found=%t clicked=%t button_state_changed=%t",
		afterClick.SendButtonFound, clicked.Value.Bool(), otpButtonStateChanged(beforeClick, afterClick))
	a.logOTPPageDiagnostics("点击发送验证码后立即", afterClick)

	// 点击只是触发页面行为；必须等待并读取页面反馈，不能把 click 成功当成短信发送成功。
	final, message, resultErr := a.waitForOTPSendResult(beforeClick)
	a.logOTPPageDiagnostics("发送验证码结果", final)

	shot, shotErr := pp.Screenshot(false, nil)
	if shotErr != nil {
		logrus.Warnf("creator OTP 结果截图失败: %v", shotErr)
	}
	if resultErr != nil {
		saveDebugShot("creator-login-otp-send-result", shot)
		return &OTPSendResult{Screenshot: shot, Message: message}, resultErr
	}

	return &OTPSendResult{Screenshot: shot, Message: message}, nil
}

func (a *CreatorLoginAction) collectOTPPageDiagnostics() otpPageDiagnostics {
	d := otpPageDiagnostics{}
	if info, err := a.page.Info(); err == nil {
		d.URL = info.URL
		d.Title = info.Title
	} else {
		logrus.Warnf("creator OTP 诊断读取 page URL/title 失败: %v", err)
	}

	result, err := a.page.Eval(`() => {
		const visible = (el) => {
			if (!el) return false;
			const style = window.getComputedStyle(el);
			const rect = el.getBoundingClientRect();
			return style.display !== 'none' && style.visibility !== 'hidden' &&
				style.opacity !== '0' && rect.width > 0 && rect.height > 0;
		};
		const text = (el) => (el ? (el.innerText || el.textContent || '') : '')
			.replace(/\s+/g, ' ').trim();
		const directText = (el) => Array.from(el.childNodes)
			.filter(n => n.nodeType === 3)
			.map(n => n.textContent.trim()).join('');
		const sendDirect = Array.from(document.querySelectorAll('*'))
			.find(el => directText(el) === '发送验证码');
		let send = sendDirect;
		if (sendDirect) {
			send = sendDirect.closest('button,[role="button"],a') || sendDirect;
		}
		if (!send || !visible(send)) {
			send = Array.from(document.querySelectorAll('button,[role="button"],a,*'))
				.find(el => visible(el) && /重新发送|重新获取|验证码.*(?:秒|s)/i.test(text(el)));
		}
		const disabled = !!(send && (
			send.disabled || send.hasAttribute('disabled') ||
			send.getAttribute('aria-disabled') === 'true' ||
			send.classList.contains('disabled') || send.classList.contains('is-disabled')
		));
		const selectors = [
			'[role="dialog"]', 'dialog', '[role="alert"]', '[aria-live="assertive"]',
			'[aria-live="polite"]', '[class*="modal"]', '[class*="dialog"]',
			'[class*="toast"]', '[class*="message"]', '[class*="notice"]',
			'[class*="warning"]', '[class*="error"]'
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
			phone_input_found: !!document.querySelector("input[placeholder*='手机']"),
			send_button_found: !!sendDirect,
			send_button_text: text(send),
			send_button_disabled: disabled,
			send_button_class: send ? String(send.className || '') : '',
			visible_messages: messages,
			visible_page_text: text(document.body).slice(0, 12000)
		});
	}`)
	if err != nil {
		logrus.Warnf("creator OTP 诊断读取 DOM 失败: %v", err)
		return d
	}
	if err := json.Unmarshal([]byte(result.Value.String()), &d); err != nil {
		logrus.Warnf("creator OTP 诊断解析 DOM 结果失败: %v", err)
	}
	d.URL = firstNonEmpty(d.URL, pageURL(a.page))
	return d
}

func (a *CreatorLoginAction) ensureCreatorAgreementChecked() error {
	result, err := a.page.Eval(`() => {
		const visible = (el) => {
			if (!el) return false;
			const style = window.getComputedStyle(el);
			const rect = el.getBoundingClientRect();
			return style.display !== 'none' && style.visibility !== 'hidden' &&
				style.opacity !== '0' && rect.width > 0 && rect.height > 0;
		};
		const protocolWords = /(用户协议|隐私政策|隐私协议|隐私条款|服务条款)/;
		const text = (el) => (el ? (el.innerText || el.textContent || '') : '')
			.replace(/\s+/g, ' ').trim();
		const labels = [];
		let found = 0;
		let clicked = 0;
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
			if (!associated) {
				let parent = box.parentElement;
				for (let i = 0; i < 3 && parent; i++, parent = parent.parentElement) {
					const candidate = text(parent);
					if (candidate && candidate.length <= 300 && protocolWords.test(candidate)) {
						associated = candidate;
						break;
					}
				}
			}
			if (!associated || !protocolWords.test(associated)) continue;
			found++;
			labels.push(associated);
			const checked = box.type === 'checkbox'
				? box.checked
				: box.getAttribute('aria-checked') === 'true';
			if (!checked) {
				box.click();
				clicked++;
			}
			const checkedAfter = box.type === 'checkbox'
				? box.checked
				: box.getAttribute('aria-checked') === 'true';
			if (!checkedAfter) remainingUnchecked++;
		}
		return JSON.stringify({found, clicked, remaining_unchecked: remainingUnchecked, labels});
	}`)
	if err != nil {
		logrus.Warnf("creator OTP 协议 checkbox 诊断失败: %v", err)
		return nil
	}

	var d protocolCheckboxDiagnostics
	if err := json.Unmarshal([]byte(result.Value.String()), &d); err != nil {
		logrus.Warnf("creator OTP 协议 checkbox 结果解析失败: %v", err)
		return nil
	}
	logrus.Infof("creator OTP 协议 checkbox: found=%d clicked=%d remaining_unchecked=%d labels=%s",
		d.Found, d.Clicked, d.RemainingUnchecked, strings.Join(d.Labels, " | "))
	if d.RemainingUnchecked > 0 {
		return errors.New("已识别到未勾选的用户协议/隐私协议 checkbox，但勾选后仍未选中")
	}
	return nil
}

func (a *CreatorLoginAction) waitForOTPSendResult(before otpPageDiagnostics) (otpPageDiagnostics, string, error) {
	deadline := time.Now().Add(6 * time.Second)
	latest := a.collectOTPPageDiagnostics()
	for {
		if signal := otpSuccessSignal(latest); signal != "" {
			return latest, "验证码发送成功：" + signal, nil
		}
		if message := otpErrorSignal(latest); message != "" {
			return latest, "验证码发送失败：" + message, errors.Errorf("页面提示：%s", message)
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(300 * time.Millisecond)
		latest = a.collectOTPPageDiagnostics()
	}

	if message := otpErrorSignal(latest); message != "" {
		return latest, "验证码发送失败：" + message, errors.Errorf("页面提示：%s", message)
	}
	logrus.Warnf("creator OTP 发送状态无法确认：按钮点击前 text=%s，点击后 text=%s，disabled=%t，class=%s",
		before.SendButtonText, latest.SendButtonText, latest.SendButtonDisabled, latest.SendButtonClass)
	return latest, "验证码发送状态无法确认：未检测到倒计时、成功文案或明确错误提示",
		errors.New("验证码发送状态无法确认")
}

func otpSuccessSignal(d otpPageDiagnostics) string {
	for _, text := range append([]string{d.SendButtonText}, append(d.VisibleMessages, d.VisiblePageText)...) {
		if strings.Contains(text, "验证码已发送") || strings.Contains(text, "验证码发送成功") ||
			strings.Contains(text, "短信已发送") || strings.Contains(text, "发送成功") {
			return truncateDiagnosticText(text, 240)
		}
	}
	if isOTPSendCountdown(d.SendButtonText) {
		return "发送验证码按钮进入倒计时：" + strings.TrimSpace(d.SendButtonText)
	}
	return ""
}

func otpErrorSignal(d otpPageDiagnostics) string {
	for _, text := range d.VisibleMessages {
		clean := strings.TrimSpace(text)
		if clean == "" || otpSuccessSignal(otpPageDiagnostics{VisiblePageText: clean}) != "" {
			continue
		}
		for _, marker := range []string{"失败", "错误", "无效", "不支持", "暂不", "频繁", "稍后", "重试", "风控", "风险", "限制", "请先", "勾选", "过期", "异常", "禁止", "海外", "升级中", "error", "invalid", "warning"} {
			if strings.Contains(strings.ToLower(clean), strings.ToLower(marker)) {
				return clean
			}
		}
	}
	return ""
}

func isOTPSendCountdown(text string) bool {
	text = strings.TrimSpace(strings.ToLower(text))
	if text == "" {
		return false
	}
	hasCountdownWord := strings.Contains(text, "重新发送") || strings.Contains(text, "重新获取") ||
		strings.Contains(text, "倒计时") || strings.Contains(text, "秒") || strings.Contains(text, "sec")
	if !hasCountdownWord {
		return false
	}
	for _, r := range text {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func otpButtonStateChanged(before, after otpPageDiagnostics) bool {
	return before.SendButtonText != after.SendButtonText ||
		before.SendButtonDisabled != after.SendButtonDisabled ||
		before.SendButtonClass != after.SendButtonClass
}

func truncateDiagnosticText(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	if len([]rune(text)) <= maxRunes {
		return text
	}
	return string([]rune(text)[:maxRunes]) + "…"
}

func (a *CreatorLoginAction) logOTPPageDiagnostics(stage string, d otpPageDiagnostics) {
	logrus.Infof("creator OTP 诊断[%s]: URL=%s title=%s phone_input_found=%t send_button_found=%t send_button_text=%s send_button_disabled=%t send_button_class=%s",
		stage, d.URL, d.Title, d.PhoneInputFound, d.SendButtonFound, d.SendButtonText, d.SendButtonDisabled, d.SendButtonClass)
	if len(d.VisibleMessages) == 0 {
		logrus.Infof("creator OTP 诊断[%s]: 可见 dialog/modal/toast/alert 文本=<none>", stage)
		return
	}
	logrus.Infof("creator OTP 诊断[%s]: 可见 dialog/modal/toast/alert 文本=%s", stage, strings.Join(d.VisibleMessages, " | "))
}

func pageURL(page *rod.Page) string {
	info, err := page.Info()
	if err != nil {
		return ""
	}
	return info.URL
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// VerifyOTP 填写验证码并提交登录。
// 返回值：(安全验证二维码截图, error)
// 若小红书弹出"安全验证扫码"弹窗，会将截图返回给调用方展示给用户，
// 同时在后台等待最多 120 秒直到 web_session 出现（用户扫码后写入）。
func (a *CreatorLoginAction) VerifyOTP(otp string) ([]byte, error) {
	pp := a.page.Timeout(10 * time.Second)

	// 找到验证码输入框并用 rod 模拟真实键盘输入，确保触发 Vue 响应式
	otpInput, err := pp.Element("input[placeholder*='验证码']")
	if err != nil {
		return nil, errors.New("未找到验证码输入框")
	}
	if err := otpInput.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return nil, errors.Wrap(err, "点击验证码输入框失败")
	}
	// 清空已有内容，再输入验证码，确保 Vue 响应式绑定生效
	if err := otpInput.SelectAllText(); err != nil {
		logrus.Warnf("SelectAllText failed (non-fatal): %v", err)
	}
	if err := otpInput.Input(otp); err != nil {
		return nil, errors.Wrap(err, "输入验证码失败")
	}
	time.Sleep(1 * time.Second)

	if shot, e := a.page.Screenshot(false, nil); e == nil {
		saveDebugShot("creator-before-login-click", shot)
	}

	// 等待登录按钮变为可点击（Vue 验证通过），最多 5 秒
	loginBtn, err := pp.Element(".beer-login-btn")
	if err != nil {
		return nil, errors.New("未找到登录按钮")
	}
	for i := 0; i < 10; i++ {
		d, _ := loginBtn.Attribute("disabled")
		if d == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err := loginBtn.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return nil, errors.Wrap(err, "点击登录按钮失败")
	}
	time.Sleep(3 * time.Second)

	if shot, e := a.page.Screenshot(false, nil); e == nil {
		saveDebugShot("creator-after-login-click", shot)
	}

	// 以 creator 页面离开 /login 为登录成功的信号（比 web_session 更准确）
	if a.creatorLoginDone() {
		return nil, nil
	}

	// 仍在 /login 页面：说明弹出了安全验证二维码，截图后等待用户扫码（最多 120 秒）
	shot, _ := a.page.Screenshot(false, nil)
	logrus.Infof("检测到安全验证弹窗，等待用户扫码（最多 120 秒）")
	for i := 0; i < 60; i++ {
		time.Sleep(2 * time.Second)
		if a.creatorLoginDone() {
			logrus.Infof("扫码验证完成，creator 登录成功")
			return shot, nil
		}
	}
	return nil, errors.New("安全验证超时（120 秒），请重新登录")
}

// creatorLoginDone 检查 creator 页面是否已离开 /login（登录完成的信号）
func (a *CreatorLoginAction) creatorLoginDone() bool {
	info, err := a.page.Info()
	if err != nil {
		return false
	}
	done := !strings.Contains(info.URL, "/login")
	if done {
		logrus.Infof("creator 登录完成，当前 URL: %s", info.URL)
	}
	return done
}

// CheckCreatorLoginStatus 检查 creator 是否已登录
func (a *CreatorLoginAction) CheckCreatorLoginStatus() bool {
	pp := a.page.Timeout(15 * time.Second)
	if err := pp.Navigate("https://creator.xiaohongshu.com"); err != nil {
		return false
	}
	if err := pp.WaitLoad(); err != nil {
		logrus.Warnf("creator 首页加载超时（non-fatal）: %v", err)
	}
	time.Sleep(2 * time.Second)
	info, err := pp.Info()
	if err != nil {
		return false
	}
	return !strings.Contains(info.URL, "/login")
}

func saveDebugShot(name string, shot []byte) {
	if shot == nil {
		return
	}
	path := fmt.Sprintf("/tmp/xhs-%s-%d.png", name, time.Now().Unix())
	_ = os.WriteFile(path, shot, 0644)
	logrus.Infof("调试截图已保存: %s", path)
}
