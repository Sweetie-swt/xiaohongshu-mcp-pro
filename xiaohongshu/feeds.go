package xiaohongshu

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/lisiyuan/xiaohongshu-mcp-pro/errors"
	"github.com/sirupsen/logrus"
)

type FeedsListAction struct {
	page *rod.Page
}

func NewFeedsListAction(page *rod.Page) *FeedsListAction {
	pp := page.Timeout(60 * time.Second)

	pp.MustNavigate("https://www.xiaohongshu.com")
	pp.MustWaitDOMStable()

	return &FeedsListAction{page: pp}
}

const homepageSearchProbeTimeout = 2 * time.Second

type homepageSearchProbeRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type homepageSearchProbeNode struct {
	TagName string `json:"tag_name"`
	ID      string `json:"id"`
	Class   string `json:"class"`
}

type homepageSearchProbeForm struct {
	Exists       bool   `json:"exists"`
	Method       string `json:"method"`
	ActionOrigin string `json:"action_origin"`
	ActionPath   string `json:"action_path"`
}

type homepageSearchProbeControl struct {
	TagName         string                    `json:"tag_name"`
	Type            string                    `json:"type"`
	Placeholder     string                    `json:"placeholder"`
	AriaLabel       string                    `json:"aria_label"`
	Title           string                    `json:"title"`
	Name            string                    `json:"name"`
	Role            string                    `json:"role"`
	ID              string                    `json:"id"`
	Class           string                    `json:"class"`
	DataTestID      string                    `json:"data_testid"`
	ContentEditable bool                      `json:"contenteditable"`
	Visible         bool                      `json:"visible"`
	Rect            homepageSearchProbeRect   `json:"rect"`
	ShortText       string                    `json:"short_text"`
	Ancestors       []homepageSearchProbeNode `json:"ancestors"`
}

type homepageSearchProbeElement struct {
	homepageSearchProbeControl
	TopArea       bool                         `json:"top_area"`
	Form          homepageSearchProbeForm      `json:"form"`
	NearbyButtons []homepageSearchProbeControl `json:"nearby_buttons"`
}

type homepageSearchProbeSnapshot struct {
	URL                  string                          `json:"url"`
	Title                string                          `json:"title"`
	ReadyState           string                          `json:"ready_state"`
	ViewportWidth        int                             `json:"viewport_width"`
	ViewportHeight       int                             `json:"viewport_height"`
	InputCount           int                             `json:"input_count"`
	TextareaCount        int                             `json:"textarea_count"`
	ContenteditableCount int                             `json:"contenteditable_count"`
	ButtonCount          int                             `json:"button_count"`
	RoleButtonCount      int                             `json:"role_button_count"`
	InitialStateKeys     []string                        `json:"initial_state_keys"`
	InputLike            []homepageSearchProbeElement    `json:"input_like"`
	SearchInput          *homepageSearchProbeInput       `json:"search_input"`
	InputBoxNodes        []homepageSearchProbeNodeDetail `json:"input_box_nodes"`
}

type homepageSearchProbeNodeDetail struct {
	TagName       string                  `json:"tag_name"`
	ID            string                  `json:"id"`
	Class         string                  `json:"class"`
	Role          string                  `json:"role"`
	AriaLabel     string                  `json:"aria_label"`
	Title         string                  `json:"title"`
	DataTestID    string                  `json:"data_testid"`
	Type          string                  `json:"type"`
	TabIndex      string                  `json:"tabindex"`
	Visible       bool                    `json:"visible"`
	Rect          homepageSearchProbeRect `json:"rect"`
	Cursor        string                  `json:"cursor"`
	PointerEvents string                  `json:"pointer_events"`
	ShortText     string                  `json:"short_text"`
	Depth         int                     `json:"depth"`
	ParentIndex   int                     `json:"parent_index"`
}

type homepageSearchProbeInput struct {
	homepageSearchProbeNodeDetail
	Disabled       bool            `json:"disabled"`
	ReadOnly       bool            `json:"readonly"`
	Autocomplete   string          `json:"autocomplete"`
	MaxLength      string          `json:"maxlength"`
	Spellcheck     bool            `json:"spellcheck"`
	InputMode      string          `json:"inputmode"`
	EnterKeyHint   string          `json:"enterkeyhint"`
	InlineHandlers map[string]bool `json:"inline_handlers"`
}

type homepageSearchTriggerTarget struct {
	Name       string
	Selector   string
	Expression string
}

type homepageSearchTriggerListenerDetail struct {
	Target       string
	Type         string
	UseCapture   bool
	Passive      bool
	Once         bool
	ScriptID     string
	LineNumber   int
	ColumnNumber int
}

type homepageSearchTriggerListenerSummary struct {
	Target  string
	Counts  map[string]int
	Total   int
	Details []homepageSearchTriggerListenerDetail
}

var homepageSearchTriggerEventTypes = map[string]struct{}{
	"keydown":          {},
	"keyup":            {},
	"keypress":         {},
	"input":            {},
	"change":           {},
	"compositionstart": {},
	"compositionend":   {},
	"click":            {},
	"submit":           {},
	"focus":            {},
	"blur":             {},
}

var homepageSearchTriggerTargets = []homepageSearchTriggerTarget{
	{Name: "input", Selector: `input#search-input`},
	{Name: "input-box", Selector: `div.input-box`},
	{Name: "input-button", Selector: `div.input-button`},
	{Name: "search-icon", Selector: `div.search-icon`},
	{Name: "header", Selector: `header.mask-paper`},
	{Name: "app", Selector: `#app`},
	{Name: "document", Expression: `() => document`},
	{Name: "window", Expression: `() => window`},
}

func summarizeHomepageSearchTriggerListeners(target string, listeners []*proto.DOMDebuggerEventListener) homepageSearchTriggerListenerSummary {
	summary := homepageSearchTriggerListenerSummary{
		Target:  target,
		Counts:  make(map[string]int),
		Details: make([]homepageSearchTriggerListenerDetail, 0),
	}
	detailedTarget := target != "document" && target != "window"
	for _, listener := range listeners {
		if listener == nil {
			continue
		}
		if _, ok := homepageSearchTriggerEventTypes[listener.Type]; !ok {
			continue
		}
		summary.Total++
		summary.Counts[listener.Type]++
		if detailedTarget {
			summary.Details = append(summary.Details, homepageSearchTriggerListenerDetail{
				Target:       target,
				Type:         listener.Type,
				UseCapture:   listener.UseCapture,
				Passive:      listener.Passive,
				Once:         listener.Once,
				ScriptID:     string(listener.ScriptID),
				LineNumber:   listener.LineNumber,
				ColumnNumber: listener.ColumnNumber,
			})
		}
	}
	return summary
}

func formatHomepageSearchTriggerListenerCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "none"
	}
	eventTypes := []string{
		"keydown", "keyup", "keypress", "input", "change", "compositionstart", "compositionend",
		"click", "submit", "focus", "blur",
	}
	parts := make([]string, 0, len(counts))
	for _, eventType := range eventTypes {
		if count := counts[eventType]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", eventType, count))
		}
	}
	return strings.Join(parts, ",")
}

type homepageSearchProbeCandidateResult struct {
	Element  homepageSearchProbeElement
	Priority string
}

func homepageSearchProbeHasSearchHint(element homepageSearchProbeElement) bool {
	text := strings.ToLower(strings.Join([]string{
		element.Placeholder,
		element.AriaLabel,
		element.Title,
		element.Name,
		element.ID,
		element.Class,
		element.DataTestID,
		element.Role,
	}, "\x00"))
	return strings.Contains(text, "搜索") || strings.Contains(text, "search")
}

func homepageSearchProbeCandidate(element homepageSearchProbeElement) (priority string, ok bool) {
	if homepageSearchProbeHasSearchHint(element) {
		return "high", true
	}

	// A visible, input-sized control in the upper viewport is useful evidence,
	// but without a search-related attribute it is only a secondary candidate.
	if element.Visible && element.TopArea &&
		element.Rect.Width >= 120 && element.Rect.Height >= 18 && element.Rect.Height <= 96 {
		return "secondary", true
	}
	return "", false
}

func homepageSearchProbeCandidates(snapshot homepageSearchProbeSnapshot) []homepageSearchProbeCandidateResult {
	candidates := make([]homepageSearchProbeCandidateResult, 0, len(snapshot.InputLike))
	for _, element := range snapshot.InputLike {
		priority, ok := homepageSearchProbeCandidate(element)
		if ok {
			candidates = append(candidates, homepageSearchProbeCandidateResult{Element: element, Priority: priority})
		}
	}
	return candidates
}

func formatHomepageSearchProbeNode(node homepageSearchProbeNode) string {
	return fmt.Sprintf("%s#%s.%s", node.TagName, node.ID, node.Class)
}

func formatHomepageSearchProbeControl(control homepageSearchProbeControl) string {
	return fmt.Sprintf("tag=%s type=%s id=%q class=%q role=%q aria_label=%q title=%q data_testid=%q visible=%t rect=(%.0f,%.0f %.0fx%.0f) short_text=%q",
		control.TagName, control.Type, control.ID, control.Class, control.Role, control.AriaLabel,
		control.Title, control.DataTestID, control.Visible, control.Rect.X, control.Rect.Y,
		control.Rect.Width, control.Rect.Height, control.ShortText)
}

func logHomepageSearchProbe(snapshot homepageSearchProbeSnapshot) {
	logrus.Infof("homepage-search-probe: url=%q", snapshot.URL)
	logrus.Infof("homepage-search-probe: title=%q", snapshot.Title)
	logrus.Infof("homepage-search-probe: ready_state=%s", snapshot.ReadyState)
	logrus.Infof("homepage-search-probe: viewport=%dx%d", snapshot.ViewportWidth, snapshot.ViewportHeight)
	logrus.Infof("homepage-search-probe: counts input=%d textarea=%d contenteditable=%d button=%d role_button=%d",
		snapshot.InputCount, snapshot.TextareaCount, snapshot.ContenteditableCount,
		snapshot.ButtonCount, snapshot.RoleButtonCount)
	if len(snapshot.InitialStateKeys) > 0 {
		logrus.Infof("homepage-search-probe: initial_state_keys=%v", snapshot.InitialStateKeys)
	}

	candidates := homepageSearchProbeCandidates(snapshot)
	logrus.Infof("homepage-search-probe: candidate_count=%d", len(candidates))
	for candidateIndex, candidate := range candidates {
		element := candidate.Element
		ancestors := make([]string, 0, len(element.Ancestors))
		for _, ancestor := range element.Ancestors {
			ancestors = append(ancestors, formatHomepageSearchProbeNode(ancestor))
		}
		nearbyButtons := make([]string, 0, len(element.NearbyButtons))
		for _, button := range element.NearbyButtons {
			nearbyButtons = append(nearbyButtons, formatHomepageSearchProbeControl(button))
		}
		logrus.Infof("homepage-search-probe: candidate=%d priority=%s %s placeholder=%q name=%q contenteditable=%t top_area=%t form_exists=%t form_method=%s form_action_origin=%q form_action_path=%q ancestors=%v nearby_buttons=%v",
			candidateIndex+1, candidate.Priority, formatHomepageSearchProbeControl(element.homepageSearchProbeControl),
			element.Placeholder, element.Name, element.ContentEditable, element.TopArea, element.Form.Exists,
			element.Form.Method, element.Form.ActionOrigin, element.Form.ActionPath, ancestors, nearbyButtons)
	}
	logrus.Infof("homepage-search-probe: complete")
}

func formatHomepageSearchProbeNodeDetail(node homepageSearchProbeNodeDetail) string {
	return fmt.Sprintf("tag=%s id=%q class=%q role=%q aria_label=%q title=%q data_testid=%q type=%q tabindex=%q visible=%t rect=(%.0f,%.0f %.0fx%.0f) cursor=%q pointer_events=%q short_text=%q",
		node.TagName, node.ID, node.Class, node.Role, node.AriaLabel, node.Title, node.DataTestID,
		node.Type, node.TabIndex, node.Visible, node.Rect.X, node.Rect.Y, node.Rect.Width,
		node.Rect.Height, node.Cursor, node.PointerEvents, node.ShortText)
}

func formatHomepageSearchProbeInlineHandlers(handlers map[string]bool) string {
	if len(handlers) == 0 {
		return "none"
	}
	eventTypes := []string{
		"keydown", "keyup", "keypress", "input", "change", "compositionstart", "compositionend",
		"click", "submit", "focus", "blur",
	}
	parts := make([]string, 0, len(eventTypes))
	for _, eventType := range eventTypes {
		if handlers["on"+eventType] {
			parts = append(parts, "on"+eventType+"=true")
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ",")
}

func logHomepageSearchTriggerStructure(snapshot homepageSearchProbeSnapshot) {
	if snapshot.SearchInput == nil {
		logrus.Warn("homepage-search-trigger-probe: input input#search-input not found")
	} else {
		input := snapshot.SearchInput
		logrus.Infof("homepage-search-trigger-probe: input %s disabled=%t readonly=%t autocomplete=%q maxlength=%q spellcheck=%t inputmode=%q enterkeyhint=%q inline_handlers=%s",
			formatHomepageSearchProbeNodeDetail(input.homepageSearchProbeNodeDetail), input.Disabled, input.ReadOnly,
			input.Autocomplete, input.MaxLength, input.Spellcheck, input.InputMode, input.EnterKeyHint,
			formatHomepageSearchProbeInlineHandlers(input.InlineHandlers))
	}

	for index, node := range snapshot.InputBoxNodes {
		logrus.Infof("homepage-search-trigger-probe: input_box_child index=%d depth=%d parent_index=%d %s",
			index, node.Depth, node.ParentIndex, formatHomepageSearchProbeNodeDetail(node))
	}
	logrus.Infof("homepage-search-trigger-probe: summary search_input_found=%t input_box_node_count=%d",
		snapshot.SearchInput != nil, len(snapshot.InputBoxNodes))
}

func homepageSearchTriggerRemoteObject(page *rod.Page, target homepageSearchTriggerTarget) (object *proto.RuntimeRemoteObject, found bool, release func() error, err error) {
	release = func() error { return nil }
	if target.Selector != "" {
		element, elementErr := page.Element(target.Selector)
		if elementErr != nil {
			return nil, false, release, elementErr
		}
		if element == nil || element.Object == nil || element.Object.ObjectID == "" {
			return nil, false, release, nil
		}
		return element.Object, true, func() error {
			return element.Release()
		}, nil
	}

	if target.Expression == "" {
		return nil, false, release, fmt.Errorf("target %s has no selector or expression", target.Name)
	}
	object, err = page.Evaluate(rod.Eval(target.Expression).ByObject())
	if err != nil {
		return nil, false, release, err
	}
	if object == nil || object.ObjectID == "" {
		return nil, false, release, nil
	}
	return object, true, func() error {
		return page.Release(object)
	}, nil
}

func releaseHomepageSearchTriggerListenerObjects(page *rod.Page, listeners []*proto.DOMDebuggerEventListener) {
	if page == nil {
		return
	}
	for _, listener := range listeners {
		if listener == nil {
			continue
		}
		for _, object := range []*proto.RuntimeRemoteObject{listener.Handler, listener.OriginalHandler} {
			if object == nil || object.ObjectID == "" {
				continue
			}
			if err := page.Release(object); err != nil {
				logrus.Warnf("homepage-search-trigger-probe: listener remote object release failed: %v", err)
			}
		}
	}
}

func readHomepageSearchTriggerListeners(page *rod.Page, target homepageSearchTriggerTarget) (summary homepageSearchTriggerListenerSummary, found bool, err error) {
	if page == nil {
		return summary, false, fmt.Errorf("page is nil")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("listener probe panic: %v", recovered)
		}
	}()

	object, found, release, err := homepageSearchTriggerRemoteObject(page, target)
	if err != nil {
		return summary, found, err
	}
	if !found {
		return summary, false, nil
	}
	defer func() {
		if releaseErr := release(); releaseErr != nil {
			logrus.Warnf("homepage-search-trigger-probe: target=%s remote object release failed: %v", target.Name, releaseErr)
		}
	}()

	result, err := (proto.DOMDebuggerGetEventListeners{ObjectID: object.ObjectID}).Call(page)
	if err != nil {
		return summary, true, err
	}
	if result == nil {
		return summary, true, fmt.Errorf("listener result is empty")
	}
	defer releaseHomepageSearchTriggerListenerObjects(page, result.Listeners)
	return summarizeHomepageSearchTriggerListeners(target.Name, result.Listeners), true, nil
}

func logHomepageSearchTriggerListeners(page *rod.Page) {
	for _, target := range homepageSearchTriggerTargets {
		summary, found, err := readHomepageSearchTriggerListeners(page, target)
		if err != nil {
			logrus.Warnf("homepage-search-trigger-probe: listeners target=%s failed: %v", target.Name, err)
			continue
		}
		if !found {
			logrus.Infof("homepage-search-trigger-probe: listeners target=%s found=false", target.Name)
			continue
		}
		logrus.Infof("homepage-search-trigger-probe: listeners target=%s total=%d counts=%s",
			target.Name, summary.Total, formatHomepageSearchTriggerListenerCounts(summary.Counts))
		for _, detail := range summary.Details {
			logrus.Infof("homepage-search-trigger-probe: listener target=%s type=%s use_capture=%t passive=%t once=%t script_id=%q line=%d column=%d",
				detail.Target, detail.Type, detail.UseCapture, detail.Passive, detail.Once,
				detail.ScriptID, detail.LineNumber, detail.ColumnNumber)
		}
	}
	logrus.Infof("homepage-search-trigger-probe: complete")
}

func probeHomepageSearchControls(page *rod.Page) (err error) {
	if page == nil {
		return fmt.Errorf("page is nil")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("probe panic: %v", recovered)
		}
	}()

	baseCtx := page.GetContext()
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(baseCtx, homepageSearchProbeTimeout)
	defer cancel()
	result, err := page.Context(probeCtx).Evaluate(rod.Eval(homepageSearchProbeScript))
	if err != nil {
		return err
	}
	var snapshot homepageSearchProbeSnapshot
	if err := json.Unmarshal([]byte(result.Value.Str()), &snapshot); err != nil {
		return fmt.Errorf("parse probe result: %w", err)
	}
	logHomepageSearchProbe(snapshot)
	logHomepageSearchTriggerStructure(snapshot)
	logHomepageSearchTriggerListeners(page.Context(probeCtx))
	return nil
}

func probeHomepageSearchControlsBestEffort(page *rod.Page) {
	if err := probeHomepageSearchControls(page); err != nil {
		logrus.Warnf("homepage-search-probe: failed: %v", err)
		logrus.Warnf("homepage-search-trigger-probe: failed: %v", err)
	}
}

const homepageSearchProbeScript = `() => {
  const maxClassLength = 160;
  const attr = (element, name) => {
    const value = element.getAttribute(name);
    return value === null ? '' : String(value).slice(0, maxClassLength);
  };
  const safePageURL = () => {
    const raw = String(window.location.href || '');
    if (raw === 'about:blank') return raw;
    try {
      const parsed = new URL(raw);
      if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return '';
      return parsed.origin + parsed.pathname;
    } catch (_) {
      return '';
    }
  };
  const safeAction = (form) => {
    const raw = attr(form, 'action');
    try {
      const parsed = new URL(raw || window.location.href, window.location.href);
      if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
        return {origin: '', path: ''};
      }
      return {origin: parsed.origin, path: parsed.pathname};
    } catch (_) {
      return {origin: '', path: ''};
    }
  };
  const rectOf = (element) => {
    const rect = element.getBoundingClientRect();
    return {
      x: Number(rect.x) || 0,
      y: Number(rect.y) || 0,
      width: Number(rect.width) || 0,
      height: Number(rect.height) || 0
    };
  };
  const visible = (element, rect) => {
    const style = window.getComputedStyle(element);
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || 1) > 0 && rect.width > 0 && rect.height > 0;
  };
  const shortSearchText = (element) => {
    const text = String(element.textContent || '').replace(/\s+/g, ' ').trim();
    return text.length <= 20 && /^(搜索|search|查找|搜索一下)$/i.test(text) ? text : '';
  };
  const isButtonLike = (element) =>
    element.tagName.toLowerCase() === 'button' || element.getAttribute('role') === 'button';
  const ancestorsOf = (element) => {
    const result = [];
    let current = element.parentElement;
    for (let i = 0; current && i < 3; i += 1, current = current.parentElement) {
      result.push({
        tag_name: current.tagName.toLowerCase(),
        id: attr(current, 'id'),
        class: attr(current, 'class')
      });
    }
    return result;
  };
  const controlInfo = (element) => {
    const rect = rectOf(element);
    return {
      tag_name: element.tagName.toLowerCase(),
      type: attr(element, 'type'),
      placeholder: attr(element, 'placeholder'),
      aria_label: attr(element, 'aria-label'),
      title: attr(element, 'title'),
      name: attr(element, 'name'),
      role: attr(element, 'role'),
      id: attr(element, 'id'),
      class: attr(element, 'class'),
      data_testid: attr(element, 'data-testid'),
      contenteditable: attr(element, 'contenteditable').toLowerCase() === 'true',
      visible: visible(element, rect),
      rect,
      short_text: isButtonLike(element) ? shortSearchText(element) : '',
      ancestors: ancestorsOf(element)
    };
  };
  const nodeDetail = (element, depth, parentIndex) => {
    const rect = rectOf(element);
    const style = window.getComputedStyle(element);
    return {
      tag_name: element.tagName.toLowerCase(),
      id: attr(element, 'id'),
      class: attr(element, 'class'),
      role: attr(element, 'role'),
      aria_label: attr(element, 'aria-label'),
      title: attr(element, 'title'),
      data_testid: attr(element, 'data-testid'),
      type: attr(element, 'type'),
      tabindex: attr(element, 'tabindex'),
      visible: visible(element, rect),
      rect,
      cursor: String(style.cursor || ''),
      pointer_events: String(style.pointerEvents || ''),
      short_text: shortSearchText(element),
      depth,
      parent_index: parentIndex
    };
  };
  const inlineEventNames = [
    'keydown', 'keyup', 'keypress', 'input', 'change', 'compositionstart', 'compositionend',
    'click', 'submit', 'focus', 'blur'
  ];
  const inlineHandlerFlags = (element) => {
    const flags = {};
    inlineEventNames.forEach((eventName) => {
      flags['on' + eventName] = element.hasAttribute('on' + eventName);
    });
    return flags;
  };
  const inputBoxNodes = () => {
    const box = document.querySelector('div.input-box');
    if (!box) return [];
    const result = [];
    Array.from(box.children).forEach((child, parentIndex) => {
      result.push(nodeDetail(child, 1, parentIndex));
      Array.from(child.children).forEach((grandchild) => {
        result.push(nodeDetail(grandchild, 2, parentIndex));
      });
    });
    return result;
  };
  const searchInputInfo = () => {
    const input = document.querySelector('input#search-input');
    if (!input) return null;
    return {
      ...nodeDetail(input, 0, -1),
      disabled: !!input.disabled,
      readonly: !!input.readOnly,
      autocomplete: attr(input, 'autocomplete'),
      maxlength: attr(input, 'maxlength'),
      spellcheck: !!input.spellcheck,
      inputmode: attr(input, 'inputmode'),
      enterkeyhint: attr(input, 'enterkeyhint'),
      inline_handlers: inlineHandlerFlags(input)
    };
  };
  const relatedButtons = (element) => {
    const result = [];
    const seen = new Set();
    const add = (button) => {
      if (!button || seen.has(button) || !isButtonLike(button)) return;
      const rect = rectOf(button);
      if (!visible(button, rect)) return;
      seen.add(button);
      result.push(controlInfo(button));
    };
    const parent = element.parentElement;
    if (parent) Array.from(parent.children).forEach(add);
    const grandparent = parent && parent.parentElement;
    if (grandparent) Array.from(grandparent.children).forEach(add);
    const form = element.closest('form');
    if (form) {
      const inputRect = rectOf(element);
      Array.from(form.querySelectorAll('button, [role="button"]')).forEach((button) => {
        const rect = rectOf(button);
        const near = Math.abs((rect.x + rect.width / 2) - (inputRect.x + inputRect.width / 2)) <= 360 &&
          Math.abs((rect.y + rect.height / 2) - (inputRect.y + inputRect.height / 2)) <= 140;
        if (near) add(button);
      });
    }
    return result.slice(0, 5);
  };
  const formInfo = (element) => {
    const form = element.closest('form');
    if (!form) return {exists: false, method: '', action_origin: '', action_path: ''};
    const action = safeAction(form);
    return {
      exists: true,
      method: (attr(form, 'method') || 'get').toLowerCase(),
      action_origin: action.origin,
      action_path: action.path
    };
  };
  const inputLikeElements = Array.from(document.querySelectorAll('input, textarea, [contenteditable="true"]'));
  const buttons = Array.from(document.querySelectorAll('button'));
  const roleButtons = Array.from(document.querySelectorAll('[role="button"]'));
  let initialStateKeys = [];
  try {
    if (window.__INITIAL_STATE__ && typeof window.__INITIAL_STATE__ === 'object') {
      initialStateKeys = Object.keys(window.__INITIAL_STATE__).slice(0, 50);
    }
  } catch (_) {}
  return JSON.stringify({
    url: safePageURL(),
    title: String(document.title || '').slice(0, 200),
    ready_state: String(document.readyState || ''),
    viewport_width: Number(window.innerWidth) || 0,
    viewport_height: Number(window.innerHeight) || 0,
    input_count: document.querySelectorAll('input').length,
    textarea_count: document.querySelectorAll('textarea').length,
    contenteditable_count: document.querySelectorAll('[contenteditable="true"]').length,
    button_count: buttons.length,
    role_button_count: roleButtons.length,
    initial_state_keys: initialStateKeys,
    input_like: inputLikeElements.map((element) => {
      const info = controlInfo(element);
      const rect = info.rect;
      return {
        ...info,
        top_area: rect.y >= 0 && rect.y <= (Number(window.innerHeight) || 0) * 0.30,
        form: formInfo(element),
        nearby_buttons: relatedButtons(element)
      };
    }),
    search_input: searchInputInfo(),
    input_box_nodes: inputBoxNodes()
  });
}`

// GetFeedsList 获取页面的 Feed 列表数据
func (f *FeedsListAction) GetFeedsList(ctx context.Context) ([]Feed, error) {
	page := f.page.Context(ctx)

	time.Sleep(1 * time.Second)

	result := page.MustEval(`() => {
		if (window.__INITIAL_STATE__ &&
		    window.__INITIAL_STATE__.feed &&
		    window.__INITIAL_STATE__.feed.feeds) {
			const feeds = window.__INITIAL_STATE__.feed.feeds;
			const feedsData = feeds.value !== undefined ? feeds.value : feeds._value;
			if (feedsData) {
				return JSON.stringify(feedsData);
			}
		}
		return "";
	}`).String()

	if result == "" {
		return nil, errors.ErrNoFeeds
	}

	var feeds []Feed
	if err := json.Unmarshal([]byte(result), &feeds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal feeds: %w", err)
	}

	return feeds, nil
}
