package xiaohongshu

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
)

type consumerVerifyOTPDiagnostics struct {
	OTPInputFound            bool     `json:"otp_input_found"`
	OTPInputVisible          bool     `json:"otp_input_visible"`
	OTPInputValueLength      int      `json:"otp_input_value_length"`
	OTPInputPlaceholder      string   `json:"otp_input_placeholder"`
	OTPInputName             string   `json:"otp_input_name"`
	OTPInputType             string   `json:"otp_input_type"`
	OTPInputAutocomplete     string   `json:"otp_input_autocomplete"`
	OTPInputMode             string   `json:"otp_inputmode"`
	OTPInputMaxLength        int      `json:"otp_input_max_length"`
	LoginButtonFound         bool     `json:"login_button_found"`
	LoginButtonTag           string   `json:"login_button_tag"`
	LoginButtonClass         string   `json:"login_button_class"`
	LoginButtonRole          string   `json:"login_button_role"`
	LoginButtonText          string   `json:"login_button_text"`
	LoginButtonDisabled      bool     `json:"login_button_disabled"`
	LoginButtonAriaDisabled  bool     `json:"login_button_aria_disabled"`
	LoginButtonDisabledClass bool     `json:"login_button_disabled_class"`
	LoginButtonPointerEvents string   `json:"login_button_pointer_events"`
	LoginButtonVisibility    string   `json:"login_button_visibility"`
	VisibleMessages          []string `json:"visible_messages"`
	AuthGatePresent          bool     `json:"auth_gate_present"`
	AuthGateKind             string   `json:"auth_gate_kind"`
	AuthGateSelector         string   `json:"auth_gate_selector"`
	AuthGateClass            string   `json:"auth_gate_class"`
	LoginClickDispatched     bool     `json:"login_click_dispatched"`
	ButtonStateChanged       bool     `json:"button_state_changed"`
}

func (a *ConsumerLoginAction) collectConsumerVerifyOTPDiagnostics() consumerVerifyOTPDiagnostics {
	diagnostics := consumerVerifyOTPDiagnostics{}
	result, err := a.page.Eval(consumerVerifyOTPDiagnosticsScript)
	if err != nil {
		logrus.Warnf("consumer VerifyOTP 诊断读取 DOM 失败: %v", err)
	} else if err := json.Unmarshal([]byte(result.Value.String()), &diagnostics); err != nil {
		logrus.Warnf("consumer VerifyOTP 诊断解析 DOM 结果失败: %v", err)
	}
	if gate, err := ReadConsumerAuthGate(a.page); err == nil && gate.Present {
		diagnostics.AuthGatePresent = true
		diagnostics.AuthGateKind = gate.Kind
		diagnostics.AuthGateSelector = gate.Selector
		diagnostics.AuthGateClass = gate.ClassName
	}
	return diagnostics
}

func (a *ConsumerLoginAction) logConsumerVerifyOTPDiagnostics(stage string, diagnostics consumerVerifyOTPDiagnostics) {
	messages := make([]string, 0, len(diagnostics.VisibleMessages))
	for _, message := range diagnostics.VisibleMessages {
		messages = append(messages, sanitizeConsumerDiagnosticText(message))
	}
	logrus.Infof("consumer VerifyOTP 诊断[%s]: otp_input_found=%t otp_input_visible=%t otp_input_value_length=%d placeholder=%s name=%s type=%s autocomplete=%s inputmode=%s maxlength=%d login_button_found=%t tag=%s class=%s role=%s text=%s disabled=%t aria_disabled=%t disabled_class=%t pointer_events=%s visibility=%s login_click_dispatched=%t button_state_changed=%t visible_messages=%s auth_gate_present=%t auth_gate_kind=%s auth_gate_selector=%s auth_gate_class=%s",
		stage,
		diagnostics.OTPInputFound,
		diagnostics.OTPInputVisible,
		diagnostics.OTPInputValueLength,
		sanitizeConsumerDiagnosticText(diagnostics.OTPInputPlaceholder),
		sanitizeConsumerDiagnosticText(diagnostics.OTPInputName),
		sanitizeConsumerDiagnosticText(diagnostics.OTPInputType),
		sanitizeConsumerDiagnosticText(diagnostics.OTPInputAutocomplete),
		sanitizeConsumerDiagnosticText(diagnostics.OTPInputMode),
		diagnostics.OTPInputMaxLength,
		diagnostics.LoginButtonFound,
		sanitizeConsumerDiagnosticText(diagnostics.LoginButtonTag),
		sanitizeConsumerDiagnosticText(diagnostics.LoginButtonClass),
		sanitizeConsumerDiagnosticText(diagnostics.LoginButtonRole),
		sanitizeConsumerDiagnosticText(diagnostics.LoginButtonText),
		diagnostics.LoginButtonDisabled,
		diagnostics.LoginButtonAriaDisabled,
		diagnostics.LoginButtonDisabledClass,
		sanitizeConsumerDiagnosticText(diagnostics.LoginButtonPointerEvents),
		sanitizeConsumerDiagnosticText(diagnostics.LoginButtonVisibility),
		diagnostics.LoginClickDispatched,
		diagnostics.ButtonStateChanged,
		sanitizeConsumerDiagnosticText(strings.Join(messages, " | ")),
		diagnostics.AuthGatePresent,
		sanitizeConsumerDiagnosticText(diagnostics.AuthGateKind),
		sanitizeConsumerDiagnosticText(diagnostics.AuthGateSelector),
		sanitizeConsumerDiagnosticText(diagnostics.AuthGateClass))
}

func consumerVerifyOTPButtonStateChanged(before, after consumerVerifyOTPDiagnostics) bool {
	return before.LoginButtonFound != after.LoginButtonFound ||
		before.LoginButtonClass != after.LoginButtonClass ||
		before.LoginButtonDisabled != after.LoginButtonDisabled ||
		before.LoginButtonAriaDisabled != after.LoginButtonAriaDisabled ||
		before.LoginButtonDisabledClass != after.LoginButtonDisabledClass ||
		before.LoginButtonPointerEvents != after.LoginButtonPointerEvents ||
		before.LoginButtonVisibility != after.LoginButtonVisibility
}

func consumerVerifyOTPFailureReason(messages []string) string {
	for _, message := range messages {
		compact := strings.Join(strings.Fields(message), " ")
		switch {
		case strings.Contains(compact, "验证码错误") ||
			strings.Contains(compact, "验证码不正确") ||
			strings.Contains(compact, "验证码失效") ||
			strings.Contains(compact, "验证码已过期") ||
			strings.Contains(compact, "校验码错误"):
			return "页面提示验证码错误或已失效"
		case strings.Contains(compact, "操作频繁") ||
			strings.Contains(compact, "请求频繁") ||
			strings.Contains(compact, "请稍后再试"):
			return "页面提示操作频繁或需要稍后再试"
		case strings.Contains(compact, "网络异常") ||
			strings.Contains(compact, "网络错误") ||
			strings.Contains(compact, "网络请求失败"):
			return "页面提示网络异常"
		case strings.Contains(compact, "登录失败"):
			return "页面提示登录失败"
		case strings.Contains(compact, "需要验证") ||
			strings.Contains(compact, "需要安全验证") ||
			strings.Contains(compact, "身份验证"):
			return "页面提示需要进一步验证"
		}
	}
	return ""
}

func consumerVerifyOTPShouldBeDiagnosticInconclusive(diagnostics consumerVerifyOTPDiagnostics) bool {
	return diagnostics.OTPInputValueLength == 6 &&
		diagnostics.LoginClickDispatched &&
		diagnostics.AuthGatePresent &&
		diagnostics.AuthGateKind == "login" &&
		consumerVerifyOTPFailureReason(diagnostics.VisibleMessages) == ""
}

func compactConsumerAuthGateDescription(diagnostics consumerVerifyOTPDiagnostics) string {
	if !diagnostics.AuthGatePresent {
		return "no consumer auth gate"
	}
	return fmt.Sprintf("kind=%s selector=%s class=%q",
		diagnostics.AuthGateKind,
		diagnostics.AuthGateSelector,
		diagnostics.AuthGateClass)
}

const consumerVerifyOTPDiagnosticsScript = `() => {
  const visible = (node) => {
    if (!node || node.nodeType !== 1) return false;
    const style = window.getComputedStyle(node);
    const rect = node.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || 1) > 0 && rect.width > 0 && rect.height > 0 &&
      node.getAttribute('aria-hidden') !== 'true';
  };
  const clip = (value, limit = 180) => String(value || '').replace(/\s+/g, ' ').trim().slice(0, limit);
  const text = (node) => clip(node ? (node.innerText || node.textContent || '') : '');
  const otpInput = Array.from(document.querySelectorAll('input')).find((input) => {
    const hint = [input.placeholder, input.name, input.autocomplete, input.inputMode]
      .map(value => String(value || '').toLowerCase()).join(' ');
    return visible(input) && (/验证码|校验码|one-time|otp/.test(hint) || input.maxLength === 6);
  });
  const loginButton = Array.from(document.querySelectorAll('button, [role="button"], a'))
    .find((node) => visible(node) && text(node).replace(/\s+/g, '') === '登录');
  const buttonStyle = loginButton ? window.getComputedStyle(loginButton) : null;
  const buttonClass = loginButton ? clip(loginButton.getAttribute('class') || '') : '';
  const ariaDisabled = !!(loginButton && loginButton.getAttribute('aria-disabled') === 'true');
  const disabledClass = !!(loginButton && /(^|\s)(disabled|is-disabled)(\s|$)/i.test(buttonClass));
  const messages = [];
  const seen = new Set();
  const messageSelectors = [
    '[role="alert"]', '[aria-live="assertive"]', '[aria-live="polite"]',
    '[class*="toast"]', '[class*="message"]', '[class*="notice"]',
    '[class*="warning"]', '[class*="error"]', '[class*="invalid"]',
    '[class*="validation"]', '[data-testid*="error"]', '[data-testid*="message"]'
  ];
  for (const selector of messageSelectors) {
    for (const node of document.querySelectorAll(selector)) {
      if (!visible(node)) continue;
      const value = text(node);
      if (!value || value.length > 180 || seen.has(value)) continue;
      seen.add(value);
      messages.push(value);
      if (messages.length >= 12) break;
    }
    if (messages.length >= 12) break;
  }
  return JSON.stringify({
    otp_input_found: !!otpInput,
    otp_input_visible: !!otpInput && visible(otpInput),
    otp_input_value_length: otpInput ? String(otpInput.value || '').length : 0,
    otp_input_placeholder: otpInput ? clip(otpInput.getAttribute('placeholder') || '') : '',
    otp_input_name: otpInput ? clip(otpInput.getAttribute('name') || '') : '',
    otp_input_type: otpInput ? clip(otpInput.getAttribute('type') || '') : '',
    otp_input_autocomplete: otpInput ? clip(otpInput.getAttribute('autocomplete') || '') : '',
    otp_inputmode: otpInput ? clip(otpInput.inputMode || otpInput.getAttribute('inputmode') || '') : '',
    otp_input_max_length: otpInput ? Number(otpInput.maxLength || -1) : -1,
    login_button_found: !!loginButton,
    login_button_tag: loginButton ? String(loginButton.tagName || '').toLowerCase() : '',
    login_button_class: buttonClass,
    login_button_role: loginButton ? clip(loginButton.getAttribute('role') || '') : '',
    login_button_text: loginButton ? '登录' : '',
    login_button_disabled: !!(loginButton && (loginButton.disabled || loginButton.hasAttribute('disabled') || ariaDisabled || disabledClass)),
    login_button_aria_disabled: ariaDisabled,
    login_button_disabled_class: disabledClass,
    login_button_pointer_events: buttonStyle ? String(buttonStyle.pointerEvents || '') : '',
    login_button_visibility: buttonStyle ? String(buttonStyle.visibility || '') : '',
    visible_messages: messages
  });
}`
