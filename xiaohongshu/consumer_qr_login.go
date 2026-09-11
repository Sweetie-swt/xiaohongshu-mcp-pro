package xiaohongshu

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/pkg/errors"
)

const (
	consumerQRCodeSelector      = ".login-modal .qrcode.force-light img.qrcode-img"
	consumerQRLoginWaitInterval = 500 * time.Millisecond
	consumerQRImageWaitTimeout  = 60 * time.Second
)

// ConsumerQRCodeWaitStatus describes the terminal browser-side QR state.
// Scanned is intentionally not terminal: the phone still needs confirmation.
type ConsumerQRCodeWaitStatus string

const (
	ConsumerQRCodeLoginReady   ConsumerQRCodeWaitStatus = "qr_ready"
	ConsumerQRCodeLoginScanned ConsumerQRCodeWaitStatus = "scanned"
	ConsumerQRCodeLoginExpired ConsumerQRCodeWaitStatus = "expired"
	ConsumerQRCodeModalMissing ConsumerQRCodeWaitStatus = "modal_missing"
)

type consumerQRCodeDOMSnapshot struct {
	ModalVisible  bool   `json:"modal_visible"`
	QRCodeVisible bool   `json:"qr_code_visible"`
	StatusText    string `json:"status_text"`
	ImageSrc      string `json:"image_src"`
}

// NavigateToQRCodeLogin opens the consumer login page and waits for the
// frontend-rendered QR image. It never calls the QR creation endpoint itself;
// the page's own JavaScript performs that operation.
func (a *ConsumerLoginAction) NavigateToQRCodeLogin(ctx context.Context) ([]byte, error) {
	if a == nil || a.page == nil {
		return nil, errors.New("consumer QR 登录页面不可用")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	pp := a.page.Context(ctx)
	if err := pp.Navigate(consumerLoginURL); err != nil {
		return nil, errors.Wrap(err, "导航到 consumer QR 登录页失败")
	}
	if err := pp.WaitLoad(); err != nil {
		// The QR is rendered by the app after document load; the DOM wait below
		// is the authoritative readiness check. A resource-load timeout is not
		// itself a reason to discard a page that can still render the QR.
	}
	return a.ReadConsumerQRCodeImage(ctx, consumerQRImageWaitTimeout)
}

// ReadConsumerQRCodeImage returns only decoded PNG bytes from the visible QR
// image's data URL. No URL or data URL text is returned or logged.
func (a *ConsumerLoginAction) ReadConsumerQRCodeImage(ctx context.Context, timeout time.Duration) ([]byte, error) {
	if a == nil || a.page == nil {
		return nil, errors.New("consumer QR 登录页面不可用")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = consumerQRImageWaitTimeout
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		snapshot, err := readConsumerQRCodeDOMSnapshot(a.page.Context(ctx))
		if err == nil && snapshot.QRCodeVisible && snapshot.ImageSrc != "" {
			return decodePNGDataURL(snapshot.ImageSrc)
		}
		if err != nil {
			lastErr = err
		} else if snapshot.ImageSrc != "" {
			lastErr = fmt.Errorf("consumer QR image src is not a PNG data URL")
		}
		if !time.Now().Before(deadline) {
			if lastErr != nil {
				return nil, errors.Wrap(lastErr, "等待有效 consumer QR 图片超时")
			}
			return nil, errors.New("等待有效 consumer QR 图片超时")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(consumerQRLoginWaitInterval):
		}
	}
}

// WaitForConsumerQRCodeLogin observes only the browser DOM. The frontend
// already polls its own QR status endpoint, so this method does not make any
// login network request.
func (a *ConsumerLoginAction) WaitForConsumerQRCodeLogin(ctx context.Context, timeout time.Duration) (ConsumerQRCodeWaitStatus, error) {
	if a == nil || a.page == nil {
		return "", errors.New("consumer QR 登录页面不可用")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	deadline := time.Now().Add(timeout)
	seenQRCode := false
	for {
		snapshot, err := readConsumerQRCodeDOMSnapshot(a.page.Context(ctx))
		if err != nil {
			return "", errors.Wrap(err, "读取 consumer QR 登录状态失败")
		}
		if snapshot.QRCodeVisible {
			seenQRCode = true
		}
		switch consumerQRCodeState(snapshot, seenQRCode) {
		case ConsumerQRCodeLoginExpired:
			return ConsumerQRCodeLoginExpired, nil
		case ConsumerQRCodeModalMissing:
			return ConsumerQRCodeModalMissing, nil
		case ConsumerQRCodeLoginScanned:
			// “扫码成功” means scanned-but-not-confirmed and is deliberately
			// ignored as a terminal result.
		}
		if !time.Now().Before(deadline) {
			return "", context.DeadlineExceeded
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(consumerQRLoginWaitInterval):
		}
	}
}

func consumerQRCodeState(snapshot consumerQRCodeDOMSnapshot, seenQRCode bool) ConsumerQRCodeWaitStatus {
	if snapshot.StatusText == "二维码已过期" {
		return ConsumerQRCodeLoginExpired
	}
	if seenQRCode && !snapshot.ModalVisible {
		return ConsumerQRCodeModalMissing
	}
	if snapshot.StatusText == "扫码成功" {
		return ConsumerQRCodeLoginScanned
	}
	if snapshot.ModalVisible && snapshot.QRCodeVisible {
		return ConsumerQRCodeLoginReady
	}
	return ""
}

func readConsumerQRCodeDOMSnapshot(page *rod.Page) (consumerQRCodeDOMSnapshot, error) {
	if page == nil {
		return consumerQRCodeDOMSnapshot{}, errors.New("consumer QR page is nil")
	}
	result, err := page.Eval(consumerQRCodeDOMSnapshotScript)
	if err != nil {
		return consumerQRCodeDOMSnapshot{}, err
	}
	var snapshot consumerQRCodeDOMSnapshot
	if err := json.Unmarshal([]byte(result.Value.String()), &snapshot); err != nil {
		return consumerQRCodeDOMSnapshot{}, err
	}
	return snapshot, nil
}

func decodePNGDataURL(value string) ([]byte, error) {
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(value, prefix) {
		return nil, errors.New("consumer QR image is not a PNG data URL")
	}
	encoded := strings.TrimPrefix(value, prefix)
	if encoded == "" {
		return nil, errors.New("consumer QR PNG data URL is empty")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.Wrap(err, "decode consumer QR PNG data URL")
	}
	if len(decoded) < len(pngSignature) || string(decoded[:len(pngSignature)]) != string(pngSignature) {
		return nil, errors.New("consumer QR data URL does not contain a PNG image")
	}
	return decoded, nil
}

var pngSignature = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

const consumerQRCodeDOMSnapshotScript = `() => {
  const visible = (node) => {
    if (!node) return false;
    const style = window.getComputedStyle(node);
    const rect = node.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || 1) > 0 && rect.width > 0 && rect.height > 0 &&
      node.getAttribute('aria-hidden') !== 'true';
  };
  const modal = document.querySelector('.login-modal');
  const qr = document.querySelector('.login-modal .qrcode.force-light img.qrcode-img');
  const status = document.querySelector('.login-modal .qrcode .status-text');
  return JSON.stringify({
    modal_visible: visible(modal),
    qr_code_visible: visible(qr),
    status_text: visible(status) ? String(status.innerText || status.textContent || '').replace(/\s+/g, ' ').trim() : '',
    image_src: qr ? String(qr.src || '') : ''
  });
}`
