package xiaohongshu

import (
	"encoding/json"
	"fmt"

	"github.com/sirupsen/logrus"
)

type consumerAgreementDOMRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Top    float64 `json:"top"`
	Right  float64 `json:"right"`
	Bottom float64 `json:"bottom"`
	Left   float64 `json:"left"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type consumerAgreementDOMNode struct {
	TagName         string                   `json:"tag_name"`
	ID              string                   `json:"id"`
	ClassName       string                   `json:"class_name"`
	Role            string                   `json:"role"`
	Type            string                   `json:"type"`
	AriaChecked     string                   `json:"aria_checked"`
	AriaLabel       string                   `json:"aria_label"`
	Checked         *bool                    `json:"checked"`
	Rect            consumerAgreementDOMRect `json:"rect"`
	Cursor          string                   `json:"cursor"`
	PointerEvents   string                   `json:"pointer_events"`
	Visibility      string                   `json:"visibility"`
	Display         string                   `json:"display"`
	InteractiveHint bool                     `json:"interactive_hint"`
	HasOnClick      bool                     `json:"has_onclick"`
}

type consumerAgreementDOMHitTest struct {
	X        float64                    `json:"x"`
	Y        float64                    `json:"y"`
	Elements []consumerAgreementDOMNode `json:"elements"`
}

type consumerAgreementDOMCandidate struct {
	SemanticMatches []string                      `json:"semantic_matches"`
	TextLength      int                           `json:"text_length"`
	Region          consumerAgreementDOMNode      `json:"region"`
	Ancestors       []consumerAgreementDOMNode    `json:"ancestors"`
	Children        []consumerAgreementDOMNode    `json:"children"`
	Siblings        []consumerAgreementDOMNode    `json:"siblings"`
	HitTests        []consumerAgreementDOMHitTest `json:"hit_tests"`
}

type consumerAgreementDOMDiagnostics struct {
	CandidateCount int                             `json:"candidate_count"`
	Candidates     []consumerAgreementDOMCandidate `json:"candidates"`
}

func (a *ConsumerLoginAction) logConsumerAgreementDOMDiagnostic() (int, error) {
	result, err := a.page.Eval(consumerAgreementDOMDiagnosticScript)
	if err != nil {
		logrus.Warnf("consumer agreement DOM diagnostic evaluate failed: %v", err)
		return 0, err
	}

	var diagnostics consumerAgreementDOMDiagnostics
	if err := json.Unmarshal([]byte(result.Value.String()), &diagnostics); err != nil {
		logrus.Warnf("consumer agreement DOM diagnostic parse failed: %v", err)
		return 0, err
	}
	raw, err := json.Marshal(diagnostics)
	if err != nil {
		return len(diagnostics.Candidates), fmt.Errorf("marshal consumer agreement DOM diagnostic: %w", err)
	}
	logrus.Infof("consumer agreement DOM diagnostic: candidate_count=%d data=%s",
		diagnostics.CandidateCount, sanitizeConsumerDiagnosticJSON(raw))
	return len(diagnostics.Candidates), nil
}

func sanitizeConsumerDiagnosticJSON(data []byte) string {
	return consumerDiagnosticNumberPattern.ReplaceAllString(string(data), "<digits-redacted>")
}

const consumerAgreementDOMDiagnosticScript = `() => {
  const visible = (node) => {
    if (!node || node.nodeType !== 1) return false;
    const style = window.getComputedStyle(node);
    const rect = node.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || 1) > 0 && rect.width > 0 && rect.height > 0 &&
      node.getAttribute('aria-hidden') !== 'true';
  };
  const normalize = (value) => String(value || '').replace(/\s+/g, ' ').trim();
  const clip = (value, limit = 240) => normalize(value).slice(0, limit);
  const semanticWords = [
    '我已阅读并同意', '用户协议', '隐私政策', '隐私协议', '隐私条款', '服务条款', '相关协议'
  ];
  const textContent = (node) => normalize(node.innerText || node.textContent || '');
  const directText = (node) => Array.from(node.childNodes || [])
    .filter(child => child.nodeType === 3)
    .map(child => normalize(child.textContent))
    .filter(Boolean).join(' ');
  const semanticMatches = (value) => semanticWords.filter(word => value.includes(word));
  const snapshot = (node) => {
    if (!node || node.nodeType !== 1) return null;
    const style = window.getComputedStyle(node);
    const rect = node.getBoundingClientRect();
    const role = String(node.getAttribute('role') || '');
    const type = String(node.getAttribute('type') || '');
    const cursor = String(style.cursor || '');
    const pointerEvents = String(style.pointerEvents || '');
    const checked = typeof node.checked === 'boolean' ? node.checked : null;
    return {
      tag_name: String(node.tagName || '').toLowerCase(),
      id: clip(node.id, 160),
      class_name: clip(node.getAttribute('class') || '', 240),
      role: clip(role, 120),
      type: clip(type, 80),
      aria_checked: clip(node.getAttribute('aria-checked') || '', 80),
      aria_label: clip(node.getAttribute('aria-label') || '', 240),
      checked,
      rect: {
        x: rect.x, y: rect.y, top: rect.top, right: rect.right,
        bottom: rect.bottom, left: rect.left, width: rect.width, height: rect.height
      },
      cursor,
      pointer_events: pointerEvents,
      visibility: String(style.visibility || ''),
      display: String(style.display || ''),
      interactive_hint: pointerEvents !== 'none' &&
        (cursor === 'pointer' || /button|checkbox|link|switch/i.test(role) ||
        /^(button|a|label|input)$/i.test(String(node.tagName || '')) ||
        typeof node.onclick === 'function' || node.hasAttribute('onclick') ||
        node.hasAttribute('tabindex')),
      has_onclick: typeof node.onclick === 'function' || node.hasAttribute('onclick')
    };
  };
  const ancestors = (node) => {
    const result = [];
    for (let parent = node.parentElement, i = 0; parent && i < 5; parent = parent.parentElement, i++) {
      const value = snapshot(parent);
      if (value) result.push(value);
    }
    return result;
  };
  const children = (node) => Array.from(node.children || [])
    .slice(0, 20).map(snapshot).filter(Boolean);
  const siblings = (node) => [node.previousElementSibling, node.nextElementSibling]
    .map(snapshot).filter(Boolean);
  const hitTests = (node) => {
    const rect = node.getBoundingClientRect();
    const y = Math.max(0, Math.min(window.innerHeight - 1, rect.top + rect.height / 2));
    const points = [2, 8, 16, 28, 44]
      .map(offset => ({x: Math.max(0, rect.left - offset), y}));
    return points.map(point => ({
      x: point.x, y: point.y,
      elements: document.elementsFromPoint(point.x, point.y)
        .slice(0, 8).map(snapshot).filter(Boolean)
    }));
  };

  const rows = [];
  for (const node of document.querySelectorAll('*')) {
    if (!visible(node)) continue;
    const direct = directText(node);
    const full = textContent(node);
    const aria = normalize(node.getAttribute('aria-label') || '');
    const searchable = clip([direct, aria, full].filter(Boolean).join(' '));
    const matches = semanticMatches(searchable);
    if (!matches.length || searchable.length > 240) continue;
    const region = snapshot(node);
    if (!region) continue;
    rows.push({
      sort_key: searchable.length,
      candidate: {
        semantic_matches: matches,
        text_length: searchable.length,
        region,
        ancestors: ancestors(node),
        children: children(node),
        siblings: siblings(node),
        hit_tests: hitTests(node)
      }
    });
  }
  rows.sort((left, right) => left.sort_key - right.sort_key);
  const candidates = rows.slice(0, 24).map(row => row.candidate);
  return JSON.stringify({candidate_count: candidates.length, candidates});
}`
