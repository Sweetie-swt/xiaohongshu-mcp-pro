package xiaohongshu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/go-rod/rod"
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
	pp := page.Timeout(60 * time.Second)

	return &SearchAction{page: pp}
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

	// Context() returns a clone; reapply the action timeout after deriving it so
	// the 60-second page bound is not accidentally discarded.
	page := s.page.Context(ctx).Timeout(60 * time.Second)
	logSearchPageState(page, "before navigate")

	phase = "keyword input"
	logrus.Infof("search_feeds: keyword input start")
	searchURL := makeSearchURL(keyword)
	logrus.Infof("search_feeds: keyword input end")

	phase = "navigate/search page"
	logrus.Infof("search_feeds: navigate/search page start")
	logrus.Infof("search_feeds: submit/search trigger start")
	runSearchNavigation(page, searchURL, diagnoseSearchNavigationFailure)
	logrus.Infof("search_feeds: submit/search trigger end")
	logSearchPageState(page, "navigate/search page end")
	logrus.Infof("search_feeds: navigate/search page end")

	phase = "wait result selector"
	logrus.Infof("search_feeds: wait result selector start")
	page.MustWait(`() => window.__INITIAL_STATE__ !== undefined`)
	logrus.Infof("search_feeds: wait result selector end")

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

		// 等待页面更新
		page.MustWaitStable()
		// 重新等待 __INITIAL_STATE__ 更新
		page.MustWait(`() => window.__INITIAL_STATE__ !== undefined`)
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

func runSearchNavigation(page *rod.Page, searchURL string, diagnose func(*rod.Page, any)) {
	defer func() {
		if recovered := recover(); recovered != nil {
			// Diagnostics are best-effort. Swallow a diagnostic panic so the
			// original navigation panic remains the error seen by the service.
			if diagnose != nil {
				func() {
					defer func() { _ = recover() }()
					diagnose(page, recovered)
				}()
			}
			panic(recovered)
		}
	}()
	page.MustNavigate(searchURL)
	page.MustWaitStable()
}

func diagnoseSearchNavigationFailure(page *rod.Page, original any) {
	logrus.Warnf("search_feeds: navigation failed, original=%v", original)
	if page == nil {
		logrus.Warn("search_feeds: navigation diagnostics skipped because page is nil")
		return
	}

	const diagnosticTimeout = 800 * time.Millisecond
	diagnosticCtx, cancel := context.WithTimeout(context.Background(), diagnosticTimeout)
	defer cancel()
	diagnosticPage := page.Context(diagnosticCtx).Timeout(diagnosticTimeout)

	if info, err := diagnosticPage.Info(); err != nil {
		logrus.Warnf("search_feeds: navigation diagnostics URL/title unavailable: %v", err)
	} else {
		logrus.Warnf("search_feeds: navigation diagnostics url=%s title=%s", info.URL, info.Title)
	}

	result, err := diagnosticPage.Eval(`() => {
		const body = (document.body && (document.body.innerText || document.body.textContent) || '').slice(0, 4000);
		return JSON.stringify({
			url: String(window.location.href || ''),
			title: String(document.title || ''),
			ready_state: String(document.readyState || ''),
			is_about_blank: window.location.href === 'about:blank',
			is_xiaohongshu: /xiaohongshu\.com$/.test(window.location.hostname || ''),
			has_login_text: /登录|验证码|手机号/.test(body),
			has_verification_text: /安全验证|扫码|验证/.test(body),
			body_text_length: body.length
		});
	}`)
	if err != nil {
		logrus.Warnf("search_feeds: navigation diagnostics document state unavailable: %v", err)
		return
	}

	var state struct {
		URL                 string `json:"url"`
		Title               string `json:"title"`
		ReadyState          string `json:"ready_state"`
		IsAboutBlank        bool   `json:"is_about_blank"`
		IsXiaohongshu       bool   `json:"is_xiaohongshu"`
		HasLoginText        bool   `json:"has_login_text"`
		HasVerificationText bool   `json:"has_verification_text"`
		BodyTextLength      int    `json:"body_text_length"`
	}
	if err := json.Unmarshal([]byte(result.Value.Str()), &state); err != nil {
		logrus.Warnf("search_feeds: navigation diagnostics parse failed: %v", err)
		return
	}
	logrus.Warnf("search_feeds: navigation diagnostics ready_state=%s url=%s title=%s about_blank=%t xiaohongshu=%t login_text=%t verification_text=%t body_text_length=%d",
		state.ReadyState, state.URL, state.Title, state.IsAboutBlank, state.IsXiaohongshu,
		state.HasLoginText, state.HasVerificationText, state.BodyTextLength)
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
