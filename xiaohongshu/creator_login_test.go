package xiaohongshu

import "testing"

func TestCreatorLoginVerificationStatuses(t *testing.T) {
	if OTPVerificationSucceeded != "success" {
		t.Fatalf("success status = %q", OTPVerificationSucceeded)
	}
	if OTPVerificationSecurityVerificationNeeded != "security_verification_required" {
		t.Fatalf("security status = %q", OTPVerificationSecurityVerificationNeeded)
	}
}

func TestCreatorLoginFailureSignal(t *testing.T) {
	tests := []struct {
		name string
		diag otpPageDiagnostics
		want string
	}{
		{
			name: "visible dialog error",
			diag: otpPageDiagnostics{VisibleMessages: []string{"验证码错误，请重新输入"}},
			want: "验证码错误，请重新输入",
		},
		{
			name: "visible page error",
			diag: otpPageDiagnostics{VisiblePageText: "登录失败，请稍后重试"},
			want: "登录失败",
		},
		{
			name: "no error",
			diag: otpPageDiagnostics{VisiblePageText: "请完成安全验证"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := creatorLoginFailureSignal(tt.diag); got != tt.want {
				t.Fatalf("creatorLoginFailureSignal() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCreatorSecurityFailureSignalIgnoresScanInstruction(t *testing.T) {
	if got := creatorSecurityFailureSignal(otpPageDiagnostics{
		VisiblePageText: "请先扫码完成安全验证",
	}); got != "" {
		t.Fatalf("scan instruction was misclassified as failure: %q", got)
	}
	if got := creatorSecurityFailureSignal(otpPageDiagnostics{
		VisibleMessages: []string{"安全验证失败，请重新登录"},
	}); got == "" {
		t.Fatal("expected explicit security failure to be detected")
	}
}

func TestOTPSendCountdownSignal(t *testing.T) {
	if !isOTPSendCountdown("重新发送 59 秒") {
		t.Fatal("expected countdown signal")
	}
	if isOTPSendCountdown("发送验证码") {
		t.Fatal("plain send text must not be a countdown signal")
	}
}
