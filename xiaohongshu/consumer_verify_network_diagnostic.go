package xiaohongshu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/sirupsen/logrus"
)

const (
	consumerVerifyOutcomeRequestNotObserved     = "login_request_not_observed"
	consumerVerifyOutcomeTransportFailed        = "login_request_failed_transport"
	consumerVerifyOutcomeRejected               = "login_request_rejected"
	consumerVerifyOutcomeSecurityVerification   = "security_verification_required"
	consumerVerifyOutcomeSuccessGateRemains     = "login_response_success_but_gate_remains"
	consumerVerifyOutcomeDiagnosticInconclusive = "diagnostic_inconclusive"
	consumerVerifyNetworkLogLimit               = 3000
	consumerVerifyDOMDiffLogLimit               = 1800
)

type consumerVerifyNetworkEvent struct {
	Method           string            `json:"method"`
	URL              string            `json:"url"`
	ResourceType     string            `json:"resource_type"`
	RequestSent      bool              `json:"request_sent"`
	ResponseReceived bool              `json:"response_received"`
	Status           int               `json:"status"`
	LoadingFailed    bool              `json:"loading_failed"`
	FailureText      string            `json:"failure_text,omitempty"`
	ResponseFields   map[string]string `json:"response_fields,omitempty"`

	requestID proto.NetworkRequestID
	sequence  int
	bodyRead  bool
}

type consumerVerifyNetworkTrace struct {
	page *rod.Page

	mu      sync.Mutex
	events  map[proto.NetworkRequestID]*consumerVerifyNetworkEvent
	ordered []proto.NetworkRequestID
	armed   bool

	cancel   context.CancelFunc
	done     chan struct{}
	stopOnce sync.Once
}

func newConsumerVerifyNetworkTrace(page *rod.Page) *consumerVerifyNetworkTrace {
	return &consumerVerifyNetworkTrace{
		page:   page,
		events: make(map[proto.NetworkRequestID]*consumerVerifyNetworkEvent),
		done:   make(chan struct{}),
	}
}

func (t *consumerVerifyNetworkTrace) Start(parent context.Context) error {
	if t == nil || t.page == nil {
		return fmt.Errorf("page is nil")
	}
	if parent == nil {
		parent = context.Background()
	}

	traceCtx, cancel := context.WithCancel(parent)
	t.cancel = cancel
	tracePage := t.page.Context(traceCtx)
	enableErr := proto.NetworkEnable{}.Call(tracePage)
	wait := tracePage.EachEvent(
		func(event *proto.NetworkRequestWillBeSent) { t.handleRequestWillBeSent(event) },
		func(event *proto.NetworkResponseReceived) { t.handleResponseReceived(event) },
		func(event *proto.NetworkLoadingFailed) { t.handleLoadingFailed(event) },
	)
	go func() {
		defer close(t.done)
		defer func() {
			if recovered := recover(); recovered != nil {
				logrus.Warnf("consumer VerifyOTP 网络诊断监听器异常结束: %v", recovered)
			}
		}()
		wait()
	}()
	return enableErr
}

func (t *consumerVerifyNetworkTrace) Stop() {
	if t == nil {
		return
	}
	t.stopOnce.Do(func() {
		if t.cancel != nil {
			t.cancel()
		}
		if t.done != nil {
			<-t.done
		}
	})
}

func (t *consumerVerifyNetworkTrace) Arm() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.armed = true
	t.mu.Unlock()
}

func consumerVerifyResourceTypeRelevant(resourceType proto.NetworkResourceType) bool {
	switch resourceType {
	case proto.NetworkResourceTypeXHR, proto.NetworkResourceTypeFetch, proto.NetworkResourceTypeDocument:
		return true
	default:
		return false
	}
}

func consumerVerifySafeXHSURL(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "xiaohongshu.com" && !strings.HasSuffix(host, ".xiaohongshu.com") {
		return "", false
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", false
	}
	return parsed.Scheme + "://" + parsed.Host + path, true
}

func (t *consumerVerifyNetworkTrace) handleRequestWillBeSent(event *proto.NetworkRequestWillBeSent) {
	if t == nil || event == nil || event.Request == nil || !consumerVerifyResourceTypeRelevant(event.Type) {
		return
	}
	safeURL, ok := consumerVerifySafeXHSURL(event.Request.URL)
	if !ok {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.armed {
		return
	}
	if _, exists := t.events[event.RequestID]; exists {
		return
	}
	t.events[event.RequestID] = &consumerVerifyNetworkEvent{
		Method:       event.Request.Method,
		URL:          safeURL,
		ResourceType: string(event.Type),
		RequestSent:  true,
		requestID:    event.RequestID,
		sequence:     len(t.ordered),
	}
	t.ordered = append(t.ordered, event.RequestID)
}

func (t *consumerVerifyNetworkTrace) handleResponseReceived(event *proto.NetworkResponseReceived) {
	if t == nil || event == nil || event.Response == nil || !consumerVerifyResourceTypeRelevant(event.Type) {
		return
	}
	safeURL, ok := consumerVerifySafeXHSURL(event.Response.URL)
	if !ok {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.armed {
		return
	}
	entry, exists := t.events[event.RequestID]
	if !exists {
		entry = &consumerVerifyNetworkEvent{
			Method:       "",
			URL:          safeURL,
			ResourceType: string(event.Type),
			requestID:    event.RequestID,
			sequence:     len(t.ordered),
		}
		t.events[event.RequestID] = entry
		t.ordered = append(t.ordered, event.RequestID)
	}
	entry.URL = safeURL
	entry.ResourceType = string(event.Type)
	entry.ResponseReceived = true
	entry.Status = int(event.Response.Status)
}

func (t *consumerVerifyNetworkTrace) handleLoadingFailed(event *proto.NetworkLoadingFailed) {
	if t == nil || event == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, exists := t.events[event.RequestID]
	if !exists {
		return
	}
	entry.LoadingFailed = true
	entry.FailureText = sanitizeConsumerDiagnosticText(event.ErrorText)
	if len(entry.FailureText) > 160 {
		entry.FailureText = entry.FailureText[:160]
	}
}

func (t *consumerVerifyNetworkTrace) responseBodyCandidates() []consumerVerifyNetworkEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	candidates := make([]consumerVerifyNetworkEvent, 0)
	for _, requestID := range t.ordered {
		entry := t.events[requestID]
		if entry == nil || entry.bodyRead || !entry.ResponseReceived || !consumerVerifyLoginResponseCandidate(*entry) {
			continue
		}
		candidates = append(candidates, *entry)
	}
	return candidates
}

func (t *consumerVerifyNetworkTrace) readWhitelistedResponseBodies() {
	if t == nil || t.page == nil {
		return
	}
	for _, candidate := range t.responseBodyCandidates() {
		result, err := proto.NetworkGetResponseBody{RequestID: candidate.requestID}.Call(t.page)
		if err != nil || result == nil {
			continue
		}
		t.markResponseBodyRead(candidate.requestID)
		if result.Base64Encoded || len(result.Body) > 1024*1024 {
			continue
		}
		fields, ok := parseConsumerVerifyResponseFields(result.Body)
		if !ok || len(fields) == 0 {
			continue
		}
		t.mu.Lock()
		if entry := t.events[candidate.requestID]; entry != nil {
			entry.ResponseFields = fields
		}
		t.mu.Unlock()
	}
}

func (t *consumerVerifyNetworkTrace) markResponseBodyRead(requestID proto.NetworkRequestID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if entry := t.events[requestID]; entry != nil {
		entry.bodyRead = true
	}
}

func (t *consumerVerifyNetworkTrace) Snapshot() []consumerVerifyNetworkEvent {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]consumerVerifyNetworkEvent, 0, len(t.ordered))
	for _, requestID := range t.ordered {
		entry := t.events[requestID]
		if entry == nil {
			continue
		}
		copyEntry := *entry
		if entry.ResponseFields != nil {
			copyEntry.ResponseFields = make(map[string]string, len(entry.ResponseFields))
			for key, value := range entry.ResponseFields {
				copyEntry.ResponseFields[key] = value
			}
		}
		result = append(result, copyEntry)
	}
	return result
}

func (t *consumerVerifyNetworkTrace) LogSnapshot(stage string, authGatePresent bool, authGateKind string) string {
	if t == nil {
		return consumerVerifyOutcomeRequestNotObserved
	}
	t.readWhitelistedResponseBodies()
	events := t.Snapshot()
	outcome := consumerVerifyNetworkOutcome(events, authGatePresent, authGateKind)
	encoded, err := json.Marshal(events)
	if err != nil {
		encoded = []byte("[]")
	}
	logrus.Infof("consumer VerifyOTP 网络诊断[%s]: outcome=%s events=%s",
		stage, outcome, sanitizeConsumerVerifyNetworkLog(string(encoded)))
	return outcome
}

func sanitizeConsumerVerifyNetworkLog(value string) string {
	value = consumerDiagnosticNumberPattern.ReplaceAllString(value, "<digits-redacted>")
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > consumerVerifyNetworkLogLimit {
		return value[:consumerVerifyNetworkLogLimit]
	}
	return value
}

func consumerVerifyLoginResponseCandidate(event consumerVerifyNetworkEvent) bool {
	if event.ResourceType != string(proto.NetworkResourceTypeXHR) && event.ResourceType != string(proto.NetworkResourceTypeFetch) {
		return false
	}
	if _, ok := consumerVerifySafeXHSURL(event.URL); !ok {
		return false
	}
	parsed, err := url.Parse(event.URL)
	if err != nil {
		return false
	}
	path := strings.ToLower(parsed.EscapedPath())
	if strings.Contains(path, "login") || strings.Contains(path, "passport") ||
		strings.Contains(path, "auth") || strings.Contains(path, "session") {
		return true
	}
	return strings.Contains(path, "verify") &&
		(strings.Contains(path, "code") || strings.Contains(path, "sms") || strings.Contains(path, "otp") || strings.Contains(path, "login"))
}

func parseConsumerVerifyResponseFields(body string) (map[string]string, bool) {
	if strings.TrimSpace(body) == "" {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewBufferString(body))
	decoder.UseNumber()
	var payload map[string]json.RawMessage
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		return nil, false
	}
	allowed := map[string]struct{}{
		"code": {}, "error_code": {}, "status": {}, "success": {},
		"msg": {}, "message": {}, "error": {},
	}
	fields := make(map[string]string)
	for key, raw := range payload {
		if _, ok := allowed[key]; !ok {
			continue
		}
		var value interface{}
		valueDecoder := json.NewDecoder(bytes.NewReader(raw))
		valueDecoder.UseNumber()
		if err := valueDecoder.Decode(&value); err != nil {
			continue
		}
		var scalar string
		switch typed := value.(type) {
		case string:
			var safe bool
			scalar, safe = sanitizeConsumerVerifyResponseString(typed)
			if !safe {
				continue
			}
		case bool:
			scalar = fmt.Sprintf("%t", typed)
		case json.Number:
			scalar = sanitizeConsumerDiagnosticText(typed.String())
		default:
			continue
		}
		fields[key] = scalar
	}
	return fields, true
}

func sanitizeConsumerVerifyResponseString(value string) (string, bool) {
	lower := strings.ToLower(value)
	for _, forbidden := range []string{"token", "cookie", "authorization", "session"} {
		if strings.Contains(lower, forbidden) {
			return "", false
		}
	}
	value = sanitizeConsumerDiagnosticText(value)
	if len(value) > 160 {
		value = value[:160]
	}
	return value, true
}

func consumerVerifyResponseHasExplicitRejection(event consumerVerifyNetworkEvent) bool {
	if event.Status < 200 || event.Status >= 300 || len(event.ResponseFields) == 0 {
		return false
	}
	if strings.EqualFold(event.ResponseFields["success"], "false") {
		return true
	}
	for _, key := range []string{"error_code", "error"} {
		if value := strings.TrimSpace(event.ResponseFields[key]); value != "" && value != "0" && !strings.EqualFold(value, "false") {
			return true
		}
	}
	for _, key := range []string{"code", "status", "msg", "message"} {
		value := strings.ToLower(strings.TrimSpace(event.ResponseFields[key]))
		if value == "" || value == "0" || value == "ok" || value == "success" || value == "true" {
			continue
		}
		if strings.Contains(value, "验证码错误") || strings.Contains(value, "验证码失效") ||
			strings.Contains(value, "登录失败") || strings.Contains(value, "操作频繁") ||
			strings.Contains(value, "请求频繁") || strings.Contains(value, "网络异常") ||
			strings.Contains(value, "请稍后再试") || strings.Contains(value, "需要验证") {
			return true
		}
	}
	if code := strings.TrimSpace(event.ResponseFields["code"]); code != "" && code != "0" {
		return true
	}
	return false
}

func consumerVerifyResponseSignalsSuccess(event consumerVerifyNetworkEvent) bool {
	if event.Status < 200 || event.Status >= 300 || len(event.ResponseFields) == 0 {
		return false
	}
	if strings.EqualFold(event.ResponseFields["success"], "true") {
		return true
	}
	if status := strings.ToLower(strings.TrimSpace(event.ResponseFields["status"])); status == "ok" || status == "success" {
		return true
	}
	return strings.TrimSpace(event.ResponseFields["code"]) == "0"
}

func consumerVerifyNetworkOutcome(events []consumerVerifyNetworkEvent, authGatePresent bool, authGateKind string) string {
	if authGatePresent && authGateKind == "verification" {
		return consumerVerifyOutcomeSecurityVerification
	}
	loginEvents := make([]consumerVerifyNetworkEvent, 0, len(events))
	for _, event := range events {
		if consumerVerifyLoginRequestObserved(event) {
			loginEvents = append(loginEvents, event)
		}
	}
	if len(loginEvents) == 0 {
		return consumerVerifyOutcomeRequestNotObserved
	}
	for _, event := range loginEvents {
		if event.LoadingFailed || (event.ResponseReceived && (event.Status < 200 || event.Status >= 300)) {
			return consumerVerifyOutcomeTransportFailed
		}
	}
	for _, event := range loginEvents {
		if consumerVerifyResponseHasExplicitRejection(event) {
			return consumerVerifyOutcomeRejected
		}
	}
	for _, event := range loginEvents {
		if consumerVerifyResponseSignalsSuccess(event) {
			if authGatePresent && authGateKind == "login" {
				return consumerVerifyOutcomeSuccessGateRemains
			}
			return consumerVerifyOutcomeDiagnosticInconclusive
		}
	}
	return consumerVerifyOutcomeDiagnosticInconclusive
}

func consumerVerifyLoginRequestObserved(event consumerVerifyNetworkEvent) bool {
	if consumerVerifyLoginResponseCandidate(event) {
		return true
	}
	if event.ResourceType != string(proto.NetworkResourceTypeDocument) {
		return false
	}
	parsed, err := url.Parse(event.URL)
	if err != nil {
		return false
	}
	path := strings.ToLower(parsed.EscapedPath())
	return strings.Contains(path, "login") || strings.Contains(path, "passport") || strings.Contains(path, "auth")
}

type consumerVerifyDOMTextNode struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

type consumerVerifyDOMTextChange struct {
	Kind   string `json:"kind"`
	Key    string `json:"key"`
	Before string `json:"before,omitempty"`
	After  string `json:"after"`
}

type consumerVerifySecurityNode struct {
	Tag        string `json:"tag"`
	ID         string `json:"id"`
	Class      string `json:"class"`
	URL        string `json:"url"`
	Visibility string `json:"visibility"`
}

func (a *ConsumerLoginAction) readConsumerVerifyDOMTextSnapshot() []consumerVerifyDOMTextNode {
	if a == nil || a.page == nil {
		return nil
	}
	result, err := a.page.Eval(consumerVerifyModalTextSnapshotScript)
	if err != nil {
		logrus.Warnf("consumer VerifyOTP 文本快照读取失败: %v", err)
		return nil
	}
	var snapshot []consumerVerifyDOMTextNode
	if err := json.Unmarshal([]byte(result.Value.String()), &snapshot); err != nil {
		logrus.Warnf("consumer VerifyOTP 文本快照解析失败: %v", err)
	}
	return snapshot
}

func consumerVerifyDOMTextDiff(before, after []consumerVerifyDOMTextNode) []consumerVerifyDOMTextChange {
	beforeByKey := make(map[string]string, len(before))
	for _, node := range before {
		beforeByKey[node.Key] = node.Text
	}
	diff := make([]consumerVerifyDOMTextChange, 0)
	for _, node := range after {
		old, exists := beforeByKey[node.Key]
		if !exists {
			diff = append(diff, consumerVerifyDOMTextChange{Kind: "added", Key: node.Key, After: sanitizeConsumerVerifyDOMText(node.Text)})
		} else if old != node.Text {
			diff = append(diff, consumerVerifyDOMTextChange{
				Kind:   "changed",
				Key:    node.Key,
				Before: sanitizeConsumerVerifyDOMText(old),
				After:  sanitizeConsumerVerifyDOMText(node.Text),
			})
		}
	}
	if len(diff) > 12 {
		diff = diff[:12]
	}
	return diff
}

func sanitizeConsumerVerifyDOMText(value string) string {
	lower := strings.ToLower(value)
	for _, forbidden := range []string{"token", "cookie", "authorization", "session"} {
		if strings.Contains(lower, forbidden) {
			return "<sensitive-text-redacted>"
		}
	}
	return sanitizeConsumerDiagnosticText(value)
}

func (a *ConsumerLoginAction) logConsumerVerifyDOMTextDiff(stage string, before, after []consumerVerifyDOMTextNode) {
	diff := consumerVerifyDOMTextDiff(before, after)
	encoded, err := json.Marshal(diff)
	if err != nil {
		encoded = []byte("[]")
	}
	value := sanitizeConsumerVerifyNetworkLog(string(encoded))
	if len(value) > consumerVerifyDOMDiffLogLimit {
		value = value[:consumerVerifyDOMDiffLogLimit]
	}
	logrus.Infof("consumer VerifyOTP DOM 文本差异[%s]: %s", stage, value)
}

func (a *ConsumerLoginAction) readConsumerVerifySecurityNodes() []consumerVerifySecurityNode {
	if a == nil || a.page == nil {
		return nil
	}
	result, err := a.page.Eval(consumerVerifySecurityNodesScript)
	if err != nil {
		logrus.Warnf("consumer VerifyOTP 安全节点读取失败: %v", err)
		return nil
	}
	var nodes []consumerVerifySecurityNode
	if err := json.Unmarshal([]byte(result.Value.String()), &nodes); err != nil {
		logrus.Warnf("consumer VerifyOTP 安全节点解析失败: %v", err)
	}
	return nodes
}

func consumerVerifySecurityNodeKey(node consumerVerifySecurityNode) string {
	return strings.Join([]string{node.Tag, node.ID, node.Class, node.URL}, "|")
}

func consumerVerifyNewSecurityNodes(before, after []consumerVerifySecurityNode) []consumerVerifySecurityNode {
	seen := make(map[string]struct{}, len(before))
	for _, node := range before {
		seen[consumerVerifySecurityNodeKey(node)] = struct{}{}
	}
	result := make([]consumerVerifySecurityNode, 0)
	for _, node := range after {
		if _, exists := seen[consumerVerifySecurityNodeKey(node)]; !exists {
			result = append(result, node)
		}
	}
	return result
}

func (a *ConsumerLoginAction) logConsumerVerifySecurityNodes(stage string, before, after []consumerVerifySecurityNode) {
	newNodes := consumerVerifyNewSecurityNodes(before, after)
	encoded, err := json.Marshal(newNodes)
	if err != nil {
		encoded = []byte("[]")
	}
	logrus.Infof("consumer VerifyOTP 新增安全节点[%s]: %s", stage, sanitizeConsumerVerifyNetworkLog(string(encoded)))
}

const consumerVerifyModalTextSnapshotScript = `() => {
  const visible = (node) => {
    if (!node || node.nodeType !== 1) return false;
    const style = window.getComputedStyle(node);
    const rect = node.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || 1) > 0 && rect.width > 0 && rect.height > 0 &&
      node.getAttribute('aria-hidden') !== 'true';
  };
  const clip = (value) => String(value || '').replace(/\s+/g, ' ').trim().slice(0, 120);
  const fixed = (value) => value.startsWith('我已阅读并同意') ||
    /^(用户协议|隐私政策|隐私协议|隐私条款|服务条款|手机号登录|获取验证码|登录)$/.test(value);
  const modal = Array.from(document.querySelectorAll(
    'div.reds-modal.reds-modal-open.login-modal,[role="dialog"],dialog,[class*="login-modal"]'
  )).find(visible);
  if (!modal) return JSON.stringify([]);
  const nodes = [];
  const selectors = 'span,p,label,a,small,div,[role="alert"],[aria-live]';
  let index = 0;
  for (const node of modal.querySelectorAll(selectors)) {
    if (!visible(node) || node.children.length > 0) continue;
    const value = clip(node.textContent || '');
    if (!value || fixed(value)) continue;
    nodes.push({
      key: String(node.tagName || '').toLowerCase() + '|' +
        clip(node.id || '') + '|' + clip(node.className || '') + '|' + index,
      text: value
    });
    index++;
    if (nodes.length >= 24) break;
  }
  return JSON.stringify(nodes);
}`

const consumerVerifySecurityNodesScript = `() => {
  const visible = (node) => {
    if (!node || node.nodeType !== 1) return false;
    const style = window.getComputedStyle(node);
    const rect = node.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      Number(style.opacity || 1) > 0 && rect.width > 0 && rect.height > 0 &&
      node.getAttribute('aria-hidden') !== 'true';
  };
  const clip = (value, limit = 120) => String(value || '').replace(/\s+/g, ' ').trim().slice(0, limit);
  const nodes = [];
  const seen = new Set();
  const selectors = [
    'iframe', '[class*="captcha" i]', '[class*="verification" i]',
    '[class*="security" i]', '[class*="risk" i]'
  ];
  for (const selector of selectors) {
    for (const node of document.querySelectorAll(selector)) {
      if (!visible(node)) continue;
      let safeURL = '';
      if (String(node.tagName || '').toLowerCase() === 'iframe') {
        try {
          const parsed = new URL(node.src || '', location.href);
          safeURL = parsed.protocol + '//' + parsed.host + (parsed.pathname || '/');
        } catch (_) {}
      }
      const item = {
        tag: String(node.tagName || '').toLowerCase(),
        id: clip(node.id || ''),
        class: clip(node.className || ''),
        url: safeURL,
        visibility: String(window.getComputedStyle(node).visibility || '')
      };
      const key = JSON.stringify(item);
      if (seen.has(key)) continue;
      seen.add(key);
      nodes.push(item);
      if (nodes.length >= 24) return JSON.stringify(nodes);
    }
  }
  return JSON.stringify(nodes);
}`
