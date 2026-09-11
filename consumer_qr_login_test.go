package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/lisiyuan/xiaohongshu-mcp-pro/browser"
	"github.com/lisiyuan/xiaohongshu-mcp-pro/xiaohongshu"
)

func newConsumerQRLoginServiceForTest() (*XiaohongshuService, *browser.ProfileBrowser, *rod.Page) {
	service := &XiaohongshuService{}
	profileBrowser := &browser.ProfileBrowser{}
	page := &rod.Page{}
	service.consumerQRLoginBrowser = profileBrowser
	service.consumerQRLoginPage = page
	return service, profileBrowser, page
}

func TestConsumerQRLoginRejectsExistingLoginFlow(t *testing.T) {
	tests := []struct {
		name    string
		service *XiaohongshuService
	}{
		{
			name: "consumer phone flow",
			service: &XiaohongshuService{
				consumerLoginBrowser: &browser.ProfileBrowser{},
				consumerLoginPage:    &rod.Page{},
			},
		},
		{
			name: "consumer QR flow",
			service: &XiaohongshuService{
				consumerQRLoginBrowser: &browser.ProfileBrowser{},
				consumerQRLoginPage:    &rod.Page{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := tt.service
			if _, err := service.ConsumerQRLogin(context.Background()); err == nil {
				t.Fatal("consumer QR login must reject an existing login flow")
			}
			if service.consumerQRLoginBrowser == nil || service.consumerQRLoginPage == nil {
				t.Fatal("conflict must not replace the existing pending flow")
			}
		})
	}
}

func TestConsumerCompleteQRLoginWithoutPendingFlow(t *testing.T) {
	service := &XiaohongshuService{}
	if _, err := service.ConsumerCompleteQRLogin(context.Background()); err == nil || err.Error() != "no_active_consumer_qr_login" {
		t.Fatalf("ConsumerCompleteQRLogin() error = %v, want no_active_consumer_qr_login", err)
	}
}

func TestConsumerCompleteQRLoginExpiredCleansUpWithoutRefresh(t *testing.T) {
	service, profileBrowser, page := newConsumerQRLoginServiceForTest()
	closed := false
	service.consumerQRWaitFunc = func(ctx context.Context, gotPage *rod.Page, timeout time.Duration) (xiaohongshu.ConsumerQRCodeWaitStatus, error) {
		if ctx == nil || gotPage != page || timeout != consumerQRServerWaitTimeout {
			t.Fatalf("unexpected QR wait arguments")
		}
		return xiaohongshu.ConsumerQRCodeLoginExpired, nil
	}
	service.consumerQRCloseLoginSessionFunc = func(gotBrowser *browser.ProfileBrowser, gotPage *rod.Page) {
		if gotBrowser != profileBrowser || gotPage != page {
			t.Fatalf("unexpected QR session cleanup arguments")
		}
		closed = true
	}

	result, err := service.ConsumerCompleteQRLogin(context.Background())
	if err != nil || result == nil || result.Status != consumerQRStatusExpired {
		t.Fatalf("expired completion = %#v, error = %v", result, err)
	}
	if !closed || service.consumerQRLoginBrowser != nil || service.consumerQRLoginPage != nil {
		t.Fatal("expired QR flow must clean up and clear pending state")
	}
}

func TestConsumerCompleteQRLoginTimeoutCleansUp(t *testing.T) {
	service, _, _ := newConsumerQRLoginServiceForTest()
	closed := false
	service.consumerQRWaitFunc = func(context.Context, *rod.Page, time.Duration) (xiaohongshu.ConsumerQRCodeWaitStatus, error) {
		return "", context.DeadlineExceeded
	}
	service.consumerQRCloseLoginSessionFunc = func(*browser.ProfileBrowser, *rod.Page) { closed = true }

	result, err := service.ConsumerCompleteQRLogin(context.Background())
	if err != nil || result == nil || result.Status != consumerQRStatusTimeout {
		t.Fatalf("timeout completion = %#v, error = %v", result, err)
	}
	if !closed || service.consumerQRLoginBrowser != nil || service.consumerQRLoginPage != nil {
		t.Fatal("timeout QR flow must clean up and clear pending state")
	}
}

func TestConsumerCompleteQRLoginRequiresPositiveFinalizer(t *testing.T) {
	tests := []struct {
		name          string
		finalizeError error
		wantError     bool
	}{
		{name: "finalizer failure", finalizeError: fmt.Errorf("no positive consumer evidence"), wantError: true},
		{name: "finalizer success", wantError: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, _, page := newConsumerQRLoginServiceForTest()
			closed := false
			service.consumerQRWaitFunc = func(context.Context, *rod.Page, time.Duration) (xiaohongshu.ConsumerQRCodeWaitStatus, error) {
				return xiaohongshu.ConsumerQRCodeModalMissing, nil
			}
			service.consumerQRFinalizeLoginFunc = func(gotPage *rod.Page) error {
				if gotPage != page {
					t.Fatalf("finalizer received the wrong page")
				}
				return tt.finalizeError
			}
			service.consumerQRCloseLoginSessionFunc = func(*browser.ProfileBrowser, *rod.Page) { closed = true }

			result, err := service.ConsumerCompleteQRLogin(context.Background())
			if (err != nil) != tt.wantError {
				t.Fatalf("completion error = %v, wantError=%t", err, tt.wantError)
			}
			if tt.wantError {
				if result != nil || !errors.Is(err, tt.finalizeError) {
					t.Fatalf("failed finalizer result=%#v error=%v", result, err)
				}
			} else if result == nil || result.Status != consumerQRStatusSuccess {
				t.Fatalf("successful finalizer result=%#v error=%v", result, err)
			}
			if !closed || service.consumerQRLoginBrowser != nil || service.consumerQRLoginPage != nil {
				t.Fatal("finalizer completion must clean up and clear pending state")
			}
		})
	}
}
