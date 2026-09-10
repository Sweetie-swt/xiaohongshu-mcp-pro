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

func TestBootstrapSearchHomepageUsesHomepageBeforeControls(t *testing.T) {
	steps := make([]string, 0, 2)
	err := bootstrapSearchHomepageWith(
		context.Background(),
		func(stageCtx context.Context, targetURL string) error {
			steps = append(steps, "navigate:"+targetURL)
			_, hasDeadline := stageCtx.Deadline()
			require.True(t, hasDeadline, "homepage bootstrap must derive a bounded stage context")
			return nil
		},
		func(context.Context) error {
			steps = append(steps, "homepage-ready")
			return nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, []string{
		"navigate:https://www.xiaohongshu.com/explore",
		"homepage-ready",
	}, steps)
}

func TestSearchRouteURLMatchesExactDecodedKeyword(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "exact keyword", url: "https://www.xiaohongshu.com/search_result?keyword=老人家&source=web_search", want: true},
		{name: "substring is not enough", url: "https://www.xiaohongshu.com/search_result?keyword=老人家一路走好", want: false},
		{name: "wrong host", url: "https://xiaohongshu.com/search_result?keyword=老人家", want: false},
		{name: "wrong path", url: "https://www.xiaohongshu.com/explore?keyword=老人家", want: false},
		{name: "missing keyword", url: "https://www.xiaohongshu.com/search_result", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, searchRouteURLMatches(tt.url, "老人家"))
		})
	}
}

func TestSearchReadyScriptsRequireRouteAndSearchData(t *testing.T) {
	require.Contains(t, searchRouteReadyScript, "window.location.pathname !== '/search_result'")
	require.Contains(t, searchRouteReadyScript, "URLSearchParams")
	require.NotContains(t, searchRouteReadyScript, "source")

	require.Contains(t, searchDataReadyScript, "window.__INITIAL_STATE__")
	require.Contains(t, searchDataReadyScript, "Array.isArray(feedsData)")
	require.Contains(t, searchDataReadyScript, "window.location.pathname !== '/search_result'")
	require.Contains(t, searchDataReadyScript, "URLSearchParams")
	require.NotContains(t, searchDataReadyScript, "feedsData.length > 0")
	require.NotContains(t, searchHomepageReadyScript, "__INITIAL_STATE__")
}

func TestSearchInteractionUsesObservedSelectorsAndNativeRodPath(t *testing.T) {
	require.Equal(t, "input#search-input", searchInputSelector)
	require.Equal(t, "div.search-icon", searchIconSelector)
	require.Contains(t, searchHomepageReadyScript, "input#search-input")
	require.Contains(t, searchHomepageReadyScript, "div.search-icon")
	require.NotContains(t, searchHomepageReadyScript, ".value")
}

func TestSearchTriggerHitTestScriptIsReadOnlyAndSafe(t *testing.T) {
	require.Contains(t, searchTriggerHitTestScript, "document.elementFromPoint")
	require.Contains(t, searchTriggerHitTestScript, "getBoundingClientRect")
	for _, forbidden := range []string{
		".value",
		"textContent",
		"innerHTML",
		"outerHTML",
		".click(",
	} {
		require.NotContains(t, searchTriggerHitTestScript, forbidden)
	}
}

func TestStagedSearchTriggerClickUsesOneMousePair(t *testing.T) {
	events := make([]string, 0, 6)
	var downCount, upCount int
	err := stagedSearchTriggerClickWithOps(searchTriggerClickOps{
		hitTest: func() error {
			events = append(events, "hit-test")
			return nil
		},
		waitInteractable: func() (*proto.Point, error) {
			events = append(events, "interactable")
			return &proto.Point{X: 10, Y: 20}, nil
		},
		moveMouse: func(proto.Point) error {
			events = append(events, "move")
			return nil
		},
		waitEnabled: func() error {
			events = append(events, "enabled")
			return nil
		},
		mouseDown: func(proto.InputMouseButton, int) error {
			events = append(events, "down")
			downCount++
			return nil
		},
		mouseUp: func(proto.InputMouseButton, int) error {
			events = append(events, "up")
			upCount++
			return nil
		},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"hit-test", "interactable", "move", "enabled", "down", "up"}, events)
	require.Equal(t, 1, downCount)
	require.Equal(t, 1, upCount)
}

func TestStagedSearchTriggerClickStopsBeforeMouseDownOnStageFailure(t *testing.T) {
	stageErrors := []struct {
		name  string
		stage string
		want  string
		err   error
	}{
		{name: "hit-test", stage: "hit-test", want: "search trigger hit-test failed", err: fmt.Errorf("covered")},
		{name: "interactable", stage: "interactable", want: "search trigger interactable wait failed", err: fmt.Errorf("timeout")},
		{name: "move", stage: "move", want: "search trigger mouse move failed", err: fmt.Errorf("move failed")},
		{name: "enabled", stage: "enabled", want: "search trigger enabled wait failed", err: fmt.Errorf("disabled")},
		{name: "mouse down", stage: "down", want: "search trigger mouse down failed", err: fmt.Errorf("down failed")},
		{name: "mouse up", stage: "up", want: "search trigger mouse up failed", err: fmt.Errorf("up failed")},
	}

	for _, tt := range stageErrors {
		t.Run(tt.name, func(t *testing.T) {
			events := make([]string, 0, 6)
			downCount, upCount := 0, 0
			fail := func(stage string) func() error {
				return func() error {
					events = append(events, stage)
					if stage == tt.stage {
						return tt.err
					}
					return nil
				}
			}
			err := stagedSearchTriggerClickWithOps(searchTriggerClickOps{
				hitTest: fail("hit-test"),
				waitInteractable: func() (*proto.Point, error) {
					events = append(events, "interactable")
					if tt.stage == "interactable" {
						return nil, tt.err
					}
					return &proto.Point{X: 10, Y: 20}, nil
				},
				moveMouse: func(proto.Point) error {
					events = append(events, "move")
					if tt.stage == "move" {
						return tt.err
					}
					return nil
				},
				waitEnabled: fail("enabled"),
				mouseDown: func(proto.InputMouseButton, int) error {
					events = append(events, "down")
					downCount++
					if tt.stage == "down" {
						return tt.err
					}
					return nil
				},
				mouseUp: func(proto.InputMouseButton, int) error {
					events = append(events, "up")
					upCount++
					if tt.stage == "up" {
						return tt.err
					}
					return nil
				},
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.want)
			if tt.stage != "up" {
				require.Equal(t, 0, upCount, "mouse up must not run before the failing stage completes")
			}
			if tt.stage == "hit-test" || tt.stage == "interactable" || tt.stage == "move" || tt.stage == "enabled" {
				require.Equal(t, 0, downCount, "mouse down must not run before the failing stage completes")
			}
		})
	}
}

func TestSearchRouteAndDataReadyErrorsAreDistinct(t *testing.T) {
	routeErr := waitForSearchRouteReadyWith(context.Background(), func(context.Context) error {
		return context.DeadlineExceeded
	})
	require.Error(t, routeErr)
	require.Contains(t, routeErr.Error(), "search route timeout:")

	dataErr := waitForSearchDataReadyWith(context.Background(), func(context.Context) error {
		return context.DeadlineExceeded
	})
	require.Error(t, dataErr)
	require.Contains(t, dataErr.Error(), "search data ready timeout:")
}

func TestSearchReadyStagesUseFreshContexts(t *testing.T) {
	parentCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var routeCtx context.Context
	var dataCtx context.Context

	err := waitForSearchRouteReadyWith(parentCtx, func(ctx context.Context) error {
		routeCtx = ctx
		require.Nil(t, ctx.Err())
		return nil
	})
	require.NoError(t, err)

	err = waitForSearchDataReadyWith(parentCtx, func(ctx context.Context) error {
		dataCtx = ctx
		require.Nil(t, ctx.Err())
		return nil
	})
	require.NoError(t, err)
	require.NotSame(t, routeCtx, dataCtx)
	require.Nil(t, parentCtx.Err())
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
