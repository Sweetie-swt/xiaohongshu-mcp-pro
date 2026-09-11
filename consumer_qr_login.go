package main

import (
	"context"
	"fmt"
	"time"

	"github.com/go-rod/rod"
	"github.com/lisiyuan/xiaohongshu-mcp-pro/browser"
	"github.com/lisiyuan/xiaohongshu-mcp-pro/xiaohongshu"
)

const (
	consumerQRServerWaitTimeout = 180 * time.Second

	consumerQRStatusReady   = "qr_ready"
	consumerQRStatusSuccess = "success"
	consumerQRStatusExpired = "expired"
	consumerQRStatusTimeout = "timeout"
)

// ConsumerQRLoginResponse is intentionally split from the text status: the
// image is returned as MCP image content, never as a URL or data URL string.
type ConsumerQRLoginResponse struct {
	Status string
	Image  []byte
}

// ConsumerQRLogin starts one persistent consumer QR flow and leaves its
// browser/page in memory for ConsumerCompleteQRLogin.
func (s *XiaohongshuService) ConsumerQRLogin(ctx context.Context) (*ConsumerQRLoginResponse, error) {
	s.loginFlowMu.Lock()
	defer s.loginFlowMu.Unlock()

	if s.creatorLoginBrowser != nil || s.creatorLoginPage != nil {
		return nil, fmt.Errorf("已有 creator 登录会话正在进行，拒绝创建 consumer QR 登录")
	}
	if s.consumerLoginBrowser != nil || s.consumerLoginPage != nil {
		return nil, fmt.Errorf("已有 consumer 手机号登录会话正在进行，拒绝创建第二个 consumer 登录")
	}
	if s.consumerQRLoginBrowser != nil || s.consumerQRLoginPage != nil {
		return nil, fmt.Errorf("已有活跃的 consumer QR 登录流程")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	b, err := newProfileBrowserWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("创建 consumer QR profile browser 失败: %w", err)
	}
	// The pending page must outlive this MCP request; bind page creation to a
	// background context rather than the request context.
	page, err := b.NewPageWithContext(context.Background())
	if err != nil {
		b.Close()
		return nil, fmt.Errorf("创建 consumer QR 登录页面失败: %w", err)
	}
	image, err := xiaohongshu.NewConsumerLogin(page).NavigateToQRCodeLogin(ctx)
	if err != nil {
		_ = page.Close()
		b.Close()
		return nil, err
	}

	s.consumerQRLoginBrowser = b
	s.consumerQRLoginPage = page
	return &ConsumerQRLoginResponse{Status: consumerQRStatusReady, Image: image}, nil
}

// ConsumerCompleteQRLogin waits for the already-open QR page. It never
// creates or refreshes a QR code and only saves cookies after the existing
// consumer-specific positive page acceptance succeeds.
func (s *XiaohongshuService) ConsumerCompleteQRLogin(ctx context.Context) (*ConsumerQRLoginResponse, error) {
	s.loginFlowMu.Lock()
	defer s.loginFlowMu.Unlock()

	if s.consumerQRLoginBrowser == nil || s.consumerQRLoginPage == nil {
		return nil, fmt.Errorf("no_active_consumer_qr_login")
	}

	b := s.consumerQRLoginBrowser
	page := s.consumerQRLoginPage
	waitCtx, cancel := context.WithTimeout(context.Background(), consumerQRServerWaitTimeout)
	defer cancel()
	waitStatus, err := s.waitForConsumerQRLogin(waitCtx, page, consumerQRServerWaitTimeout)
	if err != nil {
		s.releaseConsumerQRLoginSession(b, page)
		if waitCtx.Err() == context.DeadlineExceeded || err == context.DeadlineExceeded {
			return &ConsumerQRLoginResponse{Status: consumerQRStatusTimeout}, nil
		}
		return nil, err
	}
	if waitStatus == xiaohongshu.ConsumerQRCodeLoginExpired {
		s.releaseConsumerQRLoginSession(b, page)
		return &ConsumerQRLoginResponse{Status: consumerQRStatusExpired}, nil
	}
	if waitStatus != xiaohongshu.ConsumerQRCodeModalMissing {
		s.releaseConsumerQRLoginSession(b, page)
		return nil, fmt.Errorf("consumer QR 登录结束于未预期状态: %s", waitStatus)
	}

	if err := s.finalizeConsumerQRLogin(page); err != nil {
		s.releaseConsumerQRLoginSession(b, page)
		return nil, err
	}
	s.releaseConsumerQRLoginSession(b, page)
	return &ConsumerQRLoginResponse{Status: consumerQRStatusSuccess}, nil
}

func (s *XiaohongshuService) waitForConsumerQRLogin(ctx context.Context, page *rod.Page, timeout time.Duration) (xiaohongshu.ConsumerQRCodeWaitStatus, error) {
	if s.consumerQRWaitFunc != nil {
		return s.consumerQRWaitFunc(ctx, page, timeout)
	}
	return xiaohongshu.NewConsumerLogin(page).WaitForConsumerQRCodeLogin(ctx, timeout)
}

func (s *XiaohongshuService) finalizeConsumerQRLogin(page *rod.Page) error {
	if s.consumerQRFinalizeLoginFunc != nil {
		return s.consumerQRFinalizeLoginFunc(page)
	}
	return s.finalizeConsumerLogin(page)
}

func (s *XiaohongshuService) releaseConsumerQRLoginSession(b *browser.ProfileBrowser, page *rod.Page) {
	if s.consumerQRLoginBrowser == b {
		s.consumerQRLoginBrowser = nil
	}
	if s.consumerQRLoginPage == page {
		s.consumerQRLoginPage = nil
	}
	if s.consumerQRCloseLoginSessionFunc != nil {
		s.consumerQRCloseLoginSessionFunc(b, page)
		return
	}
	if page != nil {
		_ = page.Close()
	}
	if b != nil {
		b.Close()
	}
}
