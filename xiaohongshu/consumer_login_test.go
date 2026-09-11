package xiaohongshu

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConsumerAgreementScriptOnlyTargetsExplicitProtocolText(t *testing.T) {
	require.True(t, isConsumerAgreementTextRelevant("我已阅读并同意用户协议和隐私政策"))
	require.True(t, isConsumerAgreementTextRelevant("服务条款"))
	require.False(t, isConsumerAgreementTextRelevant("接收活动通知"))
	require.False(t, isConsumerAgreementTextRelevant("记住我的登录状态"))

	require.Contains(t, consumerAgreementScript, "input[type=\"checkbox\"],[role=\"checkbox\"]")
	require.Contains(t, consumerAgreementScript, "我已阅读并同意")
	require.Contains(t, consumerAgreementScript, "box.click()")
	require.Contains(t, consumerAgreementScript, "checked_transitions")
}

func TestConsumerAgreementDiagnosticsReportDisabledUnacceptedControl(t *testing.T) {
	diagnostics := consumerAgreementDiagnostics{
		Found:              1,
		Clicked:            0,
		CheckedTransitions: 0,
		RemainingUnchecked: 1,
		Labels:             []string{"我已阅读并同意用户协议和隐私政策"},
	}
	require.Equal(t, 1, diagnostics.Found)
	require.Equal(t, 1, diagnostics.RemainingUnchecked)
	require.Zero(t, diagnostics.CheckedTransitions)
	require.Contains(t, diagnostics.Labels[0], "用户协议")
}

func TestConsumerOTPButtonStateChangedDetectsDisabledToCountdown(t *testing.T) {
	before := consumerOTPPageDiagnostics{
		SendButtonText:             "获取验证码",
		SendButtonDisabled:         true,
		SendButtonAriaDisabled:     true,
		SendButtonHasDisabledClass: true,
	}
	after := consumerOTPPageDiagnostics{
		SendButtonText:             "重新发送 59 秒",
		SendButtonDisabled:         true,
		SendButtonAriaDisabled:     true,
		SendButtonHasDisabledClass: true,
		CountdownVisible:           true,
	}
	require.True(t, consumerOTPButtonStateChanged(before, after))
}

func TestConsumerOTPSendStatusKeepsConfirmedUncertainFailedStates(t *testing.T) {
	confirmed, _, confirmedMatch := consumerOTPSendStatusFromPageValue("sent:")
	require.True(t, confirmedMatch)
	require.Equal(t, OTPSendConfirmed, confirmed)

	failed, message, failedMatch := consumerOTPSendStatusFromPageValue("failed:操作频繁")
	require.True(t, failedMatch)
	require.Equal(t, OTPSendFailed, failed)
	require.Equal(t, "操作频繁", message)

	_, _, pendingMatch := consumerOTPSendStatusFromPageValue("pending:")
	require.False(t, pendingMatch)
}

func TestConsumerDiagnosticsRedactPhoneLikeNumbers(t *testing.T) {
	message := sanitizeConsumerDiagnosticText("手机号 153 7635 6205，按钮获取验证码")
	require.NotContains(t, message, "153 7635 6205")
	require.Contains(t, message, "<digits-redacted>")
	require.NotContains(t, strings.Join([]string{message}, ""), "15376356205")
}

func TestConsumerAgreementDOMDiagnosticIsReadOnlyAndStructural(t *testing.T) {
	for _, fragment := range []string{
		"我已阅读并同意",
		"用户协议",
		"隐私政策",
		"隐私协议",
		"隐私条款",
		"服务条款",
		"相关协议",
		"elementsFromPoint",
		"previousElementSibling",
		"nextElementSibling",
		"aria-checked",
		"pointer_events",
		"interactive_hint",
	} {
		require.Contains(t, consumerAgreementDOMDiagnosticScript, fragment)
	}
	require.NotContains(t, consumerAgreementDOMDiagnosticScript, "input.value")
	require.NotContains(t, consumerAgreementDOMDiagnosticScript, ".click()")
}

func TestConsumerCustomAgreementFallbackIsScopedAndReadOnly(t *testing.T) {
	for _, fragment := range []string{
		"div.agreements",
		"span.agree-icon",
		"我已阅读并同意",
		"用户协议",
		"隐私政策",
	} {
		require.Contains(t, consumerCustomAgreementIconScript, fragment)
		require.Contains(t, consumerCustomAgreementFingerprintScript, fragment)
	}
	for _, fragment := range []string{"div.icon-wrapper", "computed_style", "icon_nodes", "pointer_events"} {
		require.Contains(t, consumerCustomAgreementFingerprintScript, fragment)
	}
	require.Contains(t, consumerCustomAgreementStateScript, "div.icon-wrapper.agreed")
	require.Contains(t, consumerCustomAgreementStateScript, "svg.reds-icon.icon")
	require.NotContains(t, consumerCustomAgreementStateScript, "querySelectorAll('.agree-icon')")
	require.NotContains(t, consumerCustomAgreementIconScript, ".click()")
	require.NotContains(t, consumerCustomAgreementFingerprintScript, ".click()")
	require.NotContains(t, consumerCustomAgreementIconScript, "input[type=\"checkbox\"]")
}

func TestConsumerCustomAgreementFingerprintTransitionDetectsStructuralChange(t *testing.T) {
	before := consumerCustomAgreementFingerprint{
		Found: true,
		Icon: consumerCustomNodeFingerprint{
			ClassName:     "agree-icon",
			ComputedStyle: consumerCustomComputedStyle{Background: "transparent"},
		},
	}
	after := before
	after.Icon.ClassName = "agree-icon selected"
	after.Icon.ComputedStyle.Background = "rgb(255, 36, 66)"

	require.True(t, consumerCustomAgreementFingerprintChanged(before, after))
	require.False(t, consumerCustomAgreementFingerprintChanged(before, before))
}

func TestConsumerCustomAgreementResultDefaultsToNoInteraction(t *testing.T) {
	result := &consumerCustomAgreementResult{}
	require.False(t, result.Found)
	require.False(t, result.AlreadyAgreed)
	require.False(t, result.Clicked)
	require.False(t, result.Confirmed)
}

func TestConsumerCustomAgreementStateRequiresAgreedWrapper(t *testing.T) {
	require.False(t, consumerCustomAgreementStateConfirmed(consumerCustomAgreementState{}))
	require.False(t, consumerCustomAgreementStateConfirmed(consumerCustomAgreementState{Found: true}))
	require.True(t, consumerCustomAgreementStateConfirmed(consumerCustomAgreementState{
		Found:         true,
		AlreadyAgreed: true,
	}))
}

func TestConsumerAgreementReadyForOTPSendPreservesStandardAndCustomPriority(t *testing.T) {
	require.False(t, consumerShouldUseCustomAgreement(consumerAgreementDiagnostics{Found: 1}))
	require.True(t, consumerShouldUseCustomAgreement(consumerAgreementDiagnostics{}))
	require.True(t, consumerAgreementReadyForOTPSend(
		consumerAgreementDiagnostics{Found: 1}, nil))
	require.True(t, consumerAgreementReadyForOTPSend(
		consumerAgreementDiagnostics{}, &consumerCustomAgreementResult{Found: true, Confirmed: true}))
	require.False(t, consumerAgreementReadyForOTPSend(
		consumerAgreementDiagnostics{}, &consumerCustomAgreementResult{Found: true, Confirmed: false}))
	require.False(t, consumerAgreementReadyForOTPSend(consumerAgreementDiagnostics{}, nil))
}
