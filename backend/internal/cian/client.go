package cian

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	rand "math/rand/v2"
	"mime"
	"net"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

const (
	DefaultHomeURL = "https://www.cian.ru/"
	DefaultAPIURL  = "https://api.cian.ru/search-offers/v2/search-offers-desktop/"
	defaultReferer = "https://www.cian.ru/cat.php?deal_type=sale&engine_version=2&offer_type=flat&region=1"
	maxBodyBytes   = 32 << 20
)

var ErrBlocked = errors.New("Cian anti-bot returned a captcha or blocked response")

type browserIdentity struct {
	profile profiles.ClientProfile
	version string
}

var browserIdentities = []browserIdentity{
	{profile: profiles.Chrome_133, version: "133"},
	{profile: profiles.Chrome_144, version: "144"},
	{profile: profiles.Chrome_146, version: "146"},
}

// Pause waits between network requests. It is injectable to keep unit tests
// deterministic and instant.
type Pause func(context.Context) error

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
	CloseIdleConnections()
}

// SessionConfig holds one browser-like cookie session. A Session must stay
// bound to the same proxy because Cian can bind challenge cookies to its IP.
type SessionConfig struct {
	HomeURL          string
	APIURL           string
	Region           int
	BootstrapCookies bool
	BetweenRequests  Pause
}

// Session makes requests through one proxy and one cookie jar.
type Session struct {
	client      httpDoer
	config      SessionConfig
	identity    browserIdentity
	initialized bool
}

// NewTLSSession creates an HTTP/2-capable Chrome-profile client. HTTP/3 is
// disabled because conventional HTTP/SOCKS proxies do not carry QUIC traffic.
func NewTLSSession(proxyURL string, timeout time.Duration, config SessionConfig) (*Session, error) {
	if timeout <= 0 {
		return nil, errors.New("timeout must be positive")
	}
	config = withSessionDefaults(config)
	identity := browserIdentities[rand.IntN(len(browserIdentities))]

	options := []tlsclient.HttpClientOption{
		tlsclient.WithTimeoutMilliseconds(int(timeout.Milliseconds())),
		tlsclient.WithClientProfile(identity.profile),
		tlsclient.WithCookieJar(tlsclient.NewCookieJar()),
		tlsclient.WithRandomTLSExtensionOrder(),
		tlsclient.WithDisableHttp3(),
	}
	if proxyURL != "" {
		options = append(options, tlsclient.WithProxyUrl(proxyURL))
	}
	client, err := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(), options...)
	if err != nil {
		return nil, fmt.Errorf("create TLS client: %w", err)
	}
	return &Session{client: client, config: config, identity: identity}, nil
}

func withSessionDefaults(config SessionConfig) SessionConfig {
	if config.HomeURL == "" {
		config.HomeURL = DefaultHomeURL
	}
	if config.APIURL == "" {
		config.APIURL = DefaultAPIURL
	}
	if config.Region == 0 {
		config.Region = 1
	}
	if config.BetweenRequests == nil {
		config.BetweenRequests = func(context.Context) error { return nil }
	}
	return config
}

func newSessionForTest(client httpDoer, config SessionConfig) *Session {
	return &Session{client: client, config: withSessionDefaults(config), identity: browserIdentities[len(browserIdentities)-1]}
}

// Search fetches and parses one result page.
func (session *Session) Search(ctx context.Context, filter Filter, page int) ([]Listing, error) {
	if page < 1 {
		return nil, errors.New("page must be at least 1")
	}
	if session.config.BootstrapCookies && !session.initialized {
		if err := session.bootstrap(ctx); err != nil {
			return nil, err
		}
		if err := session.config.BetweenRequests(ctx); err != nil {
			return nil, err
		}
	}

	body, err := BuildSearchBody(session.config.Region, filter, page)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, session.config.APIURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create Cian API request: %w", err)
	}
	req.Header = apiHeaders(session.identity)

	responseBody, contentType, err := session.do(req)
	if err != nil {
		return nil, err
	}
	if isBlockedAPIResponse(contentType, responseBody) {
		return nil, ErrBlocked
	}
	if !json.Valid(responseBody) {
		return nil, fmt.Errorf("Cian API returned non-JSON response (%s)", contentType)
	}
	offers, err := ParseSearchResponse(responseBody, time.Now())
	if err != nil {
		return nil, err
	}
	return offers, nil
}

func (session *Session) bootstrap(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, session.config.HomeURL, nil)
	if err != nil {
		return fmt.Errorf("create Cian bootstrap request: %w", err)
	}
	req.Header = navigationHeaders(session.identity)
	body, _, err := session.do(req)
	if err != nil {
		return fmt.Errorf("bootstrap Cian cookie session: %w", err)
	}
	if containsCaptcha(body) {
		return ErrBlocked
	}
	session.initialized = true
	return nil
}

func (session *Session) do(req *http.Request) ([]byte, string, error) {
	resp, err := session.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("request Cian: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read Cian response: %w", err)
	}
	if len(body) > maxBodyBytes {
		return nil, "", fmt.Errorf("Cian response exceeds %d bytes", maxBodyBytes)
	}
	contentType := resp.Header.Get("Content-Type")
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, contentType, ErrBlocked
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, contentType, HTTPStatusError{Code: resp.StatusCode}
	}
	return body, contentType, nil
}

func (session *Session) Close() {
	session.client.CloseIdleConnections()
}

// BuildSearchBody builds the internal Cian query documented in the brief.
func BuildSearchBody(region int, filter Filter, page int) ([]byte, error) {
	if region < 1 {
		return nil, errors.New("region must be at least 1")
	}
	if page < 1 {
		return nil, errors.New("page must be at least 1")
	}
	query := map[string]any{
		"region":         map[string]any{"type": "terms", "value": []int{region}},
		"_type":          "flatsale",
		"engine_version": map[string]any{"type": "term", "value": 2},
		"page":           map[string]any{"type": "term", "value": page},
	}
	if filter.Room > 0 {
		query["room"] = map[string]any{"type": "terms", "value": []int{filter.Room}}
	}
	if filter.MinPrice > 0 {
		query["minprice"] = map[string]any{"type": "term", "value": filter.MinPrice}
	}
	if filter.MaxPrice > 0 {
		query["maxprice"] = map[string]any{"type": "term", "value": filter.MaxPrice}
	}
	if filter.MinPrice > 0 && filter.MaxPrice > 0 && filter.MinPrice > filter.MaxPrice {
		return nil, errors.New("minimum price exceeds maximum price")
	}
	return json.Marshal(map[string]any{"jsonQuery": query})
}

func navigationHeaders(identity browserIdentity) http.Header {
	userAgent := chromeUserAgent(identity.version)
	return http.Header{
		"accept":                    {"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8"},
		"accept-language":           {"ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7"},
		"cache-control":             {"max-age=0"},
		"sec-ch-ua":                 {secCHUA(identity.version)},
		"sec-ch-ua-mobile":          {"?0"},
		"sec-ch-ua-platform":        {`"Windows"`},
		"sec-fetch-dest":            {"document"},
		"sec-fetch-mode":            {"navigate"},
		"sec-fetch-site":            {"none"},
		"sec-fetch-user":            {"?1"},
		"upgrade-insecure-requests": {"1"},
		"user-agent":                {userAgent},
		http.HeaderOrderKey: {
			"cache-control", "sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform",
			"upgrade-insecure-requests", "user-agent", "accept", "sec-fetch-site",
			"sec-fetch-mode", "sec-fetch-user", "sec-fetch-dest", "accept-language",
		},
		http.PHeaderOrderKey: {":method", ":authority", ":scheme", ":path"},
	}
}

func apiHeaders(identity browserIdentity) http.Header {
	userAgent := chromeUserAgent(identity.version)
	return http.Header{
		"accept":             {"application/json, text/plain, */*"},
		"accept-language":    {"ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7"},
		"content-type":       {"application/json"},
		"origin":             {"https://www.cian.ru"},
		"referer":            {defaultReferer},
		"sec-ch-ua":          {secCHUA(identity.version)},
		"sec-ch-ua-mobile":   {"?0"},
		"sec-ch-ua-platform": {`"Windows"`},
		"sec-fetch-dest":     {"empty"},
		"sec-fetch-mode":     {"cors"},
		"sec-fetch-site":     {"same-site"},
		"user-agent":         {userAgent},
		http.HeaderOrderKey: {
			"content-type", "sec-ch-ua-platform", "user-agent", "sec-ch-ua", "sec-ch-ua-mobile",
			"accept", "origin", "sec-fetch-site", "sec-fetch-mode", "sec-fetch-dest", "referer", "accept-language",
		},
		http.PHeaderOrderKey: {":method", ":authority", ":scheme", ":path"},
	}
}

func chromeUserAgent(version string) string {
	return fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s.0.0.0 Safari/537.36", version)
}

func secCHUA(version string) string {
	return fmt.Sprintf(`"Not_A Brand";v="99", "Chromium";v="%s", "Google Chrome";v="%s"`, version, version)
}

func isBlockedAPIResponse(contentType string, body []byte) bool {
	if containsCaptcha(body) {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err == nil && (mediaType == "text/html" || mediaType == "application/xhtml+xml") {
		return true
	}
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) > 0 && trimmed[0] == '<'
}

func containsCaptcha(body []byte) bool {
	lower := strings.ToLower(string(body))
	for _, marker := range []string{
		"captcha - база объявлений циан",
		"<title>captcha",
		"smartcaptcha",
		"showcaptcha",
		"qrator",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// HTTPStatusError is retryable for rate limits and server-side failures.
type HTTPStatusError struct {
	Code int
}

func (err HTTPStatusError) Error() string {
	return fmt.Sprintf("Cian returned HTTP %d", err.Code)
}

func IsRetryable(err error) bool {
	if errors.Is(err, ErrBlocked) {
		return true
	}
	var statusErr HTTPStatusError
	if errors.As(err, &statusErr) {
		return statusErr.Code == http.StatusTooManyRequests || statusErr.Code >= 500
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
}
