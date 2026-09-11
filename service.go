package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/go-rod/rod"
	"github.com/lisiyuan/xiaohongshu-mcp-pro/accounts"
	"github.com/lisiyuan/xiaohongshu-mcp-pro/browser"
	"github.com/lisiyuan/xiaohongshu-mcp-pro/configs"
	"github.com/lisiyuan/xiaohongshu-mcp-pro/cookies"
	"github.com/lisiyuan/xiaohongshu-mcp-pro/pkg/downloader"
	"github.com/lisiyuan/xiaohongshu-mcp-pro/pkg/xhsutil"
	"github.com/lisiyuan/xiaohongshu-mcp-pro/xiaohongshu"
	"github.com/sirupsen/logrus"
)

// XiaohongshuService 小红书业务服务
type XiaohongshuService struct {
	// creatorLoginBrowser/Page 用于 creator 手机号登录流程（跨两次 MCP 调用）
	creatorLoginBrowser *browser.ProfileBrowser
	creatorLoginPage    *rod.Page

	// consumerLoginBrowser/Page 用于 www 消费端手机号登录流程（跨两次 MCP 调用）
	consumerLoginBrowser *browser.ProfileBrowser
	consumerLoginPage    *rod.Page

	// 以下函数仅用于隔离 creator 登录流程的单元测试；生产环境保持 nil，走真实实现。
	creatorVerifyOTPFunc          func(*rod.Page, string) (*xiaohongshu.OTPVerificationResult, error)
	creatorWaitSecurityVerifyFunc func(*rod.Page, time.Duration) error
	creatorFinalizeLoginFunc      func(*rod.Page) error
	creatorCloseLoginSessionFunc  func(*browser.ProfileBrowser, *rod.Page)
}

// NewXiaohongshuService 创建小红书服务实例
func NewXiaohongshuService() *XiaohongshuService {
	return &XiaohongshuService{}
}

// PublishRequest 发布请求
type PublishRequest struct {
	Title      string   `json:"title" binding:"required"`
	Content    string   `json:"content" binding:"required"`
	Images     []string `json:"images" binding:"required,min=1"`
	Tags       []string `json:"tags,omitempty"`
	ScheduleAt string   `json:"schedule_at,omitempty"` // 定时发布时间，ISO8601格式，为空则立即发布
	IsOriginal bool     `json:"is_original,omitempty"` // 是否声明原创
	Visibility string   `json:"visibility,omitempty"`  // 可见范围: "公开可见"(默认), "仅自己可见", "仅互关好友可见"
	Products   []string `json:"products,omitempty"`    // 商品关键词列表，用于绑定带货商品
}

// LoginStatusResponse 登录状态响应
type LoginStatusResponse struct {
	IsLoggedIn bool   `json:"is_logged_in"`
	Username   string `json:"username,omitempty"`
}

// LoginQrcodeResponse 登录扫码二维码
type LoginQrcodeResponse struct {
	Timeout    string `json:"timeout"`
	IsLoggedIn bool   `json:"is_logged_in"`
	Img        string `json:"img,omitempty"`
}

// PublishResponse 发布响应
type PublishResponse struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Images  int    `json:"images"`
	Status  string `json:"status"`
	PostID  string `json:"post_id,omitempty"`
}

// PublishVideoRequest 发布视频请求（仅支持本地单个视频文件）
type PublishVideoRequest struct {
	Title      string   `json:"title" binding:"required"`
	Content    string   `json:"content" binding:"required"`
	Video      string   `json:"video" binding:"required"`
	Tags       []string `json:"tags,omitempty"`
	ScheduleAt string   `json:"schedule_at,omitempty"` // 定时发布时间，ISO8601格式，为空则立即发布
	Visibility string   `json:"visibility,omitempty"`  // 可见范围: "公开可见"(默认), "仅自己可见", "仅互关好友可见"
	Products   []string `json:"products,omitempty"`    // 商品关键词列表，用于绑定带货商品
}

// PublishVideoResponse 发布视频响应
type PublishVideoResponse struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Video   string `json:"video"`
	Status  string `json:"status"`
	PostID  string `json:"post_id,omitempty"`
}

// FeedsListResponse Feeds列表响应
type FeedsListResponse struct {
	Feeds []xiaohongshu.Feed `json:"feeds"`
	Count int                `json:"count"`
}

// UserProfileResponse 用户主页响应
type UserProfileResponse struct {
	UserBasicInfo xiaohongshu.UserBasicInfo      `json:"userBasicInfo"`
	Interactions  []xiaohongshu.UserInteractions `json:"interactions"`
	Feeds         []xiaohongshu.Feed             `json:"feeds"`
}

// DeleteCookies 删除当前激活账号的 cookies 文件，重置为未登录
func (s *XiaohongshuService) DeleteCookies(ctx context.Context) error {
	cookiePath := accounts.GetManager().GetActiveCookiesPath()
	return cookies.NewLoadCookie(cookiePath).DeleteCookies()
}

// CheckLoginStatus 检查登录状态
func (s *XiaohongshuService) CheckLoginStatus(ctx context.Context) (*LoginStatusResponse, error) {
	var isLoggedIn bool
	err := withProfileBrowserPage(ctx, "check_login_status", 90*time.Second, func(actionCtx context.Context, page *rod.Page) error {
		loginAction := xiaohongshu.NewLogin(page)
		var err error
		isLoggedIn, err = loginAction.CheckLoginStatus(actionCtx)
		return err
	})
	if err != nil {
		return nil, err
	}

	response := &LoginStatusResponse{
		IsLoggedIn: isLoggedIn,
		Username:   configs.Username,
	}

	return response, nil
}

// GetLoginQrcode 获取登录的扫码二维码，失败时自动重试最多 3 次
func (s *XiaohongshuService) GetLoginQrcode(ctx context.Context) (*LoginQrcodeResponse, error) {
	const maxRetry = 3

	var (
		b           *browser.ProfileBrowser
		page        *rod.Page
		loginAction *xiaohongshu.LoginAction
		img         string
		loggedIn    bool
		err         error
	)

	closeAll := func() {
		if page != nil {
			_ = page.Close()
		}
		if b != nil {
			b.Close()
		}
		b, page = nil, nil
	}

	b = newProfileBrowser()
	page = b.NewPage()
	loginAction = xiaohongshu.NewLogin(page)
	img, loggedIn, err = loginAction.FetchQrcodeImage(ctx)

	// 失败则关闭当前浏览器，重新启动重试
	for i := 1; i < maxRetry && err != nil; i++ {
		logrus.Warnf("get_login_qrcode 第 %d 次尝试失败: %v，重试中...", i, err)
		closeAll()
		b = newProfileBrowser()
		page = b.NewPage()
		loginAction = xiaohongshu.NewLogin(page)
		img, loggedIn, err = loginAction.FetchQrcodeImage(ctx)
	}

	if err != nil || loggedIn {
		defer closeAll()
	}
	if err != nil {
		return nil, err
	}

	timeout := 4 * time.Minute

	if !loggedIn {
		go func() {
			ctxTimeout, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			defer closeAll()

			if loginAction.WaitForLogin(ctxTimeout) {
				// 扫码登录成功后，跳转到 creator 根域触发 SSO，让 profile 同时持有两个域的 session
				creatorCtx, creatorCancel := context.WithTimeout(context.Background(), 60*time.Second)
				creatorPP := page.Context(creatorCtx)
				if err := creatorPP.Navigate("https://creator.xiaohongshu.com"); err != nil {
					logrus.Warnf("访问 creator 根域失败（non-fatal）: %v", err)
				} else {
					_ = creatorPP.WaitLoad()
					time.Sleep(5 * time.Second)
					if info, err := page.Info(); err == nil {
						logrus.Infof("creator SSO 后当前 URL: %s", info.URL)
					}
				}
				creatorCancel()

				if er := saveCookies(page); er != nil {
					logrus.Errorf("failed to save cookies: %v", er)
				} else {
					logrus.Info("session 已保存（profile + cookies.json）")
				}
			}
		}()
	}

	return &LoginQrcodeResponse{
		Timeout: func() string {
			if loggedIn {
				return "0s"
			}
			return timeout.String()
		}(),
		Img:        img,
		IsLoggedIn: loggedIn,
	}, nil
}

// PublishContent 发布内容
func (s *XiaohongshuService) PublishContent(ctx context.Context, req *PublishRequest) (*PublishResponse, error) {
	// 验证标题长度（小红书限制：最大20个字）
	if xhsutil.CalcTitleLength(req.Title) > 20 {
		return nil, fmt.Errorf("标题长度超过限制")
	}

	// 处理图片：下载URL图片或使用本地路径
	imagePaths, err := s.processImages(req.Images)
	if err != nil {
		return nil, err
	}

	// 解析定时发布时间
	var scheduleTime *time.Time
	if req.ScheduleAt != "" {
		t, err := time.Parse(time.RFC3339, req.ScheduleAt)
		if err != nil {
			return nil, fmt.Errorf("定时发布时间格式错误，请使用 ISO8601 格式: %v", err)
		}

		// 校验定时发布时间范围：1小时至14天
		now := time.Now()
		minTime := now.Add(1 * time.Hour)
		maxTime := now.Add(14 * 24 * time.Hour)

		if t.Before(minTime) {
			return nil, fmt.Errorf("定时发布时间必须至少在1小时后，当前设置: %s，最早可选: %s",
				t.Format("2006-01-02 15:04"), minTime.Format("2006-01-02 15:04"))
		}
		if t.After(maxTime) {
			return nil, fmt.Errorf("定时发布时间不能超过14天，当前设置: %s，最晚可选: %s",
				t.Format("2006-01-02 15:04"), maxTime.Format("2006-01-02 15:04"))
		}

		scheduleTime = &t
		logrus.Infof("设置定时发布时间: %s", t.Format("2006-01-02 15:04"))
	}

	// 构建发布内容
	content := xiaohongshu.PublishImageContent{
		Title:        req.Title,
		Content:      req.Content,
		Tags:         req.Tags,
		ImagePaths:   imagePaths,
		ScheduleTime: scheduleTime,
		IsOriginal:   req.IsOriginal,
		Visibility:   req.Visibility,
		Products:     req.Products,
	}

	// 执行发布
	if err := s.publishContent(ctx, content); err != nil {
		logrus.Errorf("发布内容失败: title=%s %v", content.Title, err)
		return nil, err
	}

	response := &PublishResponse{
		Title:   req.Title,
		Content: req.Content,
		Images:  len(imagePaths),
		Status:  "发布完成",
	}

	return response, nil
}

// processImages 处理图片列表，支持URL下载和本地路径
func (s *XiaohongshuService) processImages(images []string) ([]string, error) {
	processor := downloader.NewImageProcessor()
	return processor.ProcessImages(images)
}

// publishContent 执行内容发布（使用 profile 目录保持 creator session）
func (s *XiaohongshuService) publishContent(ctx context.Context, content xiaohongshu.PublishImageContent) error {
	b := newProfileBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action, err := xiaohongshu.NewPublishImageAction(page)
	if err != nil {
		return err
	}

	// 执行发布
	return action.Publish(ctx, content)
}

// PublishVideo 发布视频（本地文件）
func (s *XiaohongshuService) PublishVideo(ctx context.Context, req *PublishVideoRequest) (*PublishVideoResponse, error) {
	// 标题长度校验（小红书限制：最大20个字）
	if xhsutil.CalcTitleLength(req.Title) > 20 {
		return nil, fmt.Errorf("标题长度超过限制")
	}

	// 本地视频文件校验
	if req.Video == "" {
		return nil, fmt.Errorf("必须提供本地视频文件")
	}
	if _, err := os.Stat(req.Video); err != nil {
		return nil, fmt.Errorf("视频文件不存在或不可访问: %v", err)
	}

	// 解析定时发布时间
	var scheduleTime *time.Time
	if req.ScheduleAt != "" {
		t, err := time.Parse(time.RFC3339, req.ScheduleAt)
		if err != nil {
			return nil, fmt.Errorf("定时发布时间格式错误，请使用 ISO8601 格式: %v", err)
		}

		// 校验定时发布时间范围：1小时至14天
		now := time.Now()
		minTime := now.Add(1 * time.Hour)
		maxTime := now.Add(14 * 24 * time.Hour)

		if t.Before(minTime) {
			return nil, fmt.Errorf("定时发布时间必须至少在1小时后，当前设置: %s，最早可选: %s",
				t.Format("2006-01-02 15:04"), minTime.Format("2006-01-02 15:04"))
		}
		if t.After(maxTime) {
			return nil, fmt.Errorf("定时发布时间不能超过14天，当前设置: %s，最晚可选: %s",
				t.Format("2006-01-02 15:04"), maxTime.Format("2006-01-02 15:04"))
		}

		scheduleTime = &t
		logrus.Infof("设置定时发布时间: %s", t.Format("2006-01-02 15:04"))
	}

	// 构建发布内容
	content := xiaohongshu.PublishVideoContent{
		Title:        req.Title,
		Content:      req.Content,
		Tags:         req.Tags,
		VideoPath:    req.Video,
		ScheduleTime: scheduleTime,
		Visibility:   req.Visibility,
		Products:     req.Products,
	}

	// 执行发布
	if err := s.publishVideo(ctx, content); err != nil {
		return nil, err
	}

	resp := &PublishVideoResponse{
		Title:   req.Title,
		Content: req.Content,
		Video:   req.Video,
		Status:  "发布完成",
	}
	return resp, nil
}

// publishVideo 执行视频发布（使用 profile 目录保持 creator session）
func (s *XiaohongshuService) publishVideo(ctx context.Context, content xiaohongshu.PublishVideoContent) error {
	b := newProfileBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action, err := xiaohongshu.NewPublishVideoAction(page)
	if err != nil {
		return err
	}

	return action.PublishVideo(ctx, content)
}

// ListFeeds 获取Feeds列表
func (s *XiaohongshuService) ListFeeds(ctx context.Context) (*FeedsListResponse, error) {
	var feeds []xiaohongshu.Feed
	err := withProfileBrowserPage(ctx, "list_feeds", profileReadTimeout, func(actionCtx context.Context, page *rod.Page) error {
		// 创建 Feeds 列表 action
		action := xiaohongshu.NewFeedsListAction(page)
		// 获取 Feeds 列表
		var err error
		feeds, err = action.GetFeedsList(actionCtx)
		return err
	})
	if err != nil {
		logrus.Errorf("获取 Feeds 列表失败: %v", err)
		return nil, err
	}

	response := &FeedsListResponse{
		Feeds: feeds,
		Count: len(feeds),
	}

	return response, nil
}

func (s *XiaohongshuService) SearchFeeds(ctx context.Context, keyword string, filters ...xiaohongshu.FilterOption) (*FeedsListResponse, error) {
	var feeds []xiaohongshu.Feed
	err := withProfileBrowserPage(ctx, "search_feeds", profileReadTimeout, func(actionCtx context.Context, page *rod.Page) error {
		action := xiaohongshu.NewSearchAction(page)
		var err error
		feeds, err = action.Search(actionCtx, keyword, filters...)
		return err
	})
	if err != nil {
		return nil, err
	}

	response := &FeedsListResponse{
		Feeds: feeds,
		Count: len(feeds),
	}

	return response, nil
}

// GetFeedDetail 获取Feed详情
func (s *XiaohongshuService) GetFeedDetail(ctx context.Context, feedID, xsecToken string, loadAllComments bool) (*FeedDetailResponse, error) {
	return s.GetFeedDetailWithConfig(ctx, feedID, xsecToken, loadAllComments, xiaohongshu.DefaultCommentLoadConfig())
}

// GetFeedDetailWithConfig 使用配置获取Feed详情
func (s *XiaohongshuService) GetFeedDetailWithConfig(ctx context.Context, feedID, xsecToken string, loadAllComments bool, config xiaohongshu.CommentLoadConfig) (*FeedDetailResponse, error) {
	var result *xiaohongshu.FeedDetailResponse
	err := withProfileBrowserPage(ctx, "get_feed_detail", feedDetailTimeout, func(actionCtx context.Context, page *rod.Page) error {
		// 创建 Feed 详情 action
		action := xiaohongshu.NewFeedDetailAction(page)
		// 获取 Feed 详情
		var err error
		result, err = action.GetFeedDetailWithConfig(actionCtx, feedID, xsecToken, loadAllComments, config)
		return err
	})
	if err != nil {
		return nil, err
	}

	response := &FeedDetailResponse{
		FeedID: feedID,
		Data:   result,
	}

	return response, nil
}

// UserProfile 获取用户信息
func (s *XiaohongshuService) UserProfile(ctx context.Context, userID, xsecToken string) (*UserProfileResponse, error) {
	var result *xiaohongshu.UserProfileResponse
	err := withProfileBrowserPage(ctx, "user_profile", profileReadTimeout, func(actionCtx context.Context, page *rod.Page) error {
		action := xiaohongshu.NewUserProfileAction(page)
		var err error
		result, err = action.UserProfile(actionCtx, userID, xsecToken)
		return err
	})
	if err != nil {
		return nil, err
	}
	response := &UserProfileResponse{
		UserBasicInfo: result.UserBasicInfo,
		Interactions:  result.Interactions,
		Feeds:         result.Feeds,
	}

	return response, nil

}

// PostCommentToFeed 发表评论到Feed
func (s *XiaohongshuService) PostCommentToFeed(ctx context.Context, feedID, xsecToken, content string) (*PostCommentResponse, error) {
	b := newProfileBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewCommentFeedAction(page)

	if err := action.PostComment(ctx, feedID, xsecToken, content); err != nil {
		return nil, err
	}

	return &PostCommentResponse{FeedID: feedID, Success: true, Message: "评论发表成功"}, nil
}

// LikeFeed 点赞笔记
func (s *XiaohongshuService) LikeFeed(ctx context.Context, feedID, xsecToken string) (*ActionResult, error) {
	b := newProfileBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewLikeAction(page)
	if err := action.Like(ctx, feedID, xsecToken); err != nil {
		return nil, err
	}
	return &ActionResult{FeedID: feedID, Success: true, Message: "点赞成功或已点赞"}, nil
}

// UnlikeFeed 取消点赞笔记
func (s *XiaohongshuService) UnlikeFeed(ctx context.Context, feedID, xsecToken string) (*ActionResult, error) {
	b := newProfileBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewLikeAction(page)
	if err := action.Unlike(ctx, feedID, xsecToken); err != nil {
		return nil, err
	}
	return &ActionResult{FeedID: feedID, Success: true, Message: "取消点赞成功或未点赞"}, nil
}

// FavoriteFeed 收藏笔记
func (s *XiaohongshuService) FavoriteFeed(ctx context.Context, feedID, xsecToken string) (*ActionResult, error) {
	b := newProfileBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewFavoriteAction(page)
	if err := action.Favorite(ctx, feedID, xsecToken); err != nil {
		return nil, err
	}
	return &ActionResult{FeedID: feedID, Success: true, Message: "收藏成功或已收藏"}, nil
}

// UnfavoriteFeed 取消收藏笔记
func (s *XiaohongshuService) UnfavoriteFeed(ctx context.Context, feedID, xsecToken string) (*ActionResult, error) {
	b := newProfileBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewFavoriteAction(page)
	if err := action.Unfavorite(ctx, feedID, xsecToken); err != nil {
		return nil, err
	}
	return &ActionResult{FeedID: feedID, Success: true, Message: "取消收藏成功或未收藏"}, nil
}

// ReplyCommentToFeed 回复指定评论
func (s *XiaohongshuService) ReplyCommentToFeed(ctx context.Context, feedID, xsecToken, commentID, userID, content string) (*ReplyCommentResponse, error) {
	b := newProfileBrowser()
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewCommentFeedAction(page)

	if err := action.ReplyToComment(ctx, feedID, xsecToken, commentID, userID, content); err != nil {
		return nil, err
	}

	return &ReplyCommentResponse{
		FeedID:          feedID,
		TargetCommentID: commentID,
		TargetUserID:    userID,
		Success:         true,
		Message:         "评论回复成功",
	}, nil
}

// newProfileBrowser 使用激活账号的 Chrome profile 目录创建浏览器。
// profile 跨重启持久化 cookies、LocalStorage、IndexedDB，统一用于所有操作。
func newProfileBrowser() *browser.ProfileBrowser {
	profileDir := accounts.GetManager().GetActiveProfileDir()
	return browser.NewProfileBrowser(configs.IsHeadless(), profileDir, configs.GetBinPath(), os.Getenv("XHS_PROXY"))
}

func newProfileBrowserWithContext(ctx context.Context) (*browser.ProfileBrowser, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	profileDir := accounts.GetManager().GetActiveProfileDir()
	return browser.NewProfileBrowserWithContext(ctx, configs.IsHeadless(), profileDir, configs.GetBinPath(), os.Getenv("XHS_PROXY"))
}

// newProfileBrowserForAccount 使用指定账号的 Chrome profile 目录创建浏览器（广播发布时使用）
func newProfileBrowserForAccount(accountName string) (*browser.ProfileBrowser, error) {
	profileDir, err := accounts.GetManager().GetProfileDirByName(accountName)
	if err != nil {
		return nil, err
	}
	return browser.NewProfileBrowser(configs.IsHeadless(), profileDir, configs.GetBinPath(), os.Getenv("XHS_PROXY")), nil
}

// CreatorPhoneLoginResponse creator 手机号登录响应
type CreatorPhoneLoginResponse struct {
	Screenshot string `json:"screenshot"` // base64 PNG 截图
	Message    string `json:"message"`
}

// CreatorPhoneLogin 导航到 creator 登录页并发送短信验证码，返回截图供用户确认
func (s *XiaohongshuService) CreatorPhoneLogin(phone string) (*CreatorPhoneLoginResponse, error) {
	if s.consumerLoginBrowser != nil {
		return nil, fmt.Errorf("consumer 登录会话仍在进行，请先完成或结束 consumer 登录流程")
	}
	// 关闭上一次未完成的 creator 登录浏览器
	if s.creatorLoginBrowser != nil {
		s.releaseCreatorLoginSession(s.creatorLoginBrowser, s.creatorLoginPage)
	}

	b := newProfileBrowser()
	page := b.NewPage()
	action := xiaohongshu.NewCreatorLogin(page)

	// 导航到登录页
	if _, err := action.NavigateToLogin(); err != nil {
		page.Close()
		b.Close()
		return nil, err
	}

	// 发送验证码
	otpResult, err := action.SendOTP(phone)
	if !s.retainCreatorLoginSession(b, page, otpResult, err) {
		page.Close()
		b.Close()
		if otpResult == nil {
			if err == nil {
				err = fmt.Errorf("验证码发送失败：未返回有效发送状态")
			}
			return nil, err
		}
		if err == nil {
			err = fmt.Errorf("验证码发送失败：状态 %q 不允许保留登录会话", otpResult.Status)
		}
		return &CreatorPhoneLoginResponse{
			Screenshot: fmt.Sprintf("data:image/png;base64,%s", encodeBase64(otpResult.Screenshot)),
			Message:    otpResult.Message,
		}, err
	}

	return &CreatorPhoneLoginResponse{
		Screenshot: fmt.Sprintf("data:image/png;base64,%s", encodeBase64(otpResult.Screenshot)),
		Message:    otpResult.Message,
	}, nil
}

// ConsumerPhoneLoginResponse consumer www 手机号登录响应。
type ConsumerPhoneLoginResponse struct {
	Screenshot string                    `json:"screenshot"`
	Message    string                    `json:"message"`
	Status     xiaohongshu.OTPSendStatus `json:"status"`
}

// ConsumerPhoneLogin 在 www 消费端登录弹窗中发送短信验证码。
// 浏览器/page 会保留到 ConsumerVerifyOTP 或 ConsumerCompleteSecurityVerification。
func (s *XiaohongshuService) ConsumerPhoneLogin(phone string) (*ConsumerPhoneLoginResponse, error) {
	if s.creatorLoginBrowser != nil {
		return nil, fmt.Errorf("creator 登录会话仍在进行，请先完成或结束 creator 登录流程")
	}
	if s.consumerLoginBrowser != nil {
		s.releaseConsumerLoginSession(s.consumerLoginBrowser, s.consumerLoginPage)
	}

	b := newProfileBrowser()
	page := b.NewPage()
	action := xiaohongshu.NewConsumerLogin(page)
	if _, err := action.NavigateToLogin(); err != nil {
		_ = page.Close()
		b.Close()
		return nil, err
	}

	otpResult, err := action.SendOTP(phone)
	if err != nil || otpResult == nil ||
		(otpResult.Status != xiaohongshu.OTPSendConfirmed && otpResult.Status != xiaohongshu.OTPSendUncertain) {
		_ = page.Close()
		b.Close()
		if otpResult == nil {
			if err == nil {
				err = fmt.Errorf("consumer 验证码发送失败：未返回有效发送状态")
			}
			return nil, err
		}
		if err == nil {
			err = fmt.Errorf("consumer 验证码发送失败：状态 %q 不允许保留登录会话", otpResult.Status)
		}
		return &ConsumerPhoneLoginResponse{
			Screenshot: fmt.Sprintf("data:image/png;base64,%s", encodeBase64(otpResult.Screenshot)),
			Message:    otpResult.Message,
			Status:     otpResult.Status,
		}, err
	}

	s.consumerLoginBrowser = b
	s.consumerLoginPage = page
	return &ConsumerPhoneLoginResponse{
		Screenshot: fmt.Sprintf("data:image/png;base64,%s", encodeBase64(otpResult.Screenshot)),
		Message:    otpResult.Message,
		Status:     otpResult.Status,
	}, nil
}

type ConsumerVerifyOTPResult struct {
	Status         xiaohongshu.OTPVerificationStatus `json:"status"`
	SecurityQRShot []byte                            `json:"-"`
}

// ConsumerVerifyOTP 提交 www 消费端验证码；OTP 仅在本次内存调用中传递。
func (s *XiaohongshuService) ConsumerVerifyOTP(otp string) (*ConsumerVerifyOTPResult, error) {
	if s.consumerLoginPage == nil || s.consumerLoginBrowser == nil {
		return nil, fmt.Errorf("请先调用 consumer_phone_login 发送验证码")
	}

	b := s.consumerLoginBrowser
	page := s.consumerLoginPage
	verification, err := xiaohongshu.NewConsumerLogin(page).VerifyOTP(otp)
	if err != nil {
		s.releaseConsumerLoginSession(b, page)
		return nil, err
	}
	if verification == nil {
		s.releaseConsumerLoginSession(b, page)
		return nil, fmt.Errorf("consumer 验证码登录未返回有效状态")
	}
	if verification.Status == xiaohongshu.OTPVerificationSecurityVerificationNeeded {
		return &ConsumerVerifyOTPResult{
			Status:         verification.Status,
			SecurityQRShot: verification.SecurityVerificationQR,
		}, nil
	}

	if err := s.finalizeConsumerLogin(page); err != nil {
		s.releaseConsumerLoginSession(b, page)
		return nil, err
	}
	s.releaseConsumerLoginSession(b, page)
	return &ConsumerVerifyOTPResult{Status: xiaohongshu.OTPVerificationSucceeded}, nil
}

// ConsumerCompleteSecurityVerification 完成 www 消费端手机号登录的二次验证。
func (s *XiaohongshuService) ConsumerCompleteSecurityVerification() (*ConsumerVerifyOTPResult, error) {
	if s.consumerLoginPage == nil || s.consumerLoginBrowser == nil {
		return nil, fmt.Errorf("没有活跃的 consumer 登录会话，请先调用 consumer_phone_login 和 consumer_verify_otp")
	}

	b := s.consumerLoginBrowser
	page := s.consumerLoginPage
	if err := xiaohongshu.NewConsumerLogin(page).WaitForSecurityVerification(120 * time.Second); err != nil {
		s.releaseConsumerLoginSession(b, page)
		return nil, err
	}
	if err := s.finalizeConsumerLogin(page); err != nil {
		s.releaseConsumerLoginSession(b, page)
		return nil, err
	}
	s.releaseConsumerLoginSession(b, page)
	return &ConsumerVerifyOTPResult{Status: xiaohongshu.OTPVerificationSucceeded}, nil
}

func (s *XiaohongshuService) finalizeConsumerLogin(page *rod.Page) error {
	if page == nil {
		return fmt.Errorf("consumer 登录页面不可用")
	}
	consumerCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pp := page.Context(consumerCtx)
	if err := pp.WaitLoad(); err != nil {
		logrus.Warnf("www consumer 登录后 WaitLoad 未完成，继续验收页面状态: %v", err)
	}
	time.Sleep(2 * time.Second)

	if err := xiaohongshu.LogConsumerCookieMetadata(page, "consumer finalize"); err != nil {
		return fmt.Errorf("consumer cookie metadata unavailable: %w", err)
	}
	status, err := xiaohongshu.ReadConsumerCookieStatus(page)
	if err != nil {
		return fmt.Errorf("consumer cookie status unavailable: %w", err)
	}
	gate, err := xiaohongshu.ReadConsumerAuthGate(pp)
	if err != nil {
		return fmt.Errorf("consumer auth state unavailable: %w", err)
	}
	if gate.Present {
		return fmt.Errorf("consumer session not established: authentication gate visible (%s)", gate.Description())
	}
	if !status.HasValidConsumerSession() {
		return fmt.Errorf("consumer session not established: %s", status.Summary())
	}
	evidence, err := xiaohongshu.WaitForConsumerLoginEvidence(pp, 5*time.Second)
	if err != nil {
		return fmt.Errorf("consumer login evidence unavailable: %w", err)
	}
	if !evidence.Present {
		return fmt.Errorf("consumer session not established: no positive consumer login evidence")
	}
	if err := saveCookies(page); err != nil {
		return fmt.Errorf("consumer 登录后保存 cookies 失败: %w", err)
	}
	logrus.Info("consumer session 已通过正向页面验收并保存到 profile 及 cookies.json")
	return nil
}

func (s *XiaohongshuService) releaseConsumerLoginSession(b *browser.ProfileBrowser, page *rod.Page) {
	if s.consumerLoginBrowser == b {
		s.consumerLoginBrowser = nil
	}
	if s.consumerLoginPage == page {
		s.consumerLoginPage = nil
	}
	if page != nil {
		_ = page.Close()
	}
	if b != nil {
		b.Close()
	}
}

func (s *XiaohongshuService) retainCreatorLoginSession(b *browser.ProfileBrowser, page *rod.Page, result *xiaohongshu.OTPSendResult, sendErr error) bool {
	if !shouldRetainCreatorLoginSession(result, sendErr) {
		return false
	}
	// 保存状态，等待 VerifyOTP 调用。confirmed 和 uncertain 都需要保留。
	s.creatorLoginBrowser = b
	s.creatorLoginPage = page
	return true
}

func shouldRetainCreatorLoginSession(result *xiaohongshu.OTPSendResult, sendErr error) bool {
	if sendErr != nil || result == nil {
		return false
	}
	return result.Status == xiaohongshu.OTPSendConfirmed || result.Status == xiaohongshu.OTPSendUncertain
}

// CreatorVerifyOTPResult creator 验证码登录结果
type CreatorVerifyOTPResult struct {
	Status xiaohongshu.OTPVerificationStatus `json:"status"`
	// SecurityQRShot 非 nil 时表示小红书弹出了安全验证弹窗，等待用户扫码。
	SecurityQRShot []byte
}

// CreatorVerifyOTP 填写验证码完成 creator 登录
func (s *XiaohongshuService) CreatorVerifyOTP(otp string) (*CreatorVerifyOTPResult, error) {
	if s.creatorLoginPage == nil || s.creatorLoginBrowser == nil {
		return nil, fmt.Errorf("请先调用 creator_phone_login 发送验证码")
	}

	b := s.creatorLoginBrowser
	page := s.creatorLoginPage
	verification, err := s.verifyCreatorOTP(page, otp)
	if err != nil {
		s.releaseCreatorLoginSession(b, page)
		return nil, err
	}
	if verification == nil {
		s.releaseCreatorLoginSession(b, page)
		return nil, fmt.Errorf("creator 验证码登录未返回有效状态")
	}
	if verification.Status == xiaohongshu.OTPVerificationSecurityVerificationNeeded {
		// 安全验证是可继续的中间状态：保留 browser/page 给下一次 MCP 调用。
		return &CreatorVerifyOTPResult{
			Status:         verification.Status,
			SecurityQRShot: verification.SecurityVerificationQR,
		}, nil
	}

	if err := s.finalizeCreatorLogin(page); err != nil {
		s.releaseCreatorLoginSession(b, page)
		return nil, err
	}
	s.releaseCreatorLoginSession(b, page)
	return &CreatorVerifyOTPResult{Status: xiaohongshu.OTPVerificationSucceeded}, nil
}

// CreatorCompleteSecurityVerification 等待安全验证扫码完成，并复用 creator 登录成功收尾逻辑。
func (s *XiaohongshuService) CreatorCompleteSecurityVerification() (*CreatorVerifyOTPResult, error) {
	if s.creatorLoginPage == nil || s.creatorLoginBrowser == nil {
		return nil, fmt.Errorf("没有活跃的 creator 登录会话，请先调用 creator_phone_login 和 creator_verify_otp")
	}

	b := s.creatorLoginBrowser
	page := s.creatorLoginPage
	if err := s.waitForCreatorSecurityVerification(page, 120*time.Second); err != nil {
		logrus.Warnf("creator 安全验证未完成，清理临时登录会话: %v", err)
		s.releaseCreatorLoginSession(b, page)
		return nil, err
	}
	if err := s.finalizeCreatorLogin(page); err != nil {
		s.releaseCreatorLoginSession(b, page)
		return nil, err
	}
	s.releaseCreatorLoginSession(b, page)
	return &CreatorVerifyOTPResult{Status: xiaohongshu.OTPVerificationSucceeded}, nil
}

func (s *XiaohongshuService) verifyCreatorOTP(page *rod.Page, otp string) (*xiaohongshu.OTPVerificationResult, error) {
	if s.creatorVerifyOTPFunc != nil {
		return s.creatorVerifyOTPFunc(page, otp)
	}
	return xiaohongshu.NewCreatorLogin(page).VerifyOTP(otp)
}

func (s *XiaohongshuService) waitForCreatorSecurityVerification(page *rod.Page, timeout time.Duration) error {
	if s.creatorWaitSecurityVerifyFunc != nil {
		return s.creatorWaitSecurityVerifyFunc(page, timeout)
	}
	return xiaohongshu.NewCreatorLogin(page).WaitForSecurityVerification(timeout)
}

// finalizeCreatorLogin 是 creator 登录成功后的唯一收尾路径。
// 它负责 SSO、cookies 保存；ProfileBrowser 关闭由 releaseCreatorLoginSession 统一处理。
func (s *XiaohongshuService) finalizeCreatorLogin(page *rod.Page) error {
	if s.creatorFinalizeLoginFunc != nil {
		return s.creatorFinalizeLoginFunc(page)
	}

	// 登录成功后进入 www 的 canonical consumer document，触发 SSO，让
	// www session 写入 profile。不能只把“导航发出”或“cookie 已保存”当成
	// SSO 成功：creator 与 www 可能分别处于不同的认证状态。
	wwwCtx, wwwCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer wwwCancel()
	wwwPP := page.Context(wwwCtx)
	if err := wwwPP.Navigate("https://www.xiaohongshu.com/explore"); err != nil {
		return fmt.Errorf("www consumer SSO navigation failed: %w", err)
	}
	if err := wwwPP.WaitLoad(); err != nil {
		// 页面可能已有可用 document，但资源仍未全部结束；后面的
		// cookie/DOM 验收会决定是否真正成功。
		logrus.Warnf("www consumer SSO WaitLoad 未完成，继续验收页面状态: %v", err)
	}
	time.Sleep(3 * time.Second)

	if info, err := wwwPP.Info(); err == nil {
		logrus.Infof("www consumer SSO 页面 URL: %s", info.URL)
	}
	if err := xiaohongshu.LogConsumerCookieMetadata(page, "creator finalize"); err != nil {
		return fmt.Errorf("www consumer cookie metadata unavailable: %w", err)
	}
	cookieStatus, err := xiaohongshu.ReadConsumerCookieStatus(page)
	if err != nil {
		return fmt.Errorf("www consumer cookie status unavailable: %w", err)
	}
	gate, err := xiaohongshu.ReadConsumerAuthGate(wwwPP)
	if err != nil {
		return fmt.Errorf("www consumer auth state unavailable: %w", err)
	}
	if gate.Present {
		return fmt.Errorf("www consumer session rejected: authentication gate visible (%s)", gate.Description())
	}
	if !cookieStatus.HasValidConsumerSession() {
		return fmt.Errorf("www consumer session not established: %s", cookieStatus.Summary())
	}
	evidence, err := xiaohongshu.WaitForConsumerLoginEvidence(wwwPP, 5*time.Second)
	if err != nil {
		return fmt.Errorf("www consumer login evidence unavailable: %w", err)
	}
	if !evidence.Present {
		return fmt.Errorf("www consumer session not established: no positive consumer login evidence")
	}

	// 同时保存 cookies 到 JSON（向后兼容 www 操作的 CDP 注入）
	if err := saveCookies(page); err != nil {
		return fmt.Errorf("creator 登录后保存 cookies 失败: %w", err)
	}
	logrus.Info("creator + www session 已通过消费端验收并保存到 profile 及 cookies.json")
	return nil
}

func (s *XiaohongshuService) releaseCreatorLoginSession(b *browser.ProfileBrowser, page *rod.Page) {
	if s.creatorLoginBrowser == b {
		s.creatorLoginBrowser = nil
	}
	if s.creatorLoginPage == page {
		s.creatorLoginPage = nil
	}
	if s.creatorCloseLoginSessionFunc != nil {
		s.creatorCloseLoginSessionFunc(b, page)
		return
	}
	if page != nil {
		_ = page.Close()
	}
	if b != nil {
		b.Close()
	}
}

// encodeBase64 将字节切片编码为 base64 字符串
func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// saveCookies 将当前浏览器 cookies 保存到激活账号的 cookies 文件
func saveCookies(page *rod.Page) error {
	cks, err := page.Browser().GetCookies()
	if err != nil {
		return err
	}
	data, err := json.Marshal(cks)
	if err != nil {
		return err
	}
	cookiePath := accounts.GetManager().GetActiveCookiesPath()
	return cookies.NewLoadCookie(cookiePath).SaveCookies(data)
}

const (
	profileReadTimeout = 2 * time.Minute
	feedDetailTimeout  = 4 * time.Minute
)

var (
	profileBrowserPageFactory = func(ctx context.Context) (*browser.ProfileBrowser, *rod.Page, error) {
		b, err := newProfileBrowserWithContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		page, err := b.NewPageWithContext(ctx)
		if err != nil {
			b.Close()
			return nil, nil, err
		}
		return b, page, nil
	}
	profilePageCloser    = closeProfilePage
	profileBrowserCloser = func(b *browser.ProfileBrowser) { b.Close() }
)

// withProfileBrowserPage 为读取型 profile browser 提供统一的 deadline、panic
// 转 error 和 best-effort cleanup。cleanup 使用独立 context，避免操作 context
// 取消后关闭页面/浏览器也被一并取消。
func withProfileBrowserPage(ctx context.Context, operation string, timeout time.Duration, fn func(context.Context, *rod.Page) error) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	opCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	logrus.Infof("%s: browser create start", operation)
	b, page, err := profileBrowserPageFactory(opCtx)
	if err != nil {
		if b != nil {
			profileBrowserCloser(b)
		}
		if opCtx.Err() != nil {
			return fmt.Errorf("%s browser create: %w", operation, opCtx.Err())
		}
		return fmt.Errorf("%s browser create: %w", operation, err)
	}
	logrus.Infof("%s: browser created", operation)
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%s panic: %v", operation, recovered)
			logrus.Errorf("%s: panic converted to error: %v", operation, recovered)
		}
		logrus.Infof("%s: cleanup start", operation)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if closeErr := profilePageCloser(page, cleanupCtx); closeErr != nil {
			logrus.Warnf("%s: page cleanup failed: %v", operation, closeErr)
		}
		cleanupCancel()
		profileBrowserCloser(b)
		logrus.Infof("%s: cleanup end", operation)
	}()

	err = fn(opCtx, page)
	if err == nil && opCtx.Err() != nil {
		err = fmt.Errorf("%s: %w", operation, opCtx.Err())
	}
	return err
}

func closeProfilePage(page *rod.Page, ctx context.Context) (err error) {
	if page == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("close Chrome page panic: %v", recovered)
		}
	}()
	return page.Context(ctx).Close()
}

// GetMyProfile 获取当前登录用户的个人信息
func (s *XiaohongshuService) GetMyProfile(ctx context.Context) (*UserProfileResponse, error) {
	var result *xiaohongshu.UserProfileResponse
	var err error

	err = withProfileBrowserPage(ctx, "get_my_profile", profileReadTimeout, func(actionCtx context.Context, page *rod.Page) error {
		action := xiaohongshu.NewUserProfileAction(page)
		result, err = action.GetMyProfileViaSidebar(actionCtx)
		return err
	})

	if err != nil {
		return nil, err
	}

	response := &UserProfileResponse{
		UserBasicInfo: result.UserBasicInfo,
		Interactions:  result.Interactions,
		Feeds:         result.Feeds,
	}

	return response, nil
}

// ===== 账号管理 =====

// AccountInfo 账号信息响应
type AccountInfo struct {
	Name        string    `json:"name"`
	CookiesFile string    `json:"cookies_file"`
	CreatedAt   time.Time `json:"created_at"`
	LastUsed    time.Time `json:"last_used"`
	IsActive    bool      `json:"is_active"`
}

// ListAccountsResponse 账号列表响应
type ListAccountsResponse struct {
	Accounts      []*AccountInfo `json:"accounts"`
	ActiveAccount string         `json:"active_account"`
}

// ListAccounts 列出所有账号
func (s *XiaohongshuService) ListAccounts() *ListAccountsResponse {
	mgr := accounts.GetManager()
	list, active := mgr.ListAccounts()

	infos := make([]*AccountInfo, 0, len(list))
	for _, acc := range list {
		infos = append(infos, &AccountInfo{
			Name:        acc.Name,
			CookiesFile: acc.CookiesFile,
			CreatedAt:   acc.CreatedAt,
			LastUsed:    acc.LastUsed,
			IsActive:    acc.Name == active,
		})
	}
	return &ListAccountsResponse{
		Accounts:      infos,
		ActiveAccount: active,
	}
}

// SwitchAccount 切换激活账号
func (s *XiaohongshuService) SwitchAccount(name string) error {
	return accounts.GetManager().SwitchAccount(name)
}

// AddAccount 添加新账号（自动切换为激活账号，需要随后扫码登录）
func (s *XiaohongshuService) AddAccount(name string) (string, error) {
	return accounts.GetManager().AddAccount(name)
}

// RemoveAccount 删除账号
func (s *XiaohongshuService) RemoveAccount(name string) error {
	return accounts.GetManager().RemoveAccount(name)
}

// ===== 广播发布 =====

// BroadcastResult 单个账号广播发布结果
type BroadcastResult struct {
	AccountName string `json:"account_name"`
	Success     bool   `json:"success"`
	Message     string `json:"message,omitempty"`
	Error       string `json:"error,omitempty"`
}

// BroadcastPublishResponse 广播发布响应
type BroadcastPublishResponse struct {
	Results      []*BroadcastResult `json:"results"`
	SuccessCount int                `json:"success_count"`
	FailCount    int                `json:"fail_count"`
}

// BroadcastPublish 向多个账号广播发布图文内容
func (s *XiaohongshuService) BroadcastPublish(ctx context.Context, accountNames []string, req *PublishRequest) (*BroadcastPublishResponse, error) {
	if len(accountNames) == 0 {
		return nil, fmt.Errorf("账号列表不能为空")
	}

	// 处理图片（只下载一次）
	imagePaths, err := s.processImages(req.Images)
	if err != nil {
		return nil, err
	}

	// 标题长度校验
	if xhsutil.CalcTitleLength(req.Title) > 20 {
		return nil, fmt.Errorf("标题长度超过限制")
	}

	// 解析定时发布时间
	var scheduleTime *time.Time
	if req.ScheduleAt != "" {
		t, err := time.Parse(time.RFC3339, req.ScheduleAt)
		if err != nil {
			return nil, fmt.Errorf("定时发布时间格式错误，请使用 ISO8601 格式: %v", err)
		}
		now := time.Now()
		if t.Before(now.Add(1 * time.Hour)) {
			return nil, fmt.Errorf("定时发布时间必须至少在1小时后")
		}
		if t.After(now.Add(14 * 24 * time.Hour)) {
			return nil, fmt.Errorf("定时发布时间不能超过14天")
		}
		scheduleTime = &t
	}

	content := xiaohongshu.PublishImageContent{
		Title:        req.Title,
		Content:      req.Content,
		Tags:         req.Tags,
		ImagePaths:   imagePaths,
		ScheduleTime: scheduleTime,
		IsOriginal:   req.IsOriginal,
		Visibility:   req.Visibility,
		Products:     req.Products,
	}

	resp := &BroadcastPublishResponse{}
	// 逐账号顺序发布
	for _, accName := range accountNames {
		result := &BroadcastResult{AccountName: accName}

		b, err := newProfileBrowserForAccount(accName)
		if err != nil {
			result.Success = false
			result.Error = err.Error()
			resp.Results = append(resp.Results, result)
			resp.FailCount++
			logrus.Warnf("广播发布: 账号 %q 创建浏览器失败: %v", accName, err)
			continue
		}

		page := b.NewPage()
		action, err := xiaohongshu.NewPublishImageAction(page)
		if err != nil {
			page.Close()
			b.Close()
			result.Success = false
			result.Error = err.Error()
			resp.Results = append(resp.Results, result)
			resp.FailCount++
			logrus.Warnf("广播发布: 账号 %q 初始化发布页面失败: %v", accName, err)
			continue
		}

		if err := action.Publish(ctx, content); err != nil {
			result.Success = false
			result.Error = err.Error()
			resp.FailCount++
			logrus.Warnf("广播发布: 账号 %q 发布失败: %v", accName, err)
		} else {
			result.Success = true
			result.Message = "发布成功"
			resp.SuccessCount++
			logrus.Infof("广播发布: 账号 %q 发布成功", accName)
		}

		page.Close()
		b.Close()
		resp.Results = append(resp.Results, result)
	}

	return resp, nil
}
