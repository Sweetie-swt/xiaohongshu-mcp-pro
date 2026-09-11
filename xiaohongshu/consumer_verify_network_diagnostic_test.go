package xiaohongshu

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/require"
)

func TestConsumerVerifySafeXHSURLStripsQueryAndFragment(t *testing.T) {
	safeURL, ok := consumerVerifySafeXHSURL("https://www.xiaohongshu.com/api/login?phone=15376356205&otp=123456#fragment")
	require.True(t, ok)
	require.Equal(t, "https://www.xiaohongshu.com/api/login", safeURL)

	_, ok = consumerVerifySafeXHSURL("https://example.invalid/api/login?token=secret")
	require.False(t, ok)
}

func TestConsumerVerifyResponseFieldsUseTopLevelScalarAllowlist(t *testing.T) {
	body := `{"code":0,"success":false,"message":"验证码错误 123456","data":{"token":"secret-token"},"cookie":"secret-cookie","session":"secret-session"}`
	fields, ok := parseConsumerVerifyResponseFields(body)
	require.True(t, ok)
	require.Equal(t, "0", fields["code"])
	require.Equal(t, "false", fields["success"])
	require.Contains(t, fields["message"], "<digits-redacted>")
	for _, forbidden := range []string{"data", "cookie", "session", "token"} {
		_, present := fields[forbidden]
		require.False(t, present, "response field %s must not be retained", forbidden)
	}
	require.NotContains(t, strings.Join(mapValues(fields), " "), "123456")
	require.NotContains(t, strings.Join(mapValues(fields), " "), "secret")
	_, safe := sanitizeConsumerVerifyResponseString("session=secret-value")
	require.False(t, safe)
	longValue, safe := sanitizeConsumerVerifyResponseString(strings.Repeat("x", 200))
	require.True(t, safe)
	require.Len(t, longValue, 160)
}

func TestConsumerVerifyNetworkLogContainsMetadataOnly(t *testing.T) {
	event := consumerVerifyNetworkEvent{
		Method:           "POST",
		URL:              "https://www.xiaohongshu.com/api/login",
		ResourceType:     string(proto.NetworkResourceTypeXHR),
		RequestSent:      true,
		ResponseReceived: true,
		Status:           200,
		ResponseFields:   map[string]string{"code": "0", "message": "ok"},
	}
	encoded, err := json.Marshal([]consumerVerifyNetworkEvent{event})
	require.NoError(t, err)
	logLine := sanitizeConsumerVerifyNetworkLog(string(encoded))
	require.Contains(t, logLine, "api/login")
	require.NotContains(t, logLine, "phone=")
	require.NotContains(t, logLine, "otp=")
	require.NotContains(t, logLine, "request_body")
	require.NotContains(t, logLine, "postData")
	require.NotContains(t, logLine, "cookie")
	require.NotContains(t, logLine, "token")
	require.NotContains(t, logLine, "session")
}

func TestConsumerVerifyNetworkTraceArmsAtClickBoundary(t *testing.T) {
	trace := newConsumerVerifyNetworkTrace(nil)
	request := &proto.NetworkRequestWillBeSent{
		RequestID: "request-before-arm",
		Type:      proto.NetworkResourceTypeXHR,
		Request: &proto.NetworkRequest{
			URL:    "https://www.xiaohongshu.com/api/login",
			Method: "POST",
		},
	}
	trace.handleRequestWillBeSent(request)
	require.Empty(t, trace.Snapshot(), "pre-click requests must not enter the trace")
	trace.Arm()
	request.RequestID = "request-after-arm"
	trace.handleRequestWillBeSent(request)
	require.Len(t, trace.Snapshot(), 1)
}

func TestConsumerVerifyNetworkOutcomeClassification(t *testing.T) {
	tests := []struct {
		name     string
		events   []consumerVerifyNetworkEvent
		gate     bool
		gateKind string
		expected string
	}{
		{name: "not observed", expected: consumerVerifyOutcomeRequestNotObserved},
		{
			name:     "transport failure",
			events:   []consumerVerifyNetworkEvent{{URL: "https://www.xiaohongshu.com/api/login", ResourceType: string(proto.NetworkResourceTypeXHR), RequestSent: true, LoadingFailed: true}},
			expected: consumerVerifyOutcomeTransportFailed,
		},
		{
			name:     "http failure",
			events:   []consumerVerifyNetworkEvent{{URL: "https://www.xiaohongshu.com/api/login", ResourceType: string(proto.NetworkResourceTypeXHR), RequestSent: true, ResponseReceived: true, Status: 503}},
			expected: consumerVerifyOutcomeTransportFailed,
		},
		{
			name:     "business rejection",
			events:   []consumerVerifyNetworkEvent{{URL: "https://www.xiaohongshu.com/api/login", ResourceType: string(proto.NetworkResourceTypeXHR), RequestSent: true, ResponseReceived: true, Status: 200, ResponseFields: map[string]string{"success": "false", "message": "验证码错误"}}},
			expected: consumerVerifyOutcomeRejected,
		},
		{
			name:   "success but login gate remains",
			events: []consumerVerifyNetworkEvent{{URL: "https://www.xiaohongshu.com/api/login", ResourceType: string(proto.NetworkResourceTypeXHR), RequestSent: true, ResponseReceived: true, Status: 200, ResponseFields: map[string]string{"code": "0"}}},
			gate:   true, gateKind: "login",
			expected: consumerVerifyOutcomeSuccessGateRemains,
		},
		{
			name:   "security gate",
			events: []consumerVerifyNetworkEvent{{RequestSent: true}},
			gate:   true, gateKind: "verification",
			expected: consumerVerifyOutcomeSecurityVerification,
		},
		{
			name:     "request without safe conclusion",
			events:   []consumerVerifyNetworkEvent{{URL: "https://www.xiaohongshu.com/api/login", ResourceType: string(proto.NetworkResourceTypeXHR), RequestSent: true}},
			expected: consumerVerifyOutcomeDiagnosticInconclusive,
		},
		{
			name:     "unrelated request ignored",
			events:   []consumerVerifyNetworkEvent{{URL: "https://www.xiaohongshu.com/api/sns/web/v1/search", ResourceType: string(proto.NetworkResourceTypeXHR), RequestSent: true, ResponseReceived: true, Status: 503}},
			expected: consumerVerifyOutcomeRequestNotObserved,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, consumerVerifyNetworkOutcome(tt.events, tt.gate, tt.gateKind))
		})
	}
}

func TestConsumerVerifySecurityEvidenceOverridesTransportFailure(t *testing.T) {
	events := []consumerVerifyNetworkEvent{
		{
			Method:           "POST",
			URL:              "https://edith.xiaohongshu.com/api/sns/web/v2/login/code",
			ResourceType:     string(proto.NetworkResourceTypeXHR),
			RequestSent:      true,
			ResponseReceived: true,
			Status:           471,
		},
		{
			Method:           "POST",
			URL:              "https://edith.xiaohongshu.com/api/redcaptcha/v2/qr/init",
			ResourceType:     string(proto.NetworkResourceTypeFetch),
			RequestSent:      true,
			ResponseReceived: true,
			Status:           200,
		},
		{
			Method:           "POST",
			URL:              "https://edith.xiaohongshu.com/api/redcaptcha/v2/web/log",
			ResourceType:     string(proto.NetworkResourceTypeXHR),
			RequestSent:      true,
			ResponseReceived: true,
			Status:           200,
		},
	}
	nodes := []consumerVerifySecurityNode{{
		Tag:        "div",
		Class:      "r-captcha-modal theme-dark",
		Visibility: "visible",
	}}
	require.True(t, consumerVerifyVisibleSecurityChallenge(nodes))
	require.True(t, consumerVerifyRedCaptchaNetworkEvidence(events))
	require.Equal(t, consumerVerifyOutcomeSecurityVerification,
		consumerVerifyNetworkOutcomeWithSecurityEvidence(events, true, "login", true))
}

func TestConsumerVerifyOrdinaryHTTPFailureWithoutSecurityEvidenceRemainsTransport(t *testing.T) {
	events := []consumerVerifyNetworkEvent{{
		Method:           "POST",
		URL:              "https://edith.xiaohongshu.com/api/sns/web/v2/login/code",
		ResourceType:     string(proto.NetworkResourceTypeXHR),
		RequestSent:      true,
		ResponseReceived: true,
		Status:           503,
	}}
	require.False(t, consumerVerifyRedCaptchaNetworkEvidence(events))
	require.Equal(t, consumerVerifyOutcomeTransportFailed,
		consumerVerifyNetworkOutcomeWithSecurityEvidence(events, true, "login", false))
}

func TestConsumerVerifyHTTP471AloneDoesNotImplySecurity(t *testing.T) {
	events := []consumerVerifyNetworkEvent{{
		Method:           "POST",
		URL:              "https://edith.xiaohongshu.com/api/sns/web/v2/login/code",
		ResourceType:     string(proto.NetworkResourceTypeXHR),
		RequestSent:      true,
		ResponseReceived: true,
		Status:           471,
	}}
	require.Equal(t, consumerVerifyOutcomeTransportFailed,
		consumerVerifyNetworkOutcomeWithSecurityEvidence(events, true, "login", false))
}

func TestConsumerVerifyRedCaptchaNetworkEvidenceCanClassifyWithoutDOMSnapshot(t *testing.T) {
	events := []consumerVerifyNetworkEvent{
		{
			URL:              "https://edith.xiaohongshu.com/api/sns/web/v2/login/code",
			ResourceType:     string(proto.NetworkResourceTypeXHR),
			RequestSent:      true,
			ResponseReceived: true,
			Status:           471,
		},
		{
			URL:              "https://edith.xiaohongshu.com/api/redcaptcha/v2/qr/init?opaque=secret",
			ResourceType:     string(proto.NetworkResourceTypeXHR),
			RequestSent:      true,
			ResponseReceived: true,
			Status:           200,
		},
	}
	require.Equal(t, consumerVerifyOutcomeSecurityVerification,
		consumerVerifyNetworkOutcomeWithSecurityEvidence(events, true, "login", false))
}

func TestConsumerVerifyCaptchaWinsOverBackgroundLoginModal(t *testing.T) {
	events := []consumerVerifyNetworkEvent{{
		Method:           "POST",
		URL:              "https://edith.xiaohongshu.com/api/sns/web/v2/login/code",
		ResourceType:     string(proto.NetworkResourceTypeXHR),
		RequestSent:      true,
		ResponseReceived: true,
		Status:           471,
	}}
	nodes := []consumerVerifySecurityNode{
		{Tag: "div", Class: "reds-modal login-modal", Visibility: "visible"},
		{Tag: "div", Class: "r-captcha-modal theme-dark", Visibility: "visible"},
	}
	require.True(t, consumerVerifyVisibleSecurityChallenge(nodes))
	require.Equal(t, consumerVerifyOutcomeSecurityVerification,
		consumerVerifyNetworkOutcomeWithSecurityEvidence(events, true, "login", true))
}

func TestConsumerVerifySecurityEvidenceRequiresExplicitSemanticNodeOrRedCaptcha(t *testing.T) {
	require.False(t, consumerVerifyVisibleSecurityChallenge([]consumerVerifySecurityNode{{
		Tag: "iframe", Visibility: "visible",
	}}))
	require.False(t, consumerVerifyVisibleSecurityChallenge([]consumerVerifySecurityNode{{
		Tag: "div", Class: "captcha-modal", Visibility: "hidden",
	}}))
	require.False(t, consumerVerifyRedCaptchaNetworkEvidence([]consumerVerifyNetworkEvent{{
		URL:          "https://edith.xiaohongshu.com/api/sns/web/v2/login/code",
		ResourceType: string(proto.NetworkResourceTypeXHR),
		RequestSent:  true,
		Status:       471,
	}}))
}

func TestConsumerVerifyLoginResponseCandidateIsStrict(t *testing.T) {
	require.True(t, consumerVerifyLoginResponseCandidate(consumerVerifyNetworkEvent{
		URL:          "https://www.xiaohongshu.com/api/sns/web/v1/login",
		ResourceType: string(proto.NetworkResourceTypeXHR),
	}))
	require.True(t, consumerVerifyLoginResponseCandidate(consumerVerifyNetworkEvent{
		URL:          "https://www.xiaohongshu.com/api/sns/web/v1/verify_code",
		ResourceType: string(proto.NetworkResourceTypeFetch),
	}))
	require.False(t, consumerVerifyLoginResponseCandidate(consumerVerifyNetworkEvent{
		URL:          "https://www.xiaohongshu.com/api/sns/web/v1/search",
		ResourceType: string(proto.NetworkResourceTypeXHR),
	}))
	require.False(t, consumerVerifyLoginResponseCandidate(consumerVerifyNetworkEvent{
		URL:          "https://www.xiaohongshu.com/api/login",
		ResourceType: string(proto.NetworkResourceTypeDocument),
	}))
	require.False(t, consumerVerifyLoginResponseCandidate(consumerVerifyNetworkEvent{
		URL:          "https://example.invalid/api/login",
		ResourceType: string(proto.NetworkResourceTypeXHR),
	}))
}

func TestConsumerVerifyDOMTextDiffReportsOnlyShortChanges(t *testing.T) {
	before := []consumerVerifyDOMTextNode{{Key: "span|error", Text: "旧提示"}}
	after := []consumerVerifyDOMTextNode{
		{Key: "span|error", Text: "验证码错误 123456"},
		{Key: "span|new", Text: "请稍后再试"},
	}
	diff := consumerVerifyDOMTextDiff(before, after)
	require.Len(t, diff, 2)
	require.Equal(t, "changed", diff[0].Kind)
	require.Equal(t, "旧提示", diff[0].Before)
	require.Contains(t, diff[0].After, "<digits-redacted>")
	require.Equal(t, "added", diff[1].Kind)
	require.Empty(t, diff[1].Before)
	require.Equal(t, "请稍后再试", diff[1].After)
	require.NotContains(t, diff[0].After, "123456")
	require.Equal(t, "<sensitive-text-redacted>", sanitizeConsumerVerifyDOMText("session token"))
}

func TestConsumerVerifyDOMAndSecurityScriptsDoNotReadSensitiveValues(t *testing.T) {
	require.NotContains(t, consumerVerifyModalTextSnapshotScript, "input.value")
	require.NotContains(t, consumerVerifyModalTextSnapshotScript, "textarea.value")
	require.NotContains(t, consumerVerifyModalTextSnapshotScript, "document.body.innerText")
	require.Contains(t, consumerVerifyModalTextSnapshotScript, "slice(0, 120)")
	require.Contains(t, consumerVerifySecurityNodesScript, "parsed.pathname")
	require.NotContains(t, consumerVerifySecurityNodesScript, "parsed.search")
	require.NotContains(t, consumerVerifySecurityNodesScript, "innerText")
}

func mapValues(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}
