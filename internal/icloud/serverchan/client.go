package serverchan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const defaultEndpoint = "https://sctapi.ftqq.com"

type Options struct {
	SendKey string
	HideIP  bool
}

type Message struct {
	Title string
	Desp  string
	Short string
}

type Result struct {
	PushID  string `json:"pushid,omitempty"`
	ReadKey string `json:"readkey,omitempty"`
}

type Sender interface {
	Send(context.Context, Options, Message) (Result, error)
}

type Client struct {
	HTTPClient *http.Client
	Endpoint   string
}

type pushResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: 12 * time.Second},
		Endpoint:   defaultEndpoint,
	}
}

func (c *Client) Send(ctx context.Context, options Options, message Message) (Result, error) {
	options.SendKey = strings.TrimSpace(options.SendKey)
	if options.SendKey == "" {
		return Result{}, errors.New("Server 酱 SendKey 未配置")
	}
	message.Title = truncateRunes(strings.TrimSpace(message.Title), 32)
	if message.Title == "" {
		return Result{}, errors.New("推送标题为空")
	}
	payload := map[string]any{
		"title": message.Title,
		"desp":  truncateBytes(strings.TrimSpace(message.Desp), 32*1024),
		"short": truncateRunes(strings.TrimSpace(message.Short), 64),
	}
	if options.HideIP {
		payload["noip"] = 1
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{}, err
	}
	endpoint := strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	requestURL := endpoint + "/" + url.PathEscape(options.SendKey) + ".send"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "iCloud-Privacy-Mail-v2/ServerChan")
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 12 * time.Second}
	}
	response, err := httpClient.Do(request)
	if err != nil {
		var requestError *url.Error
		if errors.As(err, &requestError) && requestError.Err != nil {
			err = requestError.Err
		}
		return Result{}, fmt.Errorf("Server 酱请求失败：%w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return Result{}, fmt.Errorf("读取 Server 酱响应失败：%w", err)
	}
	if len(responseBody) > 1<<20 {
		return Result{}, errors.New("Server 酱响应过大")
	}
	var decoded pushResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return Result{}, fmt.Errorf("Server 酱返回了无效响应（HTTP %d）", response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || decoded.Code != 0 {
		message := strings.TrimSpace(decoded.Message)
		if message == "" {
			message = fmt.Sprintf("HTTP %d，业务代码 %d", response.StatusCode, decoded.Code)
		}
		return Result{}, errors.New("Server 酱推送未入队：" + message)
	}
	var result Result
	if len(decoded.Data) > 0 && string(decoded.Data) != "null" {
		_ = json.Unmarshal(decoded.Data, &result)
	}
	return result, nil
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}

func truncateBytes(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
