package xiaohongshu

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/lisiyuan/xiaohongshu-mcp-pro/errors"
	"github.com/sirupsen/logrus"
)

type SearchResult struct {
	Search struct {
		Feeds FeedsValue `json:"feeds"`
	} `json:"search"`
}

// FilterOption 筛选选项结构体
type FilterOption struct {
	SortBy      string `json:"sort_by,omitempty" jsonschema:"排序依据: 综合|最新|最多点赞|最多评论|最多收藏,默认为'综合'"`
	NoteType    string `json:"note_type,omitempty" jsonschema:"笔记类型: 不限|视频|图文,默认为'不限'"`
	PublishTime string `json:"publish_time,omitempty" jsonschema:"发布时间: 不限|一天内|一周内|半年内,默认为'不限'"`
	SearchScope string `json:"search_scope,omitempty" jsonschema:"搜索范围: 不限|已看过|未看过|已关注,默认为'不限'"`
	Location    string `json:"location,omitempty" jsonschema:"位置距离: 不限|同城|附近,默认为'不限'"`
}

// internalFilterOption 内部使用的筛选选项(基于索引)
type internalFilterOption struct {
	FiltersIndex int    // 筛选组索引
	TagsIndex    int    // 标签索引
	Text         string // 标签文本描述
}

// 预定义的筛选选项映射表（内部使用）
var filterOptionsMap = map[int][]internalFilterOption{
	1: { // 排序依据
		{FiltersIndex: 1, TagsIndex: 1, Text: "综合"},
		{FiltersIndex: 1, TagsIndex: 2, Text: "最新"},
		{FiltersIndex: 1, TagsIndex: 3, Text: "最多点赞"},
		{FiltersIndex: 1, TagsIndex: 4, Text: "最多评论"},
		{FiltersIndex: 1, TagsIndex: 5, Text: "最多收藏"},
	},
	2: { // 笔记类型
		{FiltersIndex: 2, TagsIndex: 1, Text: "不限"},
		{FiltersIndex: 2, TagsIndex: 2, Text: "视频"},
		{FiltersIndex: 2, TagsIndex: 3, Text: "图文"},
	},
	3: { // 发布时间
		{FiltersIndex: 3, TagsIndex: 1, Text: "不限"},
		{FiltersIndex: 3, TagsIndex: 2, Text: "一天内"},
		{FiltersIndex: 3, TagsIndex: 3, Text: "一周内"},
		{FiltersIndex: 3, TagsIndex: 4, Text: "半年内"},
	},
	4: { // 搜索范围
		{FiltersIndex: 4, TagsIndex: 1, Text: "不限"},
		{FiltersIndex: 4, TagsIndex: 2, Text: "已看过"},
		{FiltersIndex: 4, TagsIndex: 3, Text: "未看过"},
		{FiltersIndex: 4, TagsIndex: 4, Text: "已关注"},
	},
	5: { // 位置距离
		{FiltersIndex: 5, TagsIndex: 1, Text: "不限"},
		{FiltersIndex: 5, TagsIndex: 2, Text: "同城"},
		{FiltersIndex: 5, TagsIndex: 3, Text: "附近"},
	},
}

// convertToInternalFilters 将 FilterOption 转换为内部的 internalFilterOption 列表
func convertToInternalFilters(filter FilterOption) ([]internalFilterOption, error) {
	var internalFilters []internalFilterOption

	// 处理排序依据
	if filter.SortBy != "" {
		internal, err := findInternalOption(1, filter.SortBy)
		if err != nil {
			return nil, fmt.Errorf("排序依据错误: %w", err)
		}
		internalFilters = append(internalFilters, internal)
	}

	// 处理笔记类型
	if filter.NoteType != "" {
		internal, err := findInternalOption(2, filter.NoteType)
		if err != nil {
			return nil, fmt.Errorf("笔记类型错误: %w", err)
		}
		internalFilters = append(internalFilters, internal)
	}

	// 处理发布时间
	if filter.PublishTime != "" {
		internal, err := findInternalOption(3, filter.PublishTime)
		if err != nil {
			return nil, fmt.Errorf("发布时间错误: %w", err)
		}
		internalFilters = append(internalFilters, internal)
	}

	// 处理搜索范围
	if filter.SearchScope != "" {
		internal, err := findInternalOption(4, filter.SearchScope)
		if err != nil {
			return nil, fmt.Errorf("搜索范围错误: %w", err)
		}
		internalFilters = append(internalFilters, internal)
	}

	// 处理位置距离
	if filter.Location != "" {
		internal, err := findInternalOption(5, filter.Location)
		if err != nil {
			return nil, fmt.Errorf("位置距离错误: %w", err)
		}
		internalFilters = append(internalFilters, internal)
	}

	return internalFilters, nil
}

// findInternalOption 根据筛选组索引和文本查找内部筛选选项
func findInternalOption(filtersIndex int, text string) (internalFilterOption, error) {
	options, exists := filterOptionsMap[filtersIndex]
	if !exists {
		return internalFilterOption{}, fmt.Errorf("筛选组 %d 不存在", filtersIndex)
	}

	for _, option := range options {
		if option.Text == text {
			return option, nil
		}
	}

	return internalFilterOption{}, fmt.Errorf("在筛选组 %d 中未找到文本 '%s'", filtersIndex, text)
}

func collectInternalFilters(filters ...FilterOption) ([]internalFilterOption, error) {
	var allInternalFilters []internalFilterOption
	for _, filter := range filters {
		internalFilters, err := convertToInternalFilters(filter)
		if err != nil {
			return nil, fmt.Errorf("筛选选项转换失败: %w", err)
		}
		allInternalFilters = append(allInternalFilters, internalFilters...)
	}
	return allInternalFilters, nil
}

// validateInternalFilterOption 验证内部筛选选项是否在有效范围内
func validateInternalFilterOption(filter internalFilterOption) error {
	// 检查筛选组索引是否有效
	if filter.FiltersIndex < 1 || filter.FiltersIndex > 5 {
		return fmt.Errorf("无效的筛选组索引 %d，有效范围为 1-5", filter.FiltersIndex)
	}

	// 检查标签索引是否在对应筛选组的有效范围内
	options, exists := filterOptionsMap[filter.FiltersIndex]
	if !exists {
		return fmt.Errorf("筛选组 %d 不存在", filter.FiltersIndex)
	}

	if filter.TagsIndex < 1 || filter.TagsIndex > len(options) {
		return fmt.Errorf("筛选组 %d 的标签索引 %d 超出范围，有效范围为 1-%d",
			filter.FiltersIndex, filter.TagsIndex, len(options))
	}

	return nil
}

type SearchAction struct {
	page *rod.Page
}

func NewSearchAction(page *rod.Page) *SearchAction {
	return &SearchAction{page: page}
}

const (
	searchNavigateTimeout      = 15 * time.Second
	searchResultReadyTimeout   = 40 * time.Second
	searchTargetURLTimeout     = 800 * time.Millisecond
	searchTraceSnapshotTimeout = 500 * time.Millisecond
)

var searchNavigationTraceCounter uint64

type searchNavigationTrace struct {
	traceID     string
	mainFrameID proto.PageFrameID

	initialFrameCaptured bool
	initialFrameID       proto.PageFrameID
	initialURL           string
	initialLoaderID      proto.NetworkLoaderID

	documentRequestIDs map[proto.NetworkRequestID]struct{}

	mainDocumentRequests               int
	mainDocumentResponses              int
	mainDocumentLoadingFailures        int
	mainFrameNavigations               int
	mainFrameLifecycleEvents           int
	mainExecutionContextCreated        bool
	mainDefaultExecutionContextCreated bool
	mainDocumentRequestURLs            []string
	mainDocumentResponseStatuses       []int
	mainDocumentFailureTexts           []string
	mainFrameNavigationURLs            []string
	mainFrameNavigationLoaderIDs       []proto.NetworkLoaderID
	mainFrameLifecycleNames            []proto.PageLifecycleEventName

	cancel   context.CancelFunc
	done     chan struct{}
	stopOnce sync.Once
}

func nextSearchNavigationTraceID() string {
	return fmt.Sprintf("search-nav-%d", atomic.AddUint64(&searchNavigationTraceCounter, 1))
}

func newSearchNavigationTraceController(traceID string, cancel context.CancelFunc, wait func()) *searchNavigationTrace {
	trace := &searchNavigationTrace{
		traceID:            traceID,
		documentRequestIDs: make(map[proto.NetworkRequestID]struct{}),
		cancel:             cancel,
		done:               make(chan struct{}),
	}
	trace.startListener(wait)
	return trace
}

func (t *searchNavigationTrace) startListener(wait func()) {
	if t == nil {
		return
	}
	if t.done == nil {
		t.done = make(chan struct{})
	}
	go func() {
		defer close(t.done)
		defer func() {
			if recovered := recover(); recovered != nil {
				t.warnf("event listener panic recovered: %v", recovered)
			}
		}()
		wait()
	}()
}

func (t *searchNavigationTrace) infof(format string, args ...interface{}) {
	if t == nil {
		return
	}
	values := append([]interface{}{t.traceID}, args...)
	logrus.Infof("search_feeds: navigation trace_id=%s "+format, values...)
}

func (t *searchNavigationTrace) warnf(format string, args ...interface{}) {
	if t == nil {
		return
	}
	values := append([]interface{}{t.traceID}, args...)
	logrus.Warnf("search_feeds: navigation trace_id=%s "+format, values...)
}

func (t *searchNavigationTrace) Stop() {
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
		t.infof("trace stopped main_document_requests=%d main_document_responses=%d main_document_loading_failures=%d main_frame_navigations=%d main_frame_lifecycle_events=%d main_execution_context_created=%t main_default_execution_context_created=%t",
			t.mainDocumentRequests, t.mainDocumentResponses, t.mainDocumentLoadingFailures,
			t.mainFrameNavigations, t.mainFrameLifecycleEvents,
			t.mainExecutionContextCreated, t.mainDefaultExecutionContextCreated)
	})
}

func (t *searchNavigationTrace) isMainFrame(frameID proto.PageFrameID) bool {
	return t != nil && frameID != "" && frameID == t.mainFrameID
}

func (t *searchNavigationTrace) handleRequestWillBeSent(event *proto.NetworkRequestWillBeSent) {
	if event == nil || event.Type != proto.NetworkResourceTypeDocument || !t.isMainFrame(event.FrameID) || event.Request == nil {
		return
	}
	t.documentRequestIDs[event.RequestID] = struct{}{}
	t.mainDocumentRequests++
	t.mainDocumentRequestURLs = append(t.mainDocumentRequestURLs, event.Request.URL)
	t.infof("main document request url=%q method=%s frame_id=%s loader_id=%s document_url=%q timestamp=%v",
		event.Request.URL, event.Request.Method, event.FrameID, event.LoaderID, event.DocumentURL, event.Timestamp)
}

func (t *searchNavigationTrace) handleResponseReceived(event *proto.NetworkResponseReceived) {
	if event == nil || event.Type != proto.NetworkResourceTypeDocument || !t.isMainFrame(event.FrameID) || event.Response == nil {
		return
	}
	t.mainDocumentResponses++
	t.mainDocumentResponseStatuses = append(t.mainDocumentResponseStatuses, event.Response.Status)
	t.infof("main document response url=%q status=%d status_text=%q protocol=%s mime_type=%s frame_id=%s loader_id=%s from_disk_cache=%t from_service_worker=%t remote_ip=%s",
		event.Response.URL, event.Response.Status, event.Response.StatusText, event.Response.Protocol,
		event.Response.MIMEType, event.FrameID, event.LoaderID, event.Response.FromDiskCache,
		event.Response.FromServiceWorker, event.Response.RemoteIPAddress)
}

func (t *searchNavigationTrace) handleLoadingFailed(event *proto.NetworkLoadingFailed) {
	if event == nil || event.Type != proto.NetworkResourceTypeDocument {
		return
	}
	if _, ok := t.documentRequestIDs[event.RequestID]; !ok {
		return
	}
	t.mainDocumentLoadingFailures++
	t.mainDocumentFailureTexts = append(t.mainDocumentFailureTexts, event.ErrorText)
	corsError := ""
	if event.CorsErrorStatus != nil {
		corsError = fmt.Sprintf("%s:%s", event.CorsErrorStatus.CorsError, event.CorsErrorStatus.FailedParameter)
	}
	t.warnf("main document loading failed request_id=%s error_text=%q canceled=%t blocked_reason=%s cors_error=%s",
		event.RequestID, event.ErrorText, event.Canceled, event.BlockedReason, corsError)
}

func (t *searchNavigationTrace) handleFrameNavigated(event *proto.PageFrameNavigated) {
	if event == nil || event.Frame == nil || event.Frame.ParentID != "" {
		return
	}
	t.mainFrameID = event.Frame.ID
	t.mainFrameNavigations++
	t.mainFrameNavigationURLs = append(t.mainFrameNavigationURLs, event.Frame.URL)
	t.mainFrameNavigationLoaderIDs = append(t.mainFrameNavigationLoaderIDs, event.Frame.LoaderID)
	t.infof("main frame navigated frame_id=%s loader_id=%s url=%q security_origin=%q mime_type=%s",
		event.Frame.ID, event.Frame.LoaderID, event.Frame.URL, event.Frame.SecurityOrigin, event.Frame.MIMEType)
}

func (t *searchNavigationTrace) handleDOMContentEventFired(event *proto.PageDomContentEventFired) {
	if event == nil {
		return
	}
	t.infof("main document DOMContentLoaded timestamp=%v", event.Timestamp)
}

func (t *searchNavigationTrace) handleLoadEventFired(event *proto.PageLoadEventFired) {
	if event == nil {
		return
	}
	t.infof("main document load timestamp=%v", event.Timestamp)
}

func (t *searchNavigationTrace) handleLifecycleEvent(event *proto.PageLifecycleEvent) {
	if event == nil || !t.isMainFrame(event.FrameID) {
		return
	}
	t.mainFrameLifecycleEvents++
	t.mainFrameLifecycleNames = append(t.mainFrameLifecycleNames, event.Name)
	t.infof("main-frame lifecycle name=%s frame_id=%s loader_id=%s timestamp=%v",
		event.Name, event.FrameID, event.LoaderID, event.Timestamp)
}

func (t *searchNavigationTrace) handleExecutionContextCreated(event *proto.RuntimeExecutionContextCreated) {
	if event == nil || event.Context == nil {
		return
	}
	frameIDValue, ok := event.Context.AuxData["frameId"]
	if !ok || proto.PageFrameID(frameIDValue.Str()) != t.mainFrameID {
		return
	}
	defaultWorld := "unknown"
	if value, ok := event.Context.AuxData["isDefault"]; ok {
		defaultWorld = fmt.Sprintf("%t", value.Bool())
	} else if value, ok := event.Context.AuxData["type"]; ok {
		defaultWorld = fmt.Sprintf("%t", value.Str() == "default")
	}
	t.mainExecutionContextCreated = true
	if defaultWorld == "true" {
		t.mainDefaultExecutionContextCreated = true
	}
	t.infof("main frame execution context created context_id=%d frame_id=%s default_world=%s origin=%q name=%q",
		event.Context.ID, frameIDValue.Str(), defaultWorld, event.Context.Origin, event.Context.Name)
}

func startSearchNavigationTrace(ctx context.Context, page *rod.Page) (trace *searchNavigationTrace, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if page == nil {
		return nil, fmt.Errorf("page is nil")
	}

	traceCtx, cancel := context.WithCancel(ctx)
	trace = &searchNavigationTrace{
		traceID:            nextSearchNavigationTraceID(),
		mainFrameID:        page.FrameID,
		documentRequestIDs: make(map[proto.NetworkRequestID]struct{}),
		cancel:             cancel,
	}
	setupOK := false
	defer func() {
		if recovered := recover(); recovered != nil {
			cancel()
			trace = nil
			err = fmt.Errorf("navigation trace setup panic: %v", recovered)
			return
		}
		if !setupOK {
			cancel()
		}
	}()

	snapshotCtx, snapshotCancel := context.WithTimeout(ctx, searchTraceSnapshotTimeout)
	snapshot, snapshotErr := proto.PageGetFrameTree{}.Call(page.Context(snapshotCtx))
	snapshotCancel()
	if snapshotErr != nil {
		trace.warnf("initial frame snapshot unavailable: %v", snapshotErr)
	} else if snapshot != nil && snapshot.FrameTree != nil && snapshot.FrameTree.Frame != nil {
		frame := snapshot.FrameTree.Frame
		trace.initialFrameCaptured = true
		trace.initialFrameID = frame.ID
		trace.initialURL = frame.URL
		trace.initialLoaderID = frame.LoaderID
		trace.mainFrameID = frame.ID
		trace.infof("initial main frame frame_id=%s loader_id=%s url=%q", frame.ID, frame.LoaderID, frame.URL)
	}

	tracePage := page.Context(traceCtx)
	wait := tracePage.EachEvent(
		func(event *proto.NetworkRequestWillBeSent) { trace.handleRequestWillBeSent(event) },
		func(event *proto.NetworkResponseReceived) { trace.handleResponseReceived(event) },
		func(event *proto.NetworkLoadingFailed) { trace.handleLoadingFailed(event) },
		func(event *proto.PageFrameNavigated) { trace.handleFrameNavigated(event) },
		func(event *proto.PageDomContentEventFired) { trace.handleDOMContentEventFired(event) },
		func(event *proto.PageLoadEventFired) { trace.handleLoadEventFired(event) },
		func(event *proto.PageLifecycleEvent) { trace.handleLifecycleEvent(event) },
		func(event *proto.RuntimeExecutionContextCreated) { trace.handleExecutionContextCreated(event) },
	)
	trace.startListener(wait)
	setupOK = true
	trace.infof("trace started main_frame_id=%s", trace.mainFrameID)
	return trace, nil
}

func (s *SearchAction) Search(ctx context.Context, keyword string, filters ...FilterOption) (feeds []Feed, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	phase := "start"
	defer func() {
		if ctx.Err() != nil {
			logrus.Warnf("search_feeds: context ended during phase=%s error=%v", phase, ctx.Err())
		}
	}()

	// Every search stage derives a fresh page context from this outer request
	// context. In particular, a timed-out navigation context is never reused by
	// the ready or extraction stages.
	page := s.page.Context(ctx)
	logSearchPageState(page, "before navigate")

	phase = "keyword input"
	logrus.Infof("search_feeds: keyword input start")
	searchURL := makeSearchURL(keyword)
	logrus.Infof("search_feeds: keyword input end")

	navigationTrace, traceErr := startSearchNavigationTrace(ctx, page)
	if traceErr != nil {
		logrus.Warnf("search_feeds: navigation trace unavailable: %v", traceErr)
	} else {
		defer navigationTrace.Stop()
	}

	phase = "navigate/search page"
	logrus.Infof("search_feeds: navigate/search page start")
	logrus.Infof("search_feeds: submit/search trigger start")
	diagnose := func(diagnosticPage *rod.Page, stage string, original any) {
		diagnoseSearchNavigationFailureWithTrace(diagnosticPage, stage, original, navigationTrace)
	}
	if err := runSearchNavigation(ctx, page, searchURL, diagnose); err != nil {
		return nil, err
	}
	logrus.Infof("search_feeds: submit/search trigger end")
	logSearchPageState(page, "navigate/search page end")
	logrus.Infof("search_feeds: navigate/search page end")

	phase = "search result ready"
	if err := waitForSearchResultReady(ctx, page); err != nil {
		diagnoseSearchNavigationFailureWithTrace(page, "search-result-ready", err, navigationTrace)
		return nil, err
	}

	// The extraction/filter operations use a fresh clone rooted at the original
	// request context rather than the bounded navigation/ready child contexts.
	page = s.page.Context(ctx)

	// 先把外部筛选转换为真正的内部选项。一个空 FilterOption 不应仅
	// 因为 filters slice 非空就触发筛选面板交互。
	allInternalFilters, err := collectInternalFilters(filters...)
	if err != nil {
		return nil, err
	}
	if len(allInternalFilters) > 0 {
		phase = "submit/search trigger"
		logrus.Infof("search_feeds: submit/search trigger start")
		// 验证所有内部筛选选项
		for _, filter := range allInternalFilters {
			if err := validateInternalFilterOption(filter); err != nil {
				return nil, fmt.Errorf("筛选选项验证失败: %w", err)
			}
		}

		// 悬停在筛选按钮上
		filterButton := page.MustElement(`div.filter`)
		filterButton.MustHover()

		// 等待筛选面板出现
		page.MustWait(`() => document.querySelector('div.filter-panel') !== null`)

		// 应用所有筛选条件
		for _, filter := range allInternalFilters {
			selector := fmt.Sprintf(`div.filter-panel div.filters:nth-child(%d) div.tags:nth-child(%d)`,
				filter.FiltersIndex, filter.TagsIndex)
			option := page.MustElement(selector)
			option.MustClick()
		}

		// 重新等待 __INITIAL_STATE__ 更新
		if err := page.Wait(rod.Eval(`() => window.__INITIAL_STATE__ !== undefined`)); err != nil {
			return nil, fmt.Errorf("search result ready after filters failed: %w", err)
		}
		logrus.Infof("search_feeds: submit/search trigger end")
	}

	phase = "extract results"
	logrus.Infof("search_feeds: extract results start")
	result := page.MustEval(`() => {
		if (window.__INITIAL_STATE__ &&
		    window.__INITIAL_STATE__.search &&
		    window.__INITIAL_STATE__.search.feeds) {
			const feeds = window.__INITIAL_STATE__.search.feeds;
			const feedsData = feeds.value !== undefined ? feeds.value : feeds._value;
			if (feedsData) {
				return JSON.stringify(feedsData);
			}
		}
		return "";
	}`).String()

	if result == "" {
		logrus.Infof("search_feeds: extract results end count=0")
		return nil, errors.ErrNoFeeds
	}

	if err := json.Unmarshal([]byte(result), &feeds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal feeds: %w", err)
	}

	logrus.Infof("search_feeds: extract results end count=%d", len(feeds))
	return feeds, nil
}

func runSearchNavigation(ctx context.Context, page *rod.Page, searchURL string, diagnose func(*rod.Page, string, any)) error {
	return runSearchNavigationWithOps(
		ctx,
		searchURL,
		func(stageCtx context.Context, targetURL string) error {
			return page.Context(stageCtx).Navigate(targetURL)
		},
		func(diagnosticCtx context.Context) (string, error) {
			return readSearchPageURL(page, diagnosticCtx)
		},
		func(stage string, original any) {
			// Diagnostics are best-effort and must never replace the original
			// Navigate error returned to the service.
			if diagnose != nil {
				func() {
					defer func() { _ = recover() }()
					diagnose(page, stage, original)
				}()
			}
		},
	)
}

func runSearchNavigationWithOps(
	ctx context.Context,
	searchURL string,
	navigate func(context.Context, string) error,
	readCurrentURL func(context.Context) (string, error),
	diagnose func(string, any),
) error {
	if ctx == nil {
		ctx = context.Background()
	}

	navigateCtx, cancel := context.WithTimeout(ctx, searchNavigateTimeout)
	defer cancel()
	logrus.Infof("search_feeds: Navigate start")
	err := navigate(navigateCtx, searchURL)
	if err == nil {
		logrus.Infof("search_feeds: navigation command completed")
		return nil
	}

	if isSearchNavigationTimeout(navigateCtx, ctx, err) {
		diagnosticCtx, diagnosticCancel := context.WithTimeout(context.Background(), searchTargetURLTimeout)
		currentURL, urlErr := readCurrentURL(diagnosticCtx)
		diagnosticCancel()
		if urlErr == nil && isExpectedSearchURL(currentURL, searchURL) {
			logrus.Warnf("search_feeds: navigation command timed out after dispatch / target reached url=%s", currentURL)
			return nil
		}
		if urlErr != nil {
			logrus.Warnf("search_feeds: navigation target URL check unavailable: %v", urlErr)
		} else {
			logrus.Warnf("search_feeds: navigation command timed out but target was not reached current_url=%s expected_url=%s", currentURL, searchURL)
		}
	}

	if diagnose != nil {
		diagnose("Navigate", err)
	}
	if isSearchNavigationTimeout(navigateCtx, ctx, err) {
		return fmt.Errorf("navigation command timed out before the search target was reached: %w", err)
	}
	return err
}

func isSearchNavigationTimeout(stageCtx, parentCtx context.Context, err error) bool {
	if err == nil || parentCtx == nil || parentCtx.Err() != nil {
		return false
	}
	if stageCtx != nil && stageCtx.Err() == context.DeadlineExceeded {
		return true
	}
	if stderrors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "timeout")
}

func readSearchPageURL(page *rod.Page, ctx context.Context) (url string, err error) {
	if page == nil {
		return "", fmt.Errorf("page is nil")
	}
	defer func() {
		if original := recover(); original != nil {
			err = fmt.Errorf("read current page URL panic: %v", original)
		}
	}()
	diagnosticPage := page.Context(ctx)
	info, err := diagnosticPage.Info()
	if err != nil {
		return "", err
	}
	return info.URL, nil
}

func isExpectedSearchURL(currentURL, expectedURL string) bool {
	current, err := url.Parse(currentURL)
	if err != nil {
		return false
	}
	expected, err := url.Parse(expectedURL)
	if err != nil {
		return false
	}
	if !strings.EqualFold(current.Hostname(), "www.xiaohongshu.com") || current.Path != "/search_result" {
		return false
	}
	expectedKeyword := expected.Query().Get("keyword")
	currentKeyword := current.Query().Get("keyword")
	return expectedKeyword != "" && currentKeyword == expectedKeyword
}

func waitForSearchResultReady(ctx context.Context, page *rod.Page) error {
	return waitForSearchResultReadyWith(ctx, func(stageCtx context.Context) error {
		if page == nil {
			return fmt.Errorf("page is nil")
		}
		return page.Context(stageCtx).Wait(rod.Eval(`() => window.__INITIAL_STATE__ !== undefined`))
	})
}

func waitForSearchResultReadyWith(ctx context.Context, wait func(context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	readyCtx, cancel := context.WithTimeout(ctx, searchResultReadyTimeout)
	defer cancel()
	logrus.Infof("search_feeds: search result ready wait start")
	err := wait(readyCtx)
	if err != nil {
		if readyCtx.Err() == context.DeadlineExceeded || stderrors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("search result ready timeout: %w", err)
		}
		return fmt.Errorf("search result ready failed: %w", err)
	}
	logrus.Infof("search_feeds: search result ready wait end")
	return nil
}

func diagnoseSearchNavigationFailure(page *rod.Page, stage string, original any) {
	diagnoseSearchNavigationFailureWithTrace(page, stage, original, nil)
}

func diagnoseSearchNavigationFailureWithTrace(page *rod.Page, stage string, original any, trace *searchNavigationTrace) {
	diagnoseSearchNavigationFailureWithRunner(page, stage, original, runSearchPageDiagnostic)
	diagnoseSearchBrowserState(page, stage, trace)
}

func diagnoseSearchNavigationFailureWithRunner(
	page *rod.Page,
	stage string,
	original any,
	runDiagnostic func(*rod.Page, time.Duration, func(*rod.Page) error) error,
) {
	logrus.Warnf("search_feeds: navigation failed stage=%s original=%v", stage, original)
	if page == nil {
		logrus.Warnf("search_feeds: navigation diagnostics stage=%s skipped because page is nil", stage)
		return
	}

	// Each diagnostic group gets a fresh context rooted at Background. This is
	// deliberate: the action page context may already be deadline-exceeded.
	const (
		urlTitleDiagnosticTimeout = 800 * time.Millisecond
		documentDiagnosticTimeout = 1500 * time.Millisecond
	)

	if err := runDiagnostic(page, urlTitleDiagnosticTimeout, func(diagnosticPage *rod.Page) error {
		info, err := diagnosticPage.Info()
		if err != nil {
			return err
		}
		logrus.Warnf("search_feeds: navigation diagnostics stage=%s url=%s title=%s", stage, info.URL, info.Title)
		return nil
	}); err != nil {
		logrus.Warnf("search_feeds: navigation diagnostics stage=%s URL/title unavailable: %v", stage, err)
	}

	if err := runDiagnostic(page, documentDiagnosticTimeout, func(diagnosticPage *rod.Page) error {
		result, err := diagnosticPage.Eval(`() => {
			const bodyExists = document.body !== null;
			const bodyText = bodyExists ? (document.body.innerText || document.body.textContent || '') : '';
			const featureText = bodyText.slice(0, 4000);
			const host = window.location.hostname || '';
			return JSON.stringify({
				location_href: String(window.location.href || ''),
				title: String(document.title || ''),
				ready_state: String(document.readyState || ''),
				body_exists: bodyExists,
				body_text_length: bodyText.length,
				is_about_blank: window.location.href === 'about:blank',
				is_xiaohongshu: /xiaohongshu\.com$/.test(host),
				has_login_text: /登录|验证码|手机号/.test(featureText),
				has_verification_text: /安全验证|扫码|风控|验证/.test(featureText)
			});
		}`)
		if err != nil {
			return err
		}

		var state struct {
			LocationHref        string `json:"location_href"`
			Title               string `json:"title"`
			ReadyState          string `json:"ready_state"`
			BodyExists          bool   `json:"body_exists"`
			BodyTextLength      int    `json:"body_text_length"`
			IsAboutBlank        bool   `json:"is_about_blank"`
			IsXiaohongshu       bool   `json:"is_xiaohongshu"`
			HasLoginText        bool   `json:"has_login_text"`
			HasVerificationText bool   `json:"has_verification_text"`
		}
		if err := json.Unmarshal([]byte(result.Value.Str()), &state); err != nil {
			return fmt.Errorf("parse document diagnostics: %w", err)
		}
		logrus.Warnf("search_feeds: navigation diagnostics stage=%s document ready_state=%s location_href=%s title=%s body_exists=%t body_text_length=%d about_blank=%t xiaohongshu=%t login_text=%t verification_text=%t",
			stage, state.ReadyState, state.LocationHref, state.Title, state.BodyExists, state.BodyTextLength,
			state.IsAboutBlank, state.IsXiaohongshu, state.HasLoginText, state.HasVerificationText)
		return nil
	}); err != nil {
		logrus.Warnf("search_feeds: navigation diagnostics stage=%s document eval unavailable: %v", stage, err)
	}
}

func diagnoseSearchBrowserState(page *rod.Page, stage string, trace *searchNavigationTrace) {
	diagnoseSearchBrowserStateWithRunner(
		page,
		stage,
		runSearchPageDiagnostic,
		func(diagnosticPage *rod.Page) error {
			return readSearchTargetInfo(diagnosticPage, stage)
		},
		func(diagnosticPage *rod.Page) error {
			return readSearchFrameTree(diagnosticPage, stage, trace)
		},
	)
}

func diagnoseSearchBrowserStateWithRunner(
	page *rod.Page,
	stage string,
	runDiagnostic func(*rod.Page, time.Duration, func(*rod.Page) error) error,
	readTarget func(*rod.Page) error,
	readFrameTree func(*rod.Page) error,
) {
	if page == nil {
		logrus.Warnf("search_feeds: browser diagnostics stage=%s skipped because page is nil", stage)
		return
	}

	const browserDiagnosticTimeout = 800 * time.Millisecond
	if err := runDiagnostic(page, browserDiagnosticTimeout, readTarget); err != nil {
		logrus.Warnf("search_feeds: browser diagnostics stage=%s Target.getTargetInfo unavailable: %v", stage, err)
	}
	if err := runDiagnostic(page, browserDiagnosticTimeout, readFrameTree); err != nil {
		logrus.Warnf("search_feeds: browser diagnostics stage=%s Page.getFrameTree unavailable: %v", stage, err)
	}
}

func readSearchTargetInfo(page *rod.Page, stage string) error {
	if page == nil || page.Browser() == nil {
		return fmt.Errorf("browser is unavailable")
	}
	client := page.Browser().Context(page.GetContext())
	result, err := (proto.TargetGetTargetInfo{TargetID: page.TargetID}).Call(client)
	if err != nil {
		return err
	}
	if result == nil || result.TargetInfo == nil {
		return fmt.Errorf("target info is empty")
	}
	info := result.TargetInfo
	logrus.Warnf("search_feeds: browser diagnostics stage=%s target url=%q type=%s title=%q attached=%t",
		stage, info.URL, info.Type, info.Title, info.Attached)
	return nil
}

func readSearchFrameTree(page *rod.Page, stage string, trace *searchNavigationTrace) error {
	result, err := proto.PageGetFrameTree{}.Call(page)
	if err != nil {
		return err
	}
	if result == nil || result.FrameTree == nil || result.FrameTree.Frame == nil {
		logrus.Warnf("search_feeds: browser diagnostics stage=%s main_frame_exists=false child_frame_count=0", stage)
		return nil
	}

	mainFrame := result.FrameTree.Frame
	transition := "unknown"
	if trace != nil && trace.initialFrameCaptured {
		switched := trace.initialURL == "about:blank" && mainFrame.URL != "about:blank" &&
			mainFrame.LoaderID != "" && mainFrame.LoaderID != trace.initialLoaderID
		transition = fmt.Sprintf("%t", switched)
	}
	logrus.Warnf("search_feeds: browser diagnostics stage=%s main_frame_exists=true frame_id=%s loader_id=%s url=%q security_origin=%q mime_type=%s child_frame_count=%d from_initial_about_blank_to_new_loader=%s",
		stage, mainFrame.ID, mainFrame.LoaderID, mainFrame.URL, mainFrame.SecurityOrigin,
		mainFrame.MIMEType, countSearchFrameChildren(result.FrameTree), transition)
	return nil
}

func countSearchFrameChildren(tree *proto.PageFrameTree) int {
	if tree == nil {
		return 0
	}
	count := len(tree.ChildFrames)
	for _, child := range tree.ChildFrames {
		count += countSearchFrameChildren(child)
	}
	return count
}

func runSearchPageDiagnostic(page *rod.Page, timeout time.Duration, action func(*rod.Page) error) (err error) {
	return runSearchPageDiagnosticWithPageContext(page, timeout, func(page *rod.Page, ctx context.Context) *rod.Page {
		return page.Context(ctx)
	}, action)
}

func runSearchPageDiagnosticWithPageContext(
	page *rod.Page,
	timeout time.Duration,
	cloneWithContext func(*rod.Page, context.Context) *rod.Page,
	action func(*rod.Page) error,
) (err error) {
	if page == nil {
		return fmt.Errorf("page is nil")
	}
	defer func() {
		if original := recover(); original != nil {
			err = fmt.Errorf("diagnostic panic: %v", original)
		}
	}()

	diagnosticCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// Page.Context replaces the page context on a clone, so this does not
	// inherit the expired navigation/action context.
	diagnosticPage := cloneWithContext(page, diagnosticCtx)
	return action(diagnosticPage)
}

func logSearchPageState(page *rod.Page, stage string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logrus.Warnf("search_feeds: page state unavailable at %s: %v", stage, recovered)
		}
	}()
	info, err := page.Info()
	if err != nil {
		logrus.Warnf("search_feeds: page state unavailable at %s: %v", stage, err)
		return
	}
	logrus.Infof("search_feeds: %s url=%s title=%s", stage, info.URL, info.Title)
}

func makeSearchURL(keyword string) string {

	values := url.Values{}
	values.Set("keyword", keyword)
	values.Set("source", "web_explore_feed")

	//https://www.xiaohongshu.com/search_result?keyword=%25E7%258E%258B%25E5%25AD%2590&source=web_search_result_notes
	//https://www.xiaohongshu.com/search_result?keyword=%25E7%258E%258B%25E5%25AD%2590&source=web_explore_feed
	return fmt.Sprintf("https://www.xiaohongshu.com/search_result?%s", values.Encode())
}
