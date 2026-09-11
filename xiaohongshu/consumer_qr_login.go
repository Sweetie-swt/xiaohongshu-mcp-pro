package xiaohongshu

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
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

type consumerQRCodeCaptchaSnapshot struct {
	Visible bool   `json:"visible"`
	Selector string `json:"selector"`
}

type consumerQRCodeDiagnosticSnapshot struct {
	DOM                   consumerQRCodeDOMSnapshot
	URL                   string
	Title                 string
	QRState               string
	GateProbeOK           bool
	GatePresent           bool
	GateKind              string
	GateSelector          string
	RedCaptchaProbeOK     bool
	RedCaptchaPresent     bool
	RedCaptchaSelector    string
}

type consumerQRCodeDiagnosticKey struct {
	ModalVisible       bool
	QRCodeVisible      bool
	StatusText         string
	URL                string
	Title              string
	QRState            string
	GateProbeOK        bool
	GatePresent        bool
	GateKind           string
	GateSelector       string
	RedCaptchaProbeOK  bool
	RedCaptchaPresent  bool
	RedCaptchaSelector string
}

var consumerQRCodeDiagnosticHeartbeatOffsets = []time.Duration{
	0,
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
	30 * time.Second,
	60 * time.Second,
	120 * time.Second,
	180 * time.Second,
}

type consumerQRCodeDiagnostics struct {
	start          time.Time
	nextHeartbeat  int
	hasLast        bool
	lastKey        consumerQRCodeDiagnosticKey
	lastSnapshot   consumerQRCodeDiagnosticSnapshot
}

func newConsumerQRCodeDiagnostics() *consumerQRCodeDiagnostics {
	return &consumerQRCodeDiagnostics{start: time.Now()}
}

func (d *consumerQRCodeDiagnostics) observe(page *rod.Page, dom consumerQRCodeDOMSnapshot, seenQRCode bool) {
	if d == nil {
		return
	}
	elapsed := time.Since(d.start)
	snapshot := readConsumerQRCodeDiagnosticSnapshot(page, dom, seenQRCode)
	key := snapshot.key()
	stateChanged := !d.hasLast || key != d.lastKey
	heartbeatDue := false
	for d.nextHeartbeat < len(consumerQRCodeDiagnosticHeartbeatOffsets) &&
		elapsed >= consumerQRCodeDiagnosticHeartbeatOffsets[d.nextHeartbeat] {
		d.nextHeartbeat++
		heartbeatDue = true
	}
	d.lastSnapshot = snapshot
	if stateChanged || heartbeatDue {
		phase := "state_change"
		if heartbeatDue {
			phase = "heartbeat"
			if stateChanged {
				phase = "state_change+heartbeat"
			}
		}
		d.log(phase, elapsed, snapshot, true)
	}
	d.lastKey = key
	d.hasLast = true
}

func (d *consumerQRCodeDiagnostics) logFinal(page *rod.Page, seenQRCode bool) {
	if d == nil {
		return
	}
	dom, err := readConsumerQRCodeDOMSnapshot(page)
	if err != nil {
		if d.hasLast {
			d.log("final_snapshot_dom_unavailable", time.Since(d.start), d.lastSnapshot, false)
			return
		}
		logrus.WithFields(logrus.Fields{
			"phase":       "final_snapshot_dom_unavailable",
			"elapsed_ms":  time.Since(d.start).Milliseconds(),
			"dom_probe_ok": false,
		}).Info("consumer QR diagnostic")
		return
	}
	d.log("final_snapshot", time.Since(d.start), readConsumerQRCodeDiagnosticSnapshot(page, dom, seenQRCode), true)
}

func (d *consumerQRCodeDiagnostics) log(phase string, elapsed time.Duration, snapshot consumerQRCodeDiagnosticSnapshot, domProbeOK bool) {
	logrus.WithFields(logrus.Fields{
		"phase":                 phase,
		"elapsed_ms":            elapsed.Milliseconds(),
		"dom_probe_ok":          domProbeOK,
		"modal_visible":         snapshot.DOM.ModalVisible,
		"qr_code_visible":       snapshot.DOM.QRCodeVisible,
		"qr_state":              snapshot.QRState,
		"status_text":           snapshot.DOM.StatusText,
		"url":                   snapshot.URL,
		"title":                 snapshot.Title,
		"auth_gate_probe_ok":    snapshot.GateProbeOK,
		"auth_gate_present":     snapshot.GatePresent,
		"auth_gate_kind":        snapshot.GateKind,
		"auth_gate_selector":    snapshot.GateSelector,
		"redcaptcha_probe_ok":   snapshot.RedCaptchaProbeOK,
		"redcaptcha_present":    snapshot.RedCaptchaPresent,
		"redcaptcha_selector":   snapshot.RedCaptchaSelector,
	}).Info("consumer QR diagnostic")
}

func (s consumerQRCodeDiagnosticSnapshot) key() consumerQRCodeDiagnosticKey {
	return consumerQRCodeDiagnosticKey{
		ModalVisible:       s.DOM.ModalVisible,
		QRCodeVisible:      s.DOM.QRCodeVisible,
		StatusText:         s.DOM.StatusText,
		URL:                s.URL,
		Title:              s.Title,
		QRState:            s.QRState,
		GateProbeOK:        s.GateProbeOK,
		GatePresent:        s.GatePresent,
		GateKind:           s.GateKind,
		GateSelector:       s.GateSelector,
		RedCaptchaProbeOK:  s.RedCaptchaProbeOK,
		RedCaptchaPresent:  s.RedCaptchaPresent,
		RedCaptchaSelector: s.RedCaptchaSelector,
	}
}

func readConsumerQRCodeDiagnosticSnapshot(page *rod.Page, dom consumerQRCodeDOMSnapshot, seenQRCode bool) consumerQRCodeDiagnosticSnapshot {
	snapshot := consumerQRCodeDiagnosticSnapshot{
		DOM:     dom,
		QRState: consumerQRCodeDiagnosticState(dom, seenQRCode),
	}
	if info, err := page.Info(); err == nil && info != nil {
		snapshot.URL = sanitizeConsumerQRCodeURL(info.URL)
		snapshot.Title = sanitizeConsumerQRCodeText(info.Title)
	}
	if gate, err := ReadConsumerAuthGate(page); err == nil && gate != nil {
		snapshot.GateProbeOK = true
		snapshot.GatePresent = gate.Present
		if gate.Present {
			snapshot.GateKind = sanitizeConsumerQRCodeText(gate.Kind)
			snapshot.GateSelector = sanitizeConsumerQRCodeText(gate.Selector)
		}
	}
	if captcha, err := readConsumerQRCodeCaptchaSnapshot(page); err == nil {
		snapshot.RedCaptchaProbeOK = true
		snapshot.RedCaptchaPresent = captcha.Visible
		snapshot.RedCaptchaSelector = sanitizeConsumerQRCodeText(captcha.Selector)
	}
	return snapshot
}

func consumerQRCodeDiagnosticState(snapshot consumerQRCodeDOMSnapshot, seenQRCode bool) string {
	if state := consumerQRCodeState(snapshot, seenQRCode); state != "" {
		return string(state)
	}
	if snapshot.StatusText != "" {
		return "unknown_status"
	}
	return "unknown"
}

func sanitizeConsumerQRCodeURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "[unavailable]"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func sanitizeConsumerQRCodeText(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 160 {
		return value[:160]
	}
	return value
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
	diagnostics := newConsumerQRCodeDiagnostics()
	defer func() { diagnostics.logFinal(a.page, seenQRCode) }()
	for {
		snapshot, err := readConsumerQRCodeDOMSnapshot(a.page.Context(ctx))
		if err != nil {
			return "", errors.Wrap(err, "读取 consumer QR 登录状态失败")
		}
		if snapshot.QRCodeVisible {
			seenQRCode = true
		}
		diagnostics.observe(a.page, snapshot, seenQRCode)
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

func readConsumerQRCodeCaptchaSnapshot(page *rod.Page) (consumerQRCodeCaptchaSnapshot, error) {
	if page == nil {
		return consumerQRCodeCaptchaSnapshot{}, errors.New("consumer QR page is nil")
	}
	result, err := page.Eval(consumerQRCodeCaptchaSnapshotScript)
	if err != nil {
		return consumerQRCodeCaptchaSnapshot{}, err
	}
	var snapshot consumerQRCodeCaptchaSnapshot
	if err := json.Unmarshal([]byte(result.Value.String()), &snapshot); err != nil {
		return consumerQRCodeCaptchaSnapshot{}, err
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

const consumerQRCodeCaptchaSnapshotScript = `() => {
  const visible = (node) => {
    if (!node) return false;
    const style = window.getComputedStyle(node);
    const rect = node.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || 1) > 0 && rect.width > 0 && rect.height > 0 &&
      node.getAttribute('aria-hidden') !== 'true';
  };
  const selectors = [
    '[id*="redcaptcha" i]', '[class*="redcaptcha" i]',
    '[id*="red-captcha" i]', '[class*="red-captcha" i]',
    '[id*="captcha" i]', '[class*="captcha" i]'
  ];
  for (const selector of selectors) {
    const node = Array.from(document.querySelectorAll(selector)).find(visible);
    if (node) return JSON.stringify({visible: true, selector});
  }
  return JSON.stringify({visible: false, selector: ''});
}`
