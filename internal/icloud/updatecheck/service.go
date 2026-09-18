package updatecheck

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gpt-go/internal/icloud/buildinfo"
)

const (
	cacheTTL        = 10 * time.Minute
	requestTimeout  = 20 * time.Second
	responseMaxSize = 4 << 20
	githubRawURL    = "https://raw.githubusercontent.com"
)

//go:embed announcements.json
var announcementFiles embed.FS

type Announcement struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	Content     string `json:"content"`
	PublishedAt string `json:"published_at"`
	URL         string `json:"url,omitempty"`
}

type LatestInfo struct {
	Version     string `json:"version"`
	Name        string `json:"name"`
	Notes       string `json:"notes"`
	PublishedAt string `json:"published_at"`
	URL         string `json:"url"`
	Source      string `json:"source"`
}

type Status struct {
	Enabled         bool           `json:"enabled"`
	Repository      string         `json:"repository"`
	RepositoryURL   string         `json:"repository_url"`
	Current         buildinfo.Info `json:"current"`
	Latest          *LatestInfo    `json:"latest,omitempty"`
	UpdateAvailable bool           `json:"update_available"`
	CheckedAt       string         `json:"checked_at"`
	Error           string         `json:"error,omitempty"`
	Announcements   []Announcement `json:"announcements"`
}

type Service struct {
	enabled    bool
	repository string
	client     *http.Client
	rawBaseURL string
	mu         sync.Mutex
	cachedAt   time.Time
	cached     Status
}

type httpStatusError struct {
	StatusCode int
	Body       string
}

func (e *httpStatusError) Error() string {
	if strings.TrimSpace(e.Body) == "" {
		return fmt.Sprintf("HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("HTTP %d：%s", e.StatusCode, strings.TrimSpace(e.Body))
}

type announcementDocument struct {
	SchemaVersion int             `json:"schema_version"`
	Latest        *latestDocument `json:"latest"`
	Announcements []Announcement  `json:"announcements"`
}

type latestDocument struct {
	Version     string `json:"version"`
	Name        string `json:"name"`
	Notes       string `json:"notes"`
	PublishedAt string `json:"published_at"`
	URL         string `json:"url"`
}

func New(enabled bool, repository string) *Service {
	return &Service{
		enabled:    enabled,
		repository: strings.Trim(strings.TrimSpace(repository), "/"),
		client:     &http.Client{Timeout: requestTimeout},
		rawBaseURL: githubRawURL,
	}
}

// Check 通过仓库 Raw 公告清单检查项目版本和公告。
func (s *Service) Check(ctx context.Context, force bool) Status {
	now := time.Now()
	s.mu.Lock()
	if !force && !s.cachedAt.IsZero() && now.Sub(s.cachedAt) < cacheTTL {
		cached := s.cached
		s.mu.Unlock()
		return cached
	}
	s.mu.Unlock()

	status := s.check(ctx, now)
	s.mu.Lock()
	s.cached = status
	s.cachedAt = now
	s.mu.Unlock()
	return status
}

func (s *Service) check(ctx context.Context, checkedAt time.Time) Status {
	repositoryURL := ""
	if validRepository(s.repository) {
		repositoryURL = "https://github.com/" + s.repository
	}
	status := Status{
		Enabled:       s.enabled,
		Repository:    s.repository,
		RepositoryURL: repositoryURL,
		Current:       buildinfo.Current(),
		CheckedAt:     checkedAt.Format(time.RFC3339),
		Announcements: builtinAnnouncements(),
	}
	if !s.enabled {
		return status
	}
	if !validRepository(s.repository) {
		status.Error = "更新仓库格式不正确，应为 owner/repository"
		return status
	}

	document, documentErr := s.fetchRepositoryDocument(ctx)
	if documentErr != nil {
		status.Error = "读取仓库公告配置失败：" + documentErr.Error()
		return status
	}
	status.Announcements = mergeAnnouncements(status.Announcements, document.Announcements)
	if document.SchemaVersion != 1 {
		status.Error = fmt.Sprintf("仓库公告配置版本不支持：schema_version 应为 1，当前为 %d", document.SchemaVersion)
		return status
	}
	if document.Latest == nil {
		status.Error = "仓库公告配置缺少 latest"
		return status
	}
	latest, updateAvailable, updateAnnouncement, err := configuredLatest(status.Current, *document.Latest)
	if err != nil {
		status.Error = "仓库公告配置无效：" + err.Error()
		return status
	}
	status.Latest = latest
	status.UpdateAvailable = updateAvailable
	if updateAnnouncement != nil {
		status.Announcements = mergeAnnouncements(status.Announcements, []Announcement{*updateAnnouncement})
	}
	return status
}

func configuredLatest(current buildinfo.Info, document latestDocument) (*LatestInfo, bool, *Announcement, error) {
	latest := LatestInfo{
		Version:     truncateText(strings.TrimSpace(document.Version), 120),
		Name:        truncateText(strings.TrimSpace(document.Name), 200),
		Notes:       truncateText(strings.TrimSpace(document.Notes), 8000),
		PublishedAt: strings.TrimSpace(document.PublishedAt),
		URL:         safeHTTPSURL(document.URL),
		Source:      "config",
	}
	if latest.Version == "" {
		return nil, false, nil, errors.New("latest.version 为空")
	}
	if _, valid := parseVersion(latest.Version); !valid {
		return nil, false, nil, errors.New("latest.version 应为语义化版本，例如 2.1.0")
	}
	if strings.TrimSpace(document.URL) != "" && latest.URL == "" {
		return nil, false, nil, errors.New("latest.url 应为有效的 HTTPS 地址")
	}
	latest.Name = firstNonEmpty(latest.Name, latest.Version)
	updateAvailable := versionIsNewer(current.Version, latest.Version)
	if !updateAvailable {
		return &latest, false, nil, nil
	}
	announcement := normalizeAnnouncement(Announcement{
		ID:          "release-" + normalizeID(latest.Version),
		Type:        "update",
		Title:       "版本更新 " + latest.Version,
		Summary:     firstNonEmpty(firstContentLine(latest.Notes), "项目更新清单已发布新版本。"),
		Content:     firstNonEmpty(latest.Notes, "项目更新清单已发布新版本。"),
		PublishedAt: latest.PublishedAt,
		URL:         latest.URL,
	})
	return &latest, updateAvailable, &announcement, nil
}

func (s *Service) fetchRepositoryDocument(ctx context.Context) (announcementDocument, error) {
	announcementsURL := strings.TrimRight(s.rawBaseURL, "/") + "/" + s.repository + "/HEAD/internal/updatecheck/announcements.json?t=" + strconv.FormatInt(time.Now().UnixNano(), 10)
	var document announcementDocument
	if err := s.getJSON(ctx, announcementsURL, &document); err != nil {
		return announcementDocument{}, err
	}
	document.Announcements = normalizeAnnouncements(document.Announcements)
	return document, nil
}

func (s *Service) getJSON(ctx context.Context, targetURL string, target any) error {
	return s.get(ctx, targetURL, "application/json", func(reader io.Reader) error {
		decoder := json.NewDecoder(reader)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(target); err != nil {
			return fmt.Errorf("解析 JSON 失败：%w", err)
		}
		return nil
	})
}

func (s *Service) get(ctx context.Context, targetURL, accept string, decode func(io.Reader) error) error {
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("Cache-Control", "no-cache")
	request.Header.Set("Pragma", "no-cache")
	request.Header.Set("User-Agent", "iCloud-Privacy-Mail-v2-Updater/"+buildinfo.Current().Version)
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return &httpStatusError{StatusCode: response.StatusCode, Body: string(body)}
	}
	return decode(io.LimitReader(response.Body, responseMaxSize))
}

func builtinAnnouncements() []Announcement {
	raw, err := announcementFiles.ReadFile("announcements.json")
	if err != nil {
		return []Announcement{}
	}
	var document announcementDocument
	if json.Unmarshal(raw, &document) != nil {
		return []Announcement{}
	}
	return normalizeAnnouncements(document.Announcements)
}

func mergeAnnouncements(groups ...[]Announcement) []Announcement {
	items := map[string]Announcement{}
	for _, group := range groups {
		for _, raw := range group {
			item := normalizeAnnouncement(raw)
			if item.ID == "" || item.Title == "" {
				continue
			}
			items[item.ID] = item
		}
	}
	result := make([]Announcement, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].PublishedAt > result[j].PublishedAt
	})
	return result
}

func normalizeAnnouncements(items []Announcement) []Announcement {
	return mergeAnnouncements(items)
}

func normalizeAnnouncement(item Announcement) Announcement {
	item.ID = truncateText(strings.TrimSpace(item.ID), 120)
	item.Type = strings.ToLower(strings.TrimSpace(item.Type))
	switch item.Type {
	case "update", "project", "system":
	default:
		item.Type = "project"
	}
	item.Title = truncateText(strings.TrimSpace(item.Title), 160)
	item.Summary = truncateText(strings.TrimSpace(item.Summary), 500)
	item.Content = truncateText(strings.TrimSpace(item.Content), 8000)
	item.PublishedAt = strings.TrimSpace(item.PublishedAt)
	item.URL = safeHTTPSURL(item.URL)
	return item
}

func validRepository(repository string) bool {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, part := range parts {
		for _, char := range part {
			if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
				continue
			}
			return false
		}
	}
	return true
}

func safeHTTPSURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return ""
	}
	return parsed.String()
}

func versionIsNewer(current, latest string) bool {
	latestVersion, latestValid := parseVersion(latest)
	if !latestValid {
		return false
	}
	currentVersion, currentValid := parseVersion(current)
	if !currentValid {
		return normalizeVersion(current) != normalizeVersion(latest)
	}
	maxParts := len(currentVersion.Parts)
	if len(latestVersion.Parts) > maxParts {
		maxParts = len(latestVersion.Parts)
	}
	for index := 0; index < maxParts; index++ {
		currentPart := 0
		latestPart := 0
		if index < len(currentVersion.Parts) {
			currentPart = currentVersion.Parts[index]
		}
		if index < len(latestVersion.Parts) {
			latestPart = latestVersion.Parts[index]
		}
		if latestPart != currentPart {
			return latestPart > currentPart
		}
	}
	return comparePrerelease(latestVersion.Prerelease, currentVersion.Prerelease) > 0
}

func normalizeVersion(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "version")
	value = strings.TrimSpace(value)
	return strings.TrimPrefix(value, "v")
}

type parsedVersion struct {
	Parts      []int
	Prerelease string
}

func parseVersion(value string) (parsedVersion, bool) {
	value = normalizeVersion(value)
	if buildIndex := strings.IndexByte(value, '+'); buildIndex >= 0 {
		value = value[:buildIndex]
	}
	prerelease := ""
	if prereleaseIndex := strings.IndexByte(value, '-'); prereleaseIndex >= 0 {
		prerelease = value[prereleaseIndex+1:]
		value = value[:prereleaseIndex]
		if prerelease == "" {
			return parsedVersion{}, false
		}
	}
	fields := strings.Split(value, ".")
	if len(fields) < 2 {
		return parsedVersion{}, false
	}
	parts := make([]int, 0, len(fields))
	for _, field := range fields {
		if field == "" {
			return parsedVersion{}, false
		}
		part, err := strconv.Atoi(field)
		if err != nil || part < 0 {
			return parsedVersion{}, false
		}
		parts = append(parts, part)
	}
	return parsedVersion{Parts: parts, Prerelease: prerelease}, true
}

func comparePrerelease(left, right string) int {
	if left == right {
		return 0
	}
	if left == "" {
		return 1
	}
	if right == "" {
		return -1
	}
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	maxParts := len(leftParts)
	if len(rightParts) > maxParts {
		maxParts = len(rightParts)
	}
	for index := 0; index < maxParts; index++ {
		if index >= len(leftParts) {
			return -1
		}
		if index >= len(rightParts) {
			return 1
		}
		leftNumber, leftErr := strconv.Atoi(leftParts[index])
		rightNumber, rightErr := strconv.Atoi(rightParts[index])
		switch {
		case leftErr == nil && rightErr == nil && leftNumber != rightNumber:
			if leftNumber > rightNumber {
				return 1
			}
			return -1
		case leftErr == nil && rightErr != nil:
			return -1
		case leftErr != nil && rightErr == nil:
			return 1
		case leftParts[index] > rightParts[index]:
			return 1
		case leftParts[index] < rightParts[index]:
			return -1
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstContentLine(value string) string {
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "#-* "))
		if line != "" {
			return truncateText(line, 500)
		}
	}
	return ""
}

func normalizeID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Map(func(char rune) rune {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			return char
		}
		return '-'
	}, value)
	return strings.Trim(value, "-")
}

func truncateText(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:limit])) + "…"
}
