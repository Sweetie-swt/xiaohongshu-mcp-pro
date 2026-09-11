package xiaohongshu

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestDecodePNGDataURL(t *testing.T) {
	png := append(append([]byte(nil), pngSignature...), []byte("fixture")...)
	valid := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)

	got, err := decodePNGDataURL(valid)
	if err != nil {
		t.Fatalf("decodePNGDataURL(valid) error = %v", err)
	}
	if string(got) != string(png) {
		t.Fatalf("decoded PNG bytes = %x, want %x", got, png)
	}

	for name, value := range map[string]string{
		"empty":       "",
		"empty PNG":   "data:image/png;base64,",
		"non-PNG MIME": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(png),
		"invalid PNG": "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("not png")),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodePNGDataURL(value); err == nil {
				t.Fatalf("decodePNGDataURL(%q) unexpectedly succeeded", value)
			}
		})
	}
}

func TestConsumerQRCodeState(t *testing.T) {
	tests := []struct {
		name   string
		snap   consumerQRCodeDOMSnapshot
		seen   bool
		status ConsumerQRCodeWaitStatus
	}{
		{
			name:   "waiting for scan",
			snap:   consumerQRCodeDOMSnapshot{ModalVisible: true, QRCodeVisible: true},
			status: ConsumerQRCodeLoginReady,
		},
		{
			name:   "scanned waits for confirmation",
			snap:   consumerQRCodeDOMSnapshot{ModalVisible: true, QRCodeVisible: true, StatusText: "扫码成功"},
			seen:   true,
			status: ConsumerQRCodeLoginScanned,
		},
		{
			name:   "expired",
			snap:   consumerQRCodeDOMSnapshot{ModalVisible: true, StatusText: "二维码已过期"},
			seen:   true,
			status: ConsumerQRCodeLoginExpired,
		},
		{
			name:   "modal disappeared after QR was shown",
			snap:   consumerQRCodeDOMSnapshot{QRCodeVisible: false},
			seen:   true,
			status: ConsumerQRCodeModalMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := consumerQRCodeState(tt.snap, tt.seen); got != tt.status {
				t.Fatalf("consumerQRCodeState() = %q, want %q", got, tt.status)
			}
		})
	}
	if got := consumerQRCodeState(consumerQRCodeDOMSnapshot{ModalVisible: true, QRCodeVisible: true, StatusText: "扫码成功"}, true); got == ConsumerQRCodeModalMissing || got == ConsumerQRCodeLoginExpired {
		t.Fatalf("scanned state was treated as terminal: %q", got)
	}
}

func TestConsumerQRCodeDOMSnapshotScriptUsesOnlyExistingDOM(t *testing.T) {
	for _, selector := range []string{
		".login-modal",
		".qrcode.force-light img.qrcode-img",
		".qrcode .status-text",
	} {
		if !strings.Contains(consumerQRCodeDOMSnapshotScript, selector) {
			t.Fatalf("DOM probe script is missing selector %q", selector)
		}
	}
	for _, forbidden := range []string{"fetch(", "XMLHttpRequest", "/api/", "cookie", "token", "session"} {
		if strings.Contains(consumerQRCodeDOMSnapshotScript, forbidden) {
			t.Fatalf("DOM probe script contains forbidden network operation %q", forbidden)
		}
	}
}

func TestConsumerQRCodeDiagnosticStateAndHeartbeatSchedule(t *testing.T) {
	if got, want := len(consumerQRCodeDiagnosticHeartbeatOffsets), 8; got != want {
		t.Fatalf("heartbeat count = %d, want %d", got, want)
	}
	wantOffsets := []time.Duration{
		0,
		2 * time.Second,
		5 * time.Second,
		10 * time.Second,
		30 * time.Second,
		60 * time.Second,
		120 * time.Second,
		180 * time.Second,
	}
	for i, want := range wantOffsets {
		if got := consumerQRCodeDiagnosticHeartbeatOffsets[i]; got != want {
			t.Fatalf("heartbeat offset[%d] = %s, want %s", i, got, want)
		}
	}

	if got := consumerQRCodeDiagnosticState(consumerQRCodeDOMSnapshot{
		ModalVisible: true, QRCodeVisible: true, StatusText: "未知页面状态",
	}, true); got != "unknown_status" {
		t.Fatalf("unknown status diagnostic state = %q, want unknown_status", got)
	}
	if got := consumerQRCodeDiagnosticState(consumerQRCodeDOMSnapshot{}, true); got != string(ConsumerQRCodeModalMissing) {
		t.Fatalf("modal-missing diagnostic state = %q, want %q", got, ConsumerQRCodeModalMissing)
	}
}

func TestSanitizeConsumerQRCodeURLRemovesSensitiveURLParts(t *testing.T) {
	got := sanitizeConsumerQRCodeURL("https://www.xiaohongshu.com/login?token=secret#session")
	if want := "https://www.xiaohongshu.com/login"; got != want {
		t.Fatalf("sanitized URL = %q, want %q", got, want)
	}
}

func TestConsumerQRCodeCaptchaSnapshotScriptIsReadOnlyAndRedCaptchaAware(t *testing.T) {
	for _, selector := range []string{"redcaptcha", "red-captcha", "captcha"} {
		if !strings.Contains(consumerQRCodeCaptchaSnapshotScript, selector) {
			t.Fatalf("captcha probe script is missing selector marker %q", selector)
		}
	}
	for _, forbidden := range []string{"fetch(", "XMLHttpRequest", "/api/", "document.cookie"} {
		if strings.Contains(consumerQRCodeCaptchaSnapshotScript, forbidden) {
			t.Fatalf("captcha probe script contains forbidden operation %q", forbidden)
		}
	}
}
