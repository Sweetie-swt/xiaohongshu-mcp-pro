package xiaohongshu

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/lisiyuan/xiaohongshu-mcp-pro/browser"
	"github.com/stretchr/testify/require"
	"github.com/ysmood/gson"
)

func TestSearch(t *testing.T) {

	t.Skip("SKIP: 测试发布")

	b := browser.NewBrowser(false)
	defer b.Close()

	page := b.NewPage()
	defer func() {
		_ = page.Close()
	}()

	action := NewSearchAction(page)

	feeds, err := action.Search(context.Background(), "Kimi")
	require.NoError(t, err)
	require.NotEmpty(t, feeds, "feeds should not be empty")

	fmt.Printf("成功获取到 %d 个 Feed\n", len(feeds))

	for _, feed := range feeds {
		fmt.Printf("Feed ID: %s\n", feed.ID)
		fmt.Printf("Feed Title: %s\n", feed.NoteCard.DisplayTitle)
	}
}

func TestSearchWithFilters(t *testing.T) {

	//t.Skip("SKIP: 测试筛选功能")

	b := browser.NewBrowser(false)
	defer b.Close()

	page := b.NewPage()
	defer func() {
		_ = page.Close()
	}()

	action := NewSearchAction(page)

	// 使用新的 FilterOption 结构
	filter := FilterOption{
		NoteType:    "图文",
		PublishTime: "一天内",
	}

	feeds, err := action.Search(context.Background(), "dn432", filter)
	require.NoError(t, err)
	require.NotEmpty(t, feeds, "feeds should not be empty")

	fmt.Printf("成功获取到 %d 个筛选后的 Feed\n", len(feeds))

	for _, feed := range feeds {
		fmt.Printf("Feed ID: %s\n", feed.ID)
		fmt.Printf("Feed Title: %s\n", feed.NoteCard.DisplayTitle)
	}
}

func TestFilterValidation(t *testing.T) {
	emptyInternalFilters, err := collectInternalFilters(FilterOption{})
	require.NoError(t, err)
	require.Empty(t, emptyInternalFilters, "an empty FilterOption must skip the filter UI path")

	validInternalFilters, err := collectInternalFilters(FilterOption{NoteType: "图文"})
	require.NoError(t, err)
	require.Len(t, validInternalFilters, 1, "a real filter must keep the filter UI path enabled")

	// 测试有效的筛选选项转换
	validFilter := FilterOption{
		NoteType:    "图文",
		PublishTime: "一天内",
	}
	internalFilters, err := convertToInternalFilters(validFilter)
	require.NoError(t, err)
	require.Len(t, internalFilters, 2)

	// 验证转换后的内部筛选选项
	for _, filter := range internalFilters {
		err := validateInternalFilterOption(filter)
		require.NoError(t, err)
	}

	// 测试无效的筛选值
	invalidFilter := FilterOption{
		NoteType: "不存在的类型",
	}
	_, err = convertToInternalFilters(invalidFilter)
	require.Error(t, err)
	require.Contains(t, err.Error(), "未找到文本")

	// 测试所有有效的筛选选项
	allFilters := FilterOption{
		SortBy:      "最新",
		NoteType:    "视频",
		PublishTime: "一周内",
		SearchScope: "已关注",
		Location:    "同城",
	}
	internalFilters, err = convertToInternalFilters(allFilters)
	require.NoError(t, err)
	require.Len(t, internalFilters, 5)
}

func TestSearchNavigationTraceFiltersAndRecordsMainDocumentEvents(t *testing.T) {
	trace := &searchNavigationTrace{
		traceID:            "trace-test",
		mainFrameID:        proto.PageFrameID("main-frame"),
		documentRequestIDs: make(map[proto.NetworkRequestID]struct{}),
	}
	trace.handleRequestWillBeSent(&proto.NetworkRequestWillBeSent{
		RequestID:   "document-request",
		LoaderID:    "loader-1",
		DocumentURL: "https://www.xiaohongshu.com/search_result?keyword=Kimi",
		Request:     &proto.NetworkRequest{URL: "https://www.xiaohongshu.com/search_result?keyword=Kimi", Method: "GET"},
		Type:        proto.NetworkResourceTypeDocument,
		FrameID:     "main-frame",
	})
	trace.handleRequestWillBeSent(&proto.NetworkRequestWillBeSent{
		RequestID: "image-request",
		Request:   &proto.NetworkRequest{URL: "https://www.xiaohongshu.com/logo.png", Method: "GET"},
		Type:      proto.NetworkResourceTypeImage,
		FrameID:   "main-frame",
	})
	trace.handleRequestWillBeSent(&proto.NetworkRequestWillBeSent{
		RequestID: "xhr-request",
		Request:   &proto.NetworkRequest{URL: "https://www.xiaohongshu.com/api/sns/web/v1/search", Method: "GET"},
		Type:      proto.NetworkResourceTypeXHR,
		FrameID:   "main-frame",
	})
	trace.handleResponseReceived(&proto.NetworkResponseReceived{
		RequestID: "document-request",
		LoaderID:  "loader-1",
		Type:      proto.NetworkResourceTypeDocument,
		FrameID:   "main-frame",
		Response: &proto.NetworkResponse{
			URL:               "https://www.xiaohongshu.com/search_result?keyword=Kimi",
			Status:            200,
			StatusText:        "OK",
			Protocol:          "h2",
			MIMEType:          "text/html",
			FromDiskCache:     false,
			FromServiceWorker: false,
			RemoteIPAddress:   "192.0.2.1",
		},
	})
	trace.handleLoadingFailed(&proto.NetworkLoadingFailed{
		RequestID: "document-request",
		Type:      proto.NetworkResourceTypeDocument,
		ErrorText: "net::ERR_FAILED",
		Canceled:  true,
	})
	trace.handleLoadingFailed(&proto.NetworkLoadingFailed{
		RequestID: "image-request",
		Type:      proto.NetworkResourceTypeImage,
		ErrorText: "image failure",
	})

	require.Equal(t, 1, trace.mainDocumentRequests)
	require.Equal(t, 1, trace.mainDocumentResponses)
	require.Equal(t, []int{200}, trace.mainDocumentResponseStatuses)
	require.Equal(t, 1, trace.mainDocumentLoadingFailures)
	require.Equal(t, []string{"net::ERR_FAILED"}, trace.mainDocumentFailureTexts)
	require.Len(t, trace.documentRequestIDs, 1)
}

func TestSearchNavigationTraceRecordsFrameLifecycleAndExecutionContext(t *testing.T) {
	trace := &searchNavigationTrace{
		traceID:            "trace-test",
		mainFrameID:        proto.PageFrameID("main-frame"),
		documentRequestIDs: make(map[proto.NetworkRequestID]struct{}),
	}
	trace.handleFrameNavigated(&proto.PageFrameNavigated{Frame: &proto.PageFrame{
		ID:             "main-frame",
		LoaderID:       "loader-2",
		URL:            "https://www.xiaohongshu.com/search_result?keyword=Kimi",
		SecurityOrigin: "https://www.xiaohongshu.com",
		MIMEType:       "text/html",
	}})
	trace.handleFrameNavigated(&proto.PageFrameNavigated{Frame: &proto.PageFrame{
		ID:       "child-frame",
		ParentID: "main-frame",
		LoaderID: "child-loader",
		URL:      "https://example.invalid/frame",
	}})
	trace.handleLifecycleEvent(&proto.PageLifecycleEvent{FrameID: "main-frame", LoaderID: "loader-2", Name: proto.PageLifecycleEventNameDOMContentLoaded})
	trace.handleLifecycleEvent(&proto.PageLifecycleEvent{FrameID: "child-frame", LoaderID: "child-loader", Name: proto.PageLifecycleEventNameLoad})
	trace.handleDOMContentEventFired(&proto.PageDomContentEventFired{})
	trace.handleLoadEventFired(&proto.PageLoadEventFired{})
	trace.handleExecutionContextCreated(&proto.RuntimeExecutionContextCreated{Context: &proto.RuntimeExecutionContextDescription{
		ID:     7,
		Origin: "https://www.xiaohongshu.com",
		Name:   "",
		AuxData: map[string]gson.JSON{
			"frameId":   gson.New("main-frame"),
			"isDefault": gson.New(true),
		},
	}})
	trace.handleExecutionContextCreated(&proto.RuntimeExecutionContextCreated{Context: &proto.RuntimeExecutionContextDescription{
		ID: 8,
		AuxData: map[string]gson.JSON{
			"frameId":   gson.New("child-frame"),
			"isDefault": gson.New(true),
		},
	}})

	require.Equal(t, 1, trace.mainFrameNavigations)
	require.Equal(t, []string{"https://www.xiaohongshu.com/search_result?keyword=Kimi"}, trace.mainFrameNavigationURLs)
	require.Equal(t, []proto.PageLifecycleEventName{proto.PageLifecycleEventNameDOMContentLoaded}, trace.mainFrameLifecycleNames)
	require.True(t, trace.mainExecutionContextCreated)
	require.True(t, trace.mainDefaultExecutionContextCreated)
}

func TestSearchNavigationTraceStopsListenerWithoutLeakingWaiter(t *testing.T) {
	traceCtx, cancel := context.WithCancel(context.Background())
	waitStarted := make(chan struct{})
	trace := newSearchNavigationTraceController("trace-test", cancel, func() {
		close(waitStarted)
		<-traceCtx.Done()
	})
	<-waitStarted
	trace.Stop()
	trace.Stop()
	select {
	case <-trace.done:
	default:
		t.Fatal("trace listener is still running after Stop")
	}
}

func TestSearchBrowserDiagnosticsContinueAfterIndependentFailure(t *testing.T) {
	page := &rod.Page{}
	calls := 0
	diagnoseSearchBrowserStateWithRunner(
		page,
		"Navigate",
		func(_ *rod.Page, _ time.Duration, _ func(*rod.Page) error) error {
			calls++
			return fmt.Errorf("diagnostic unavailable")
		},
		func(*rod.Page) error { return nil },
		func(*rod.Page) error { return nil },
	)
	require.Equal(t, 2, calls, "Target and frame-tree diagnostics must be independent")
}

func TestRunSearchNavigationNavigateSuccess(t *testing.T) {
	searchURL := makeSearchURL("Kimi")
	navigateCalls := 0
	err := runSearchNavigationWithOps(
		context.Background(),
		searchURL,
		func(stageCtx context.Context, targetURL string) error {
			navigateCalls++
			_, hasDeadline := stageCtx.Deadline()
			require.True(t, hasDeadline)
			require.Equal(t, searchURL, targetURL)
			return nil
		},
		func(context.Context) (string, error) {
			t.Fatal("target URL check must not run after successful Navigate")
			return "", nil
		},
		func(stage string, original any) { t.Fatalf("unexpected %s failure: %v", stage, original) },
	)
	require.NoError(t, err)
	require.Equal(t, 1, navigateCalls)
}

func TestRunSearchNavigationTimeoutTargetReachedEntersReadyStage(t *testing.T) {
	searchURL := makeSearchURL("老人家一路走好")
	navigateCalls := 0
	var navigateCtx context.Context
	currentURLChecks := 0
	err := runSearchNavigationWithOps(
		context.Background(),
		searchURL,
		func(stageCtx context.Context, _ string) error {
			navigateCalls++
			navigateCtx = stageCtx
			return context.DeadlineExceeded
		},
		func(stageCtx context.Context) (string, error) {
			currentURLChecks++
			_, hasDeadline := stageCtx.Deadline()
			require.True(t, hasDeadline)
			return searchURL, nil
		},
		func(stage string, original any) { t.Fatalf("unexpected %s failure: %v", stage, original) },
	)
	require.NoError(t, err)
	require.Equal(t, 1, navigateCalls, "Navigate must not be retried")
	require.Equal(t, 1, currentURLChecks)
	require.Error(t, navigateCtx.Err(), "navigation child context should be canceled after the stage")

	readyCtxSeen := context.Context(nil)
	err = waitForSearchResultReadyWith(context.Background(), func(stageCtx context.Context) error {
		readyCtxSeen = stageCtx
		require.Nil(t, stageCtx.Err())
		return nil
	})
	require.NoError(t, err)
	require.NotEqual(t, navigateCtx, readyCtxSeen, "ready stage must use a new child context")
}

func TestRunSearchNavigationTimeoutAboutBlankReturnsError(t *testing.T) {
	var diagnosedStage string
	err := runSearchNavigationWithOps(
		context.Background(),
		makeSearchURL("Kimi"),
		func(context.Context, string) error { return context.DeadlineExceeded },
		func(context.Context) (string, error) { return "about:blank", nil },
		func(stage string, _ any) { diagnosedStage = stage },
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "navigation command timed out before the search target was reached")
	require.Equal(t, "Navigate", diagnosedStage)
}

func TestRunSearchNavigationTimeoutWrongTargetReturnsError(t *testing.T) {
	tests := []struct {
		name       string
		currentURL string
	}{
		{name: "wrong host", currentURL: "https://xiaohongshu.com/search_result?keyword=Kimi"},
		{name: "wrong path", currentURL: "https://www.xiaohongshu.com/explore?keyword=Kimi"},
		{name: "wrong keyword", currentURL: "https://www.xiaohongshu.com/search_result?keyword=Other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runSearchNavigationWithOps(
				context.Background(),
				makeSearchURL("Kimi"),
				func(context.Context, string) error { return context.DeadlineExceeded },
				func(context.Context) (string, error) { return tt.currentURL, nil },
				func(string, any) {},
			)
			require.Error(t, err)
		})
	}
}

func TestExpectedSearchURLRequiresExactSearchTargetAndKeyword(t *testing.T) {
	expected := makeSearchURL("Kimi")
	tests := []struct {
		name       string
		currentURL string
		want       bool
	}{
		{name: "expected target", currentURL: expected, want: true},
		{name: "source query may differ", currentURL: "https://www.xiaohongshu.com/search_result?keyword=Kimi&source=web_search_result_notes", want: true},
		{name: "login page", currentURL: "https://www.xiaohongshu.com/login?keyword=Kimi", want: false},
		{name: "different xiaohongshu path", currentURL: "https://www.xiaohongshu.com/explore?keyword=Kimi", want: false},
		{name: "different host", currentURL: "https://xiaohongshu.com/search_result?keyword=Kimi", want: false},
		{name: "different keyword", currentURL: "https://www.xiaohongshu.com/search_result?keyword=Other", want: false},
		{name: "missing keyword", currentURL: "https://www.xiaohongshu.com/search_result", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isExpectedSearchURL(tt.currentURL, expected))
		})
	}
}

func TestWaitForSearchResultReadySuccess(t *testing.T) {
	waitCalled := false
	err := waitForSearchResultReadyWith(context.Background(), func(stageCtx context.Context) error {
		waitCalled = true
		_, hasDeadline := stageCtx.Deadline()
		require.True(t, hasDeadline)
		require.Nil(t, stageCtx.Err())
		return nil
	})
	require.NoError(t, err)
	require.True(t, waitCalled)
}

func TestWaitForSearchResultReadyTimeoutReturnsExplicitError(t *testing.T) {
	err := waitForSearchResultReadyWith(context.Background(), func(context.Context) error {
		return context.DeadlineExceeded
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "search result ready timeout:")
}

func TestRunSearchNavigationPreservesNonTimeoutError(t *testing.T) {
	original := fmt.Errorf("connection refused")
	diagnosed := false
	err := runSearchNavigationWithOps(
		context.Background(),
		makeSearchURL("Kimi"),
		func(context.Context, string) error { return original },
		func(context.Context) (string, error) {
			t.Fatal("target URL check must only run for timeout errors")
			return "", nil
		},
		func(stage string, got any) {
			diagnosed = true
			require.Equal(t, "Navigate", stage)
			require.Equal(t, original, got)
		},
	)
	require.ErrorIs(t, err, original)
	require.True(t, diagnosed)
}

func TestSearchNavigationDiagnosticsUseIndependentProjects(t *testing.T) {
	page := &rod.Page{}
	timeouts := make([]time.Duration, 0, 2)
	diagnoseSearchNavigationFailureWithRunner(page, "MustNavigate", fmt.Errorf("original"),
		func(_ *rod.Page, timeout time.Duration, _ func(*rod.Page) error) error {
			timeouts = append(timeouts, timeout)
			if len(timeouts) == 1 {
				return fmt.Errorf("URL/title unavailable")
			}
			return fmt.Errorf("document eval unavailable")
		})
	require.Equal(t, []time.Duration{800 * time.Millisecond, 1500 * time.Millisecond}, timeouts)
}

func TestSearchPageDiagnosticDoesNotReuseExpiredContext(t *testing.T) {
	expiredCtx, cancel := context.WithCancel(context.Background())
	cancel()
	var diagnosticCtx context.Context
	diagnosticWasActive := false
	err := runSearchPageDiagnosticWithPageContext(&rod.Page{}, time.Second,
		func(_ *rod.Page, ctx context.Context) *rod.Page {
			diagnosticCtx = ctx
			diagnosticWasActive = ctx.Err() == nil
			return &rod.Page{}
		}, func(*rod.Page) error { return nil })
	require.NoError(t, err)
	require.NotNil(t, diagnosticCtx)
	require.True(t, diagnosticWasActive, "diagnostic context must be active while the diagnostic runs")
	require.NotEqual(t, expiredCtx, diagnosticCtx, "diagnostics must not reuse the expired action context")
}
