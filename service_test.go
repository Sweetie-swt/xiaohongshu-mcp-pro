package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/lisiyuan/xiaohongshu-mcp-pro/browser"
	"github.com/lisiyuan/xiaohongshu-mcp-pro/xiaohongshu"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCreatorPhoneLoginSessionRetention(t *testing.T) {
	tests := []struct {
		name   string
		result *xiaohongshu.OTPSendResult
		err    error
		retain bool
	}{
		{
			name:   "confirmed retains session",
			result: &xiaohongshu.OTPSendResult{Status: xiaohongshu.OTPSendConfirmed},
			retain: true,
		},
		{
			name:   "uncertain retains session",
			result: &xiaohongshu.OTPSendResult{Status: xiaohongshu.OTPSendUncertain},
			retain: true,
		},
		{
			name:   "explicit failure does not retain session",
			result: &xiaohongshu.OTPSendResult{Status: xiaohongshu.OTPSendFailed},
			err:    fmt.Errorf("页面提示：发送失败"),
			retain: false,
		},
		{
			name:   "missing result does not retain session",
			err:    fmt.Errorf("发送结果缺失"),
			retain: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &XiaohongshuService{}
			profileBrowser := &browser.ProfileBrowser{}
			page := &rod.Page{}
			if got := service.retainCreatorLoginSession(profileBrowser, page, tt.result, tt.err); got != tt.retain {
				t.Fatalf("retainCreatorLoginSession() = %t, want %t", got, tt.retain)
			}
			if tt.retain {
				if service.creatorLoginBrowser != profileBrowser || service.creatorLoginPage != page {
					t.Fatal("expected creator browser and page to be retained")
				}
				return
			}
			if service.creatorLoginBrowser != nil || service.creatorLoginPage != nil {
				t.Fatal("explicit failure must not retain creator browser or page")
			}
		})
	}
}

func newCreatorLoginServiceForTest() (*XiaohongshuService, *browser.ProfileBrowser, *rod.Page) {
	service := &XiaohongshuService{}
	profileBrowser := &browser.ProfileBrowser{}
	page := &rod.Page{}
	service.creatorLoginBrowser = profileBrowser
	service.creatorLoginPage = page
	return service, profileBrowser, page
}

func TestCreatorVerifyOTPDirectSuccessFinalizesAndReleasesSession(t *testing.T) {
	service, profileBrowser, page := newCreatorLoginServiceForTest()
	finalized := false
	closed := false
	service.creatorVerifyOTPFunc = func(gotPage *rod.Page, otp string) (*xiaohongshu.OTPVerificationResult, error) {
		if gotPage != page || otp != "test-otp" {
			t.Fatalf("unexpected VerifyOTP arguments")
		}
		return &xiaohongshu.OTPVerificationResult{Status: xiaohongshu.OTPVerificationSucceeded}, nil
	}
	service.creatorFinalizeLoginFunc = func(gotPage *rod.Page) error {
		if gotPage != page {
			t.Fatalf("finalizer received the wrong page")
		}
		finalized = true
		return nil
	}
	service.creatorCloseLoginSessionFunc = func(gotBrowser *browser.ProfileBrowser, gotPage *rod.Page) {
		if gotBrowser != profileBrowser || gotPage != page {
			t.Fatalf("closer received the wrong session")
		}
		closed = true
	}

	result, err := service.CreatorVerifyOTP("test-otp")
	if err != nil {
		t.Fatalf("CreatorVerifyOTP() error = %v", err)
	}
	if result == nil || result.Status != xiaohongshu.OTPVerificationSucceeded {
		t.Fatalf("unexpected direct-success result: %#v", result)
	}
	if !finalized || !closed {
		t.Fatalf("direct success must finalize and release the session: finalized=%t closed=%t", finalized, closed)
	}
	if service.creatorLoginBrowser != nil || service.creatorLoginPage != nil {
		t.Fatal("direct success must clear the temporary session")
	}
}

func TestCreatorVerifyOTPSecurityVerificationRetainsSession(t *testing.T) {
	service, profileBrowser, page := newCreatorLoginServiceForTest()
	finalized := false
	closed := false
	service.creatorVerifyOTPFunc = func(*rod.Page, string) (*xiaohongshu.OTPVerificationResult, error) {
		return &xiaohongshu.OTPVerificationResult{
			Status:                 xiaohongshu.OTPVerificationSecurityVerificationNeeded,
			SecurityVerificationQR: []byte("png"),
		}, nil
	}
	service.creatorFinalizeLoginFunc = func(*rod.Page) error {
		finalized = true
		return nil
	}
	service.creatorCloseLoginSessionFunc = func(*browser.ProfileBrowser, *rod.Page) {
		closed = true
	}

	result, err := service.CreatorVerifyOTP("test-otp")
	if err != nil {
		t.Fatalf("CreatorVerifyOTP() error = %v", err)
	}
	if result == nil || result.Status != xiaohongshu.OTPVerificationSecurityVerificationNeeded {
		t.Fatalf("unexpected security-verification result: %#v", result)
	}
	if string(result.SecurityQRShot) != "png" {
		t.Fatal("security-verification screenshot was not returned")
	}
	if finalized || closed {
		t.Fatalf("security-verification state must not finalize or close the session")
	}
	if service.creatorLoginBrowser != profileBrowser || service.creatorLoginPage != page {
		t.Fatal("security-verification state must retain the same browser and page")
	}
}

func TestCreatorVerifyOTPFailureReleasesSession(t *testing.T) {
	service, _, _ := newCreatorLoginServiceForTest()
	closed := false
	service.creatorVerifyOTPFunc = func(*rod.Page, string) (*xiaohongshu.OTPVerificationResult, error) {
		return nil, fmt.Errorf("页面提示：验证码错误")
	}
	service.creatorCloseLoginSessionFunc = func(*browser.ProfileBrowser, *rod.Page) {
		closed = true
	}

	if _, err := service.CreatorVerifyOTP("test-otp"); err == nil {
		t.Fatal("explicit VerifyOTP failure must be returned")
	}
	if !closed {
		t.Fatal("explicit VerifyOTP failure must close the temporary session")
	}
	if service.creatorLoginBrowser != nil || service.creatorLoginPage != nil {
		t.Fatal("explicit VerifyOTP failure must clear the temporary session")
	}
}

func TestCreatorCompleteSecurityVerificationWithoutSessionFails(t *testing.T) {
	service := &XiaohongshuService{}
	if _, err := service.CreatorCompleteSecurityVerification(); err == nil {
		t.Fatal("complete security verification without a session must fail")
	}
}

func TestCreatorCompleteSecurityVerificationSuccessFinalizesAndReleasesSession(t *testing.T) {
	service, profileBrowser, page := newCreatorLoginServiceForTest()
	waited := false
	finalized := false
	closed := false
	service.creatorWaitSecurityVerifyFunc = func(gotPage *rod.Page, timeout time.Duration) error {
		if gotPage != page || timeout != 120*time.Second {
			t.Fatalf("unexpected security wait arguments")
		}
		waited = true
		return nil
	}
	service.creatorFinalizeLoginFunc = func(gotPage *rod.Page) error {
		if gotPage != page {
			t.Fatalf("finalizer received the wrong page")
		}
		finalized = true
		return nil
	}
	service.creatorCloseLoginSessionFunc = func(gotBrowser *browser.ProfileBrowser, gotPage *rod.Page) {
		if gotBrowser != profileBrowser || gotPage != page {
			t.Fatalf("closer received the wrong session")
		}
		closed = true
	}

	result, err := service.CreatorCompleteSecurityVerification()
	if err != nil {
		t.Fatalf("CreatorCompleteSecurityVerification() error = %v", err)
	}
	if result == nil || result.Status != xiaohongshu.OTPVerificationSucceeded {
		t.Fatalf("unexpected completion result: %#v", result)
	}
	if !waited || !finalized || !closed {
		t.Fatalf("completion must wait, finalize, and release: waited=%t finalized=%t closed=%t", waited, finalized, closed)
	}
	if service.creatorLoginBrowser != nil || service.creatorLoginPage != nil {
		t.Fatal("successful completion must clear the temporary session")
	}
}

func TestCreatorCompleteSecurityVerificationFailureReleasesSession(t *testing.T) {
	service, _, _ := newCreatorLoginServiceForTest()
	closed := false
	service.creatorWaitSecurityVerifyFunc = func(*rod.Page, time.Duration) error {
		return fmt.Errorf("安全验证超时")
	}
	service.creatorCloseLoginSessionFunc = func(*browser.ProfileBrowser, *rod.Page) {
		closed = true
	}

	if _, err := service.CreatorCompleteSecurityVerification(); err == nil {
		t.Fatal("security verification timeout must fail")
	}
	if !closed {
		t.Fatal("security verification timeout must close the temporary session")
	}
	if service.creatorLoginBrowser != nil || service.creatorLoginPage != nil {
		t.Fatal("security verification timeout must clear the temporary session")
	}
}

func TestHandleCreatorVerifyOTPSecurityVerificationReturnsScreenshot(t *testing.T) {
	service, _, _ := newCreatorLoginServiceForTest()
	service.creatorVerifyOTPFunc = func(*rod.Page, string) (*xiaohongshu.OTPVerificationResult, error) {
		return &xiaohongshu.OTPVerificationResult{
			Status:                 xiaohongshu.OTPVerificationSecurityVerificationNeeded,
			SecurityVerificationQR: []byte("png"),
		}, nil
	}
	appServer := &AppServer{xiaohongshuService: service}

	result := appServer.handleCreatorVerifyOTP(context.Background(), "test-otp")
	if result.IsError {
		t.Fatal("security verification is an intermediate non-error result")
	}
	if len(result.Content) != 2 || result.Content[0].Type != "text" || result.Content[1].Type != "image" {
		t.Fatalf("expected status text and screenshot content, got %#v", result.Content)
	}
	if !strings.Contains(result.Content[0].Text, "security_verification_required") {
		t.Fatalf("missing security verification status: %q", result.Content[0].Text)
	}
}

func TestWithProfileBrowserPageCleansUpAfterSuccessErrorTimeoutCancelAndPanic(t *testing.T) {
	originalFactory := profileBrowserPageFactory
	originalPageCloser := profilePageCloser
	originalBrowserCloser := profileBrowserCloser
	t.Cleanup(func() {
		profileBrowserPageFactory = originalFactory
		profilePageCloser = originalPageCloser
		profileBrowserCloser = originalBrowserCloser
	})

	tests := []struct {
		name      string
		timeout   time.Duration
		parentCtx func() (context.Context, context.CancelFunc)
		callback  func(context.Context, *rod.Page) error
		wantErr   bool
	}{
		{
			name: "success",
			parentCtx: func() (context.Context, context.CancelFunc) {
				return context.Background(), func() {}
			},
			callback: func(context.Context, *rod.Page) error { return nil },
		},
		{
			name: "error",
			parentCtx: func() (context.Context, context.CancelFunc) {
				return context.Background(), func() {}
			},
			callback: func(context.Context, *rod.Page) error { return fmt.Errorf("expected action error") },
			wantErr:  true,
		},
		{
			name:    "timeout",
			timeout: 20 * time.Millisecond,
			parentCtx: func() (context.Context, context.CancelFunc) {
				return context.Background(), func() {}
			},
			callback: func(ctx context.Context, _ *rod.Page) error {
				<-ctx.Done()
				return ctx.Err()
			},
			wantErr: true,
		},
		{
			name: "cancellation",
			parentCtx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			},
			callback: func(ctx context.Context, _ *rod.Page) error {
				return ctx.Err()
			},
			wantErr: true,
		},
		{
			name: "panic becomes error",
			parentCtx: func() (context.Context, context.CancelFunc) {
				return context.Background(), func() {}
			},
			callback: func(context.Context, *rod.Page) error { panic("expected browser action panic") },
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeBrowser := &browser.ProfileBrowser{}
			fakePage := &rod.Page{}
			pageClosed := false
			browserClosed := false
			profileBrowserPageFactory = func(context.Context) (*browser.ProfileBrowser, *rod.Page, error) {
				return fakeBrowser, fakePage, nil
			}
			profilePageCloser = func(*rod.Page, context.Context) error {
				pageClosed = true
				return nil
			}
			profileBrowserCloser = func(*browser.ProfileBrowser) { browserClosed = true }

			ctx, cancel := tt.parentCtx()
			defer cancel()
			timeout := tt.timeout
			if timeout == 0 {
				timeout = time.Second
			}
			err := withProfileBrowserPage(ctx, "test_profile_read", timeout, tt.callback)
			if (err != nil) != tt.wantErr {
				t.Fatalf("withProfileBrowserPage() error=%v, wantErr=%t", err, tt.wantErr)
			}
			if !pageClosed || !browserClosed {
				t.Fatalf("cleanup flags: pageClosed=%t browserClosed=%t", pageClosed, browserClosed)
			}
		})
	}
}

func TestHandleSearchFeedsLaunchFailureReturnsToolError(t *testing.T) {
	originalFactory := profileBrowserPageFactory
	t.Cleanup(func() { profileBrowserPageFactory = originalFactory })
	profileBrowserPageFactory = func(context.Context) (*browser.ProfileBrowser, *rod.Page, error) {
		return nil, nil, fmt.Errorf("Chrome profile launch failed")
	}

	appServer := &AppServer{xiaohongshuService: &XiaohongshuService{}}
	result := appServer.handleSearchFeeds(context.Background(), SearchFeedsArgs{Keyword: "test"})
	if !result.IsError {
		t.Fatal("expected browser launch failure to be returned as a tool error")
	}
	if !strings.Contains(result.Content[0].Text, "Chrome profile launch failed") {
		t.Fatalf("missing launch error text: %#v", result.Content)
	}
}

func TestMCPToolRegistrationCount(t *testing.T) {
	server := InitMCPServer(&AppServer{xiaohongshuService: &XiaohongshuService{}})
	client := mcp.NewClient(&mcp.Implementation{Name: "registration-test", Version: "test"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	result, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(result.Tools); got != 21 {
		t.Fatalf("registered MCP tools=%d, want 21", got)
	}
}

func TestSearchFilterOptionsOnlyPassesRealFilters(t *testing.T) {
	if got := searchFilterOptions(FilterOption{}); len(got) != 0 {
		t.Fatalf("empty search filter produced %d filter arguments, want 0", len(got))
	}

	cases := []struct {
		name  string
		input FilterOption
	}{
		{name: "sort", input: FilterOption{SortBy: "最新"}},
		{name: "note type", input: FilterOption{NoteType: "图文"}},
		{name: "publish time", input: FilterOption{PublishTime: "一天内"}},
		{name: "scope", input: FilterOption{SearchScope: "未看过"}},
		{name: "location", input: FilterOption{Location: "同城"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := searchFilterOptions(tt.input)
			if len(got) != 1 {
				t.Fatalf("real filter produced %d filter arguments, want 1", len(got))
			}
			if got[0].SortBy != tt.input.SortBy ||
				got[0].NoteType != tt.input.NoteType ||
				got[0].PublishTime != tt.input.PublishTime ||
				got[0].SearchScope != tt.input.SearchScope ||
				got[0].Location != tt.input.Location {
				t.Fatalf("filter was not passed through unchanged: got=%#v want=%#v", got[0], tt.input)
			}
		})
	}
}
