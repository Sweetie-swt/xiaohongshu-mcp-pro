package xiaohongshu

import (
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/require"
)

func TestInspectConsumerCookieStatusSeparatesCreatorAndConsumerScope(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cookies := []*proto.NetworkCookie{
		{Name: "web_session", Value: "present", Domain: "creator.xiaohongshu.com", Path: "/", Session: true},
		{Name: "web_session", Value: "present", Domain: ".xiaohongshu.com", Path: "/", Expires: proto.TimeSinceEpoch(now.Add(time.Hour).Unix())},
		{Name: "web_session", Value: "present", Domain: "www.xiaohongshu.com", Path: "/search", Session: true},
		{Name: "web_session", Value: "present", Domain: "www.xiaohongshu.com", Path: "/", Expires: proto.TimeSinceEpoch(now.Add(-time.Hour).Unix())},
		{Name: "other_cookie", Value: "present", Domain: "www.xiaohongshu.com", Path: "/", Session: true},
	}

	status := inspectConsumerCookieStatus(cookies, now)
	require.Equal(t, 4, status.WebSessionCount)
	require.Equal(t, 3, status.ConsumerScopedCount)
	require.Equal(t, 1, status.ValidConsumerCount)
	require.Equal(t, 1, status.CreatorScopedCount)
	require.Equal(t, 2, status.InvalidConsumerCount)
	require.True(t, status.HasValidConsumerSession())
}

func TestInspectConsumerCookieStatusRejectsMissingValueAndWrongDomain(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cookies := []*proto.NetworkCookie{
		{Name: "web_session", Domain: "www.xiaohongshu.com", Path: "/", Session: true},
		{Name: "web_session", Value: "present", Domain: "api.xiaohongshu.com", Path: "/", Session: true},
		{Name: "web_session", Value: "present", Domain: "creator.xiaohongshu.com", Path: "/", Session: true},
	}

	status := inspectConsumerCookieStatus(cookies, now)
	require.Equal(t, 3, status.WebSessionCount)
	require.Equal(t, 1, status.ConsumerScopedCount)
	require.Zero(t, status.ValidConsumerCount)
	require.Equal(t, 1, status.CreatorScopedCount)
	require.Equal(t, 1, status.InvalidConsumerCount)
	require.False(t, status.HasValidConsumerSession())
}

func TestConsumerAuthGateScriptCoversKnownAndGenericModalStructures(t *testing.T) {
	for _, fragment := range []string{
		"div.reds-modal.reds-modal-open.login-modal",
		"i.reds-mask",
		"[role=\"dialog\"]",
		"[class*=\"modal\"]",
		"安全验证",
		"手机号登录",
	} {
		require.Contains(t, consumerAuthGateScript, fragment)
	}
}
