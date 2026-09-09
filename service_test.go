package main

import (
	"fmt"
	"testing"

	"github.com/go-rod/rod"
	"github.com/lisiyuan/xiaohongshu-mcp-pro/browser"
	"github.com/lisiyuan/xiaohongshu-mcp-pro/xiaohongshu"
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
