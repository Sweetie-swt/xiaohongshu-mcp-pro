package xiaohongshu

import (
	"encoding/base64"
	"strings"
	"testing"
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
	for _, forbidden := range []string{"fetch(", "XMLHttpRequest", "/api/"} {
		if strings.Contains(consumerQRCodeDOMSnapshotScript, forbidden) {
			t.Fatalf("DOM probe script contains forbidden network operation %q", forbidden)
		}
	}
}
