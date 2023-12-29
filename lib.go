package redditsmm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"go.uber.org/ratelimit"
)

type Option func(option *options) error

type options struct {
	host       string
	rateLimit  *ratelimit.Limiter
	httpClient *http.Client
}

func WithHost(host string) Option {
	return func(option *options) error {
		// Check if host is valid.
		_, err := http.NewRequest("GET", fmt.Sprintf("https://%s", host), nil)
		if err != nil {
			return fmt.Errorf("invalid host: %w", err)
		}

		option.host = host
		return nil
	}
}

func WithRateLimit(rl ratelimit.Limiter) Option {
	return func(option *options) error {
		option.rateLimit = &rl
		return nil
	}
}

func WithHttpClient(hc http.Client) Option {
	return func(option *options) error {
		option.httpClient = &hc
		return nil
	}
}

type Client struct {
	apiKey  string
	options *options
}

func New(apiKey string, opts ...Option) (*Client, error) {
	o := &options{}
	for _, opt := range opts {
		err := opt(o)
		if err != nil {
			return nil, fmt.Errorf("bad option: %w", err)
		}
	}

	if o.host == "" {
		o.host = "redditsmm.com/api/v2"
	}

	if o.rateLimit == nil {
		o.rateLimit = new(ratelimit.Limiter)
		*o.rateLimit = ratelimit.New(10, ratelimit.Per(time.Second))
	}

	if o.httpClient == nil {
		o.httpClient = http.DefaultClient
	}

	return &Client{
		apiKey:  apiKey,
		options: o,
	}, nil
}

type param struct {
	key   string
	value string
}

func (c *Client) buildUrl(p []string) string {
	return fmt.Sprintf("https://%s/%s", c.options.host, path.Join(p...))
}

func (c *Client) buildUrlWithParameters(path []string, params []param) string {
	url := c.buildUrl(path)
	for i, p := range params {
		separator := "&"
		if i == 0 {
			separator = "?"
		}
		url = fmt.Sprintf("%s%s%s=%s", url, separator, p.key, p.value)
	}
	return url
}

func (c *Client) do(req *http.Request) (data []byte, err error) {
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))

	(*c.options.rateLimit).Take()
	res, err := c.options.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("request failed with status code %d", res.StatusCode)
	}

	data, err = io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return data, nil
}

func (c *Client) post(path []string, params []param) (data []byte, err error) {
	url := c.buildUrlWithParameters(path, params)
	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	return c.do(req)
}

// Response and data structures
type Service struct {
	Service  string `json:"service"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Category string `json:"category"`
	Rate     string `json:"rate"`
	Min      string `json:"min"`
	Max      string `json:"max"`
}

type OrderStatus struct {
	Charge     string `json:"charge"`
	StartCount string `json:"start_count"`
	Status     string `json:"status"`
	Remains    string `json:"remains"`
	Currency   string `json:"currency"`
}

type UserBalance struct {
	Balance  string `json:"balance"`
	Currency string `json:"currency"`
}

func (c *Client) UserBalance() (UserBalance, error) {
	params := []param{
		{"key", c.apiKey},
		{"action", "balance"},
	}
	data, err := c.post([]string{}, params)
	if err != nil {
		return UserBalance{}, err
	}

	var response UserBalance
	err = json.Unmarshal(data, &response)
	return response, err
}

func (c *Client) Services() ([]Service, error) {
	params := []param{
		{"key", c.apiKey},
		{"action", "services"},
	}
	data, err := c.post([]string{}, params)
	if err != nil {
		return nil, err
	}

	var response []Service
	err = json.Unmarshal(data, &response)
	return response, err
}

type addOrderOptions struct {
	runs     *int
	interval *int
}

type AddOrderOption func(*addOrderOptions)

func WithRuns(runs int) AddOrderOption {
	return func(option *addOrderOptions) {
		option.runs = &runs
	}
}

func WithInterval(interval int) AddOrderOption {
	return func(option *addOrderOptions) {
		option.interval = &interval
	}
}

func (c *Client) AddOrder(serviceId, link string, quantity int, options ...AddOrderOption) (orderId string, err error) {
	opts := &addOrderOptions{}
	for _, option := range options {
		option(opts)
	}

	params := []param{
		{"key", c.apiKey},
		{"action", "add"},
		{"service", serviceId},
		{"link", link},
		{"quantity", strconv.Itoa(quantity)},
	}

	if opts.runs != nil {
		params = append(params, param{"runs", strconv.Itoa(*opts.runs)})
	}
	if opts.interval != nil {
		params = append(params, param{"interval", strconv.Itoa(*opts.interval)})
	}

	data, err := c.post([]string{}, params)
	if err != nil {
		return "", err
	}

	var response struct {
		Order string `json:"order"`
	}
	err = json.Unmarshal(data, &response)
	return response.Order, err
}

// OrderStatus checks the status of a specific order by its integer ID.
func (c *Client) OrderStatus(orderId string) (OrderStatus, error) {
	params := []param{
		{"key", c.apiKey},
		{"action", "status"},
		{"order", orderId},
	}
	data, err := c.post([]string{}, params)
	if err != nil {
		return OrderStatus{}, err
	}

	var response OrderStatus
	err = json.Unmarshal(data, &response)
	return response, err
}

// MultipleOrdersStatus checks the status of multiple orders given their integer IDs.
func (c *Client) MultipleOrdersStatus(orderIds []string) (map[string]OrderStatus, error) {
	orderIDsString := strings.Join(orderIds, ",")
	params := []param{
		{"key", c.apiKey},
		{"action", "status"},
		{"orders", orderIDsString},
	}
	data, err := c.post([]string{}, params)
	if err != nil {
		return nil, err
	}

	var response map[string]OrderStatus
	err = json.Unmarshal(data, &response)
	return response, err
}
