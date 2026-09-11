package xiaohongshu

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConsumerVerifyOTPDiagnosticsAreCompactAndValueSafe(t *testing.T) {
	for _, fragment := range []string{
		"otp_input_found",
		"otp_input_visible",
		"otp_input_value_length",
		"otp_input_placeholder",
		"otp_input_autocomplete",
		"otp_inputmode",
		"login_button_found",
		"login_button_disabled",
		"login_button_aria_disabled",
		"visible_messages",
	} {
		require.Contains(t, consumerVerifyOTPDiagnosticsScript, fragment)
	}
	require.Contains(t, consumerVerifyOTPDiagnosticsScript, "String(otpInput.value || '').length")
	require.NotContains(t, consumerVerifyOTPDiagnosticsScript, "otp_input_value:")
	require.NotContains(t, consumerVerifyOTPDiagnosticsScript, "document.body.innerText")
}

func TestConsumerVerifyOTPButtonStateChangedDetectsClickSideEffects(t *testing.T) {
	before := consumerVerifyOTPDiagnostics{
		LoginButtonFound:         true,
		LoginButtonClass:         "login",
		LoginButtonDisabled:      false,
		LoginButtonPointerEvents: "auto",
	}
	after := before
	after.LoginButtonClass = "login is-disabled"
	after.LoginButtonDisabled = true
	after.LoginButtonPointerEvents = "none"
	require.True(t, consumerVerifyOTPButtonStateChanged(before, after))
	require.False(t, consumerVerifyOTPButtonStateChanged(before, before))
}

func TestConsumerVerifyOTPFailureReasonRecognizesExplicitPageRejections(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{name: "wrong otp", message: "验证码错误", want: "验证码错误或已失效"},
		{name: "expired otp", message: "验证码已过期", want: "验证码错误或已失效"},
		{name: "rate limited", message: "操作频繁，请稍后再试", want: "操作频繁或需要稍后再试"},
		{name: "network", message: "网络请求失败", want: "网络异常"},
		{name: "login failed", message: "登录失败", want: "登录失败"},
		{name: "security", message: "需要安全验证", want: "进一步验证"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Contains(t, consumerVerifyOTPFailureReason([]string{tt.message}), tt.want)
		})
	}
	require.Empty(t, consumerVerifyOTPFailureReason([]string{"登录后推荐更懂你的笔记"}))
}

func TestConsumerVerifyOTPInconclusiveRequiresSixDigitsClickAndLoginGate(t *testing.T) {
	diagnostics := consumerVerifyOTPDiagnostics{
		OTPInputValueLength:  6,
		LoginClickDispatched: true,
		AuthGatePresent:      true,
		AuthGateKind:         "login",
	}
	require.True(t, consumerVerifyOTPShouldBeDiagnosticInconclusive(diagnostics))

	diagnostics.VisibleMessages = []string{"验证码错误"}
	require.False(t, consumerVerifyOTPShouldBeDiagnosticInconclusive(diagnostics))
	diagnostics.VisibleMessages = nil
	diagnostics.AuthGateKind = "verification"
	require.False(t, consumerVerifyOTPShouldBeDiagnosticInconclusive(diagnostics))
	diagnostics.AuthGateKind = "login"
	diagnostics.OTPInputValueLength = 5
	require.False(t, consumerVerifyOTPShouldBeDiagnosticInconclusive(diagnostics))
}

func TestConsumerVerifyOTPCompactGateDescriptionOmitsModalText(t *testing.T) {
	diagnostics := consumerVerifyOTPDiagnostics{
		AuthGatePresent:  true,
		AuthGateKind:     "login",
		AuthGateSelector: "div.reds-modal.reds-modal-open.login-modal",
		AuthGateClass:    "reds-modal login-modal",
	}
	description := compactConsumerAuthGateDescription(diagnostics)
	require.Contains(t, description, "kind=login")
	require.Contains(t, description, "selector=div.reds-modal.reds-modal-open.login-modal")
	require.Contains(t, description, "class=\"reds-modal login-modal\"")
	require.NotContains(t, description, "登录后推荐更懂你的笔记")
	require.NotContains(t, strings.ToLower(description), "otp")
}

func TestConsumerVerifyOTPDiagnosticStagesAreWired(t *testing.T) {
	source := string(mustReadConsumerVerifySource(t))
	for _, stage := range []string{
		"输入 OTP 前",
		"输入 OTP 后、点击登录前",
		"点击登录后立即",
		"点击登录后约 1 秒",
		"最终判定前",
	} {
		require.Contains(t, source, stage)
	}
	require.Contains(t, source, "loginButton.Click(proto.InputMouseButtonLeft, 1)")
	require.Contains(t, source, "networkTrace.Start(context.Background())")
	require.Contains(t, source, "networkTrace.LogSnapshotWithSecurity(\"点击登录后立即\"")
	require.Contains(t, source, "networkTrace.LogSnapshotWithSecurity(\"点击登录后约 1 秒\"")
	require.Contains(t, source, "networkTrace.LogSnapshotWithSecurity(\"最终判定前\"")
}

func TestConsumerVerifySecurityResultKeepsScreenshotReturnPath(t *testing.T) {
	source := string(mustReadConsumerVerifySource(t))
	require.Contains(t, source, "OTPVerificationSecurityVerificationNeeded")
	require.Contains(t, source, "a.page.Screenshot(false, nil)")
	require.Contains(t, source, "SecurityVerificationQR: shot")
	securityBranch := strings.Index(source, "consumerVerifyVisibleSecurityChallenge(finalSecurityNodes)")
	normalErrorBranch := strings.Index(source, "networkOutcome == consumerVerifyOutcomeTransportFailed")
	require.NotEqual(t, -1, securityBranch)
	require.NotEqual(t, -1, normalErrorBranch)
	require.Less(t, securityBranch, normalErrorBranch,
		"security verification must be returned before ordinary network errors")
}

func mustReadConsumerVerifySource(t *testing.T) []byte {
	t.Helper()
	const source = "consumer_login.go"
	data, err := os.ReadFile(source)
	require.NoError(t, err)
	return data
}
