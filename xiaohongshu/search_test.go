package xiaohongshu

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/lisiyuan/xiaohongshu-mcp-pro/browser"
	"github.com/stretchr/testify/require"
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

func TestSearchNavigationMustNavigateFailureIsStaged(t *testing.T) {
	var failedStage string
	waitCalled := false
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		runSearchNavigationPhases(
			func() { panic(fmt.Errorf("navigate failed")) },
			func() { waitCalled = true },
			func(stage string, _ any) { failedStage = stage },
		)
	}()
	require.Equal(t, "MustNavigate", failedStage)
	require.False(t, waitCalled, "MustWaitStable must not run after navigation failure")
	require.Contains(t, fmt.Sprint(recovered), "navigation MustNavigate failed: navigate failed")
}

func TestSearchNavigationMustWaitStableFailureIsStaged(t *testing.T) {
	var failedStage string
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		runSearchNavigationPhases(
			func() {},
			func() { panic(fmt.Errorf("stable wait failed")) },
			func(stage string, _ any) { failedStage = stage },
		)
	}()
	require.Equal(t, "MustWaitStable", failedStage)
	require.Contains(t, fmt.Sprint(recovered), "navigation MustWaitStable failed: stable wait failed")
}

func TestSearchNavigationSuccessRunsBothPhases(t *testing.T) {
	steps := make([]string, 0, 2)
	runSearchNavigationPhases(
		func() { steps = append(steps, "navigate") },
		func() { steps = append(steps, "wait-stable") },
		func(stage string, original any) { t.Fatalf("unexpected %s failure: %v", stage, original) },
	)
	require.Equal(t, []string{"navigate", "wait-stable"}, steps)
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

func TestSearchNavigationDiagnosticsPreserveOriginalPanic(t *testing.T) {
	called := false
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		runSearchNavigation(nil, "https://example.invalid", func(*rod.Page, string, any) {
			called = true
			panic("diagnostic failure")
		})
	}()
	if !called {
		t.Fatal("navigation diagnostics were not invoked")
	}
	if recovered == nil {
		t.Fatal("expected the original navigation panic")
	}
	if !strings.Contains(fmt.Sprint(recovered), "navigation MustNavigate failed") {
		t.Fatalf("expected staged navigation panic, got %v", recovered)
	}
	if strings.Contains(fmt.Sprint(recovered), "diagnostic failure") {
		t.Fatal("diagnostic panic replaced the original navigation panic")
	}
}
