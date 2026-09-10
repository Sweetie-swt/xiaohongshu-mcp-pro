package xiaohongshu

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/sirupsen/logrus"
)

// ConsumerCookieStatus describes only the presence and scope of web_session
// cookies. It deliberately does not expose cookie values.
type ConsumerCookieStatus struct {
	WebSessionCount      int
	ConsumerScopedCount  int
	ValidConsumerCount   int
	CreatorScopedCount   int
	InvalidConsumerCount int
}

func (s ConsumerCookieStatus) HasValidConsumerSession() bool {
	return s.ValidConsumerCount > 0
}

func (s ConsumerCookieStatus) Summary() string {
	return fmt.Sprintf("web_session=%d consumer_scoped=%d valid_consumer=%d creator_scoped=%d invalid_consumer=%d",
		s.WebSessionCount,
		s.ConsumerScopedCount,
		s.ValidConsumerCount,
		s.CreatorScopedCount,
		s.InvalidConsumerCount)
}

// ReadConsumerCookieStatus reads browser cookie metadata and uses the value
// only as an in-process presence check. No cookie value is returned or logged.
func ReadConsumerCookieStatus(page *rod.Page) (ConsumerCookieStatus, error) {
	if page == nil {
		return ConsumerCookieStatus{}, fmt.Errorf("consumer cookie status: page is nil")
	}
	cookies, err := page.Browser().GetCookies()
	if err != nil {
		return ConsumerCookieStatus{}, fmt.Errorf("get browser cookies: %w", err)
	}
	return inspectConsumerCookieStatus(cookies, time.Now()), nil
}

func inspectConsumerCookieStatus(cookies []*proto.NetworkCookie, now time.Time) ConsumerCookieStatus {
	status := ConsumerCookieStatus{}
	for _, cookie := range cookies {
		if cookie == nil || cookie.Name != "web_session" {
			continue
		}
		status.WebSessionCount++

		domain := normalizedCookieDomain(cookie.Domain)
		if domain == "creator.xiaohongshu.com" {
			status.CreatorScopedCount++
		}
		if !isConsumerCookieDomain(domain) {
			continue
		}
		status.ConsumerScopedCount++
		if isUsableConsumerCookie(cookie, now) {
			status.ValidConsumerCount++
		} else {
			status.InvalidConsumerCount++
		}
	}
	return status
}

func normalizedCookieDomain(domain string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), ".")
}

func isConsumerCookieDomain(domain string) bool {
	switch normalizedCookieDomain(domain) {
	case "xiaohongshu.com", "www.xiaohongshu.com":
		return true
	default:
		return false
	}
}

func isUsableConsumerCookie(cookie *proto.NetworkCookie, now time.Time) bool {
	if cookie == nil || cookie.Name != "web_session" || cookie.Value == "" {
		return false
	}
	if cookie.Path != "/" {
		return false
	}
	// Chrome reports zero for a session cookie. A non-zero expiry is a Unix
	// timestamp in CDP's Network.Cookie representation.
	if cookie.Expires > 0 && float64(now.Unix()) >= float64(cookie.Expires) {
		return false
	}
	return isConsumerCookieDomain(cookie.Domain)
}

// LogConsumerCookieMetadata emits only the metadata needed to diagnose the
// creator/www session handoff. In particular, it never emits cookie values or
// derived token fragments.
func LogConsumerCookieMetadata(page *rod.Page, label string) error {
	if page == nil {
		return fmt.Errorf("cookie metadata: page is nil")
	}
	cookies, err := page.Browser().GetCookies()
	if err != nil {
		return fmt.Errorf("get browser cookies: %w", err)
	}

	webSessionCount := 0
	for _, cookie := range cookies {
		if cookie == nil || cookie.Name != "web_session" {
			continue
		}
		webSessionCount++
		logrus.Infof("[%s] cookie metadata name=%s domain=%s path=%s expires=%.0f secure=%t httponly=%t samesite=%s session=%t",
			label, cookie.Name, cookie.Domain, cookie.Path, float64(cookie.Expires),
			cookie.Secure, cookie.HTTPOnly, cookie.SameSite, cookie.Session)
	}
	if webSessionCount == 0 {
		logrus.Warnf("[%s] cookie metadata contains no web_session entry", label)
	}
	return nil
}

type ConsumerAuthGate struct {
	Present     bool   `json:"present"`
	Kind        string `json:"kind"`
	Selector    string `json:"selector"`
	Tag         string `json:"tag"`
	ID          string `json:"id"`
	ClassName   string `json:"class_name"`
	TextPreview string `json:"text_preview"`
}

func (g *ConsumerAuthGate) Description() string {
	if g == nil || !g.Present {
		return "no consumer auth gate"
	}
	text := strings.Join(strings.Fields(g.TextPreview), " ")
	if len(text) > 160 {
		text = text[:160]
	}
	return fmt.Sprintf("kind=%s selector=%s tag=%s id=%s class=%q text=%q",
		g.Kind, g.Selector, g.Tag, g.ID, g.ClassName, text)
}

// ReadConsumerAuthGate inspects visible login/verification modal structures
// on the current www document. It is intentionally read-only.
func ReadConsumerAuthGate(page *rod.Page) (*ConsumerAuthGate, error) {
	if page == nil {
		return nil, fmt.Errorf("consumer auth gate: page is nil")
	}
	result, err := page.Eval(consumerAuthGateScript)
	if err != nil {
		return nil, fmt.Errorf("evaluate consumer auth gate: %w", err)
	}
	var gate ConsumerAuthGate
	if err := json.Unmarshal([]byte(result.Value.String()), &gate); err != nil {
		return nil, fmt.Errorf("parse consumer auth gate: %w", err)
	}
	return &gate, nil
}

const consumerAuthGateScript = `() => {
  const visible = (node) => {
    if (!node || node.nodeType !== 1) return false;
    const style = window.getComputedStyle(node);
    const rect = node.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || 1) > 0 && rect.width > 0 && rect.height > 0 &&
      node.getAttribute('aria-hidden') !== 'true';
  };

  const authPattern = /登录|手机号|验证码|安全验证|身份验证|扫码|风控|login|verify|verification/i;
  const modalPattern = /modal|dialog|mask|overlay|login|verify|verification/i;
  const candidates = [];
  const add = (selector) => {
    document.querySelectorAll(selector).forEach((node) => candidates.push({node, selector}));
  };

  // Keep the historical structure explicit, but also inspect generic modal
  // and mask classes because the consumer UI changes class names frequently.
  add('div.reds-modal.reds-modal-open.login-modal');
  add('[role="dialog"]');
  add('dialog');
  add('[class*="modal"]');
  add('[class*="Modal"]');
  add('[class*="dialog"]');
  add('[class*="Dialog"]');
  add('i.reds-mask');
  add('[class*="mask"]');
  add('[class*="Mask"]');
  add('[class*="overlay"]');
  add('[class*="Overlay"]');

  const seen = new Set();
  for (const item of candidates) {
    const node = item.node;
    if (seen.has(node) || !visible(node)) continue;
    seen.add(node);
    const className = String(node.getAttribute('class') || '').slice(0, 200);
    const id = String(node.id || '').slice(0, 120);
    const text = String(node.innerText || node.textContent || '').replace(/\s+/g, ' ').trim().slice(0, 240);
    const signature = className + ' ' + id + ' ' + text;
    if (!modalPattern.test(signature) || !authPattern.test(signature)) continue;
    const kind = /安全验证|身份验证|验证码|扫码|风控|verify|verification/i.test(signature)
      ? 'verification' : 'login';
    return JSON.stringify({
      present: true,
      kind,
      selector: item.selector,
      tag: String(node.tagName || '').toLowerCase(),
      id,
      class_name: className,
      text_preview: text
    });
  }

  // A few versions render a mask and authentication text without putting the
  // text inside the modal node. Only use strong auth phrases here so a normal
  // homepage login button is not mistaken for a blocking gate.
  const bodyText = document.body ? String(document.body.innerText || '').replace(/\s+/g, ' ') : '';
  const strongAuth = /手机号登录|扫码登录|安全验证|身份验证|风险验证|风控验证/.test(bodyText);
  if (strongAuth) {
    const mask = Array.from(document.querySelectorAll('i.reds-mask, [class*="mask"], [class*="Mask"], [class*="overlay"], [class*="Overlay"]')).find(visible);
    if (mask) {
      return JSON.stringify({
        present: true,
        kind: /安全验证|身份验证|风险验证|风控验证/.test(bodyText) ? 'verification' : 'login',
        selector: 'body-auth-text-with-mask',
        tag: String(mask.tagName || '').toLowerCase(),
        id: String(mask.id || '').slice(0, 120),
        class_name: String(mask.getAttribute('class') || '').slice(0, 200),
        text_preview: bodyText.slice(0, 240)
      });
    }
  }

  return JSON.stringify({present: false});
}`
