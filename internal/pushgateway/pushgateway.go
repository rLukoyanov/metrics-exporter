package pushgateway

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"

	"metrics-generator/internal/token"
)

const (
	DefaultURL       = "http://localhost:9091"
	DefaultTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
)

type Config struct {
	URL           string
	TokenPath     string
	SkipTLSVerify bool
	Timeout       time.Duration
}

func ConfigFromEnv() Config {
	cfg := Config{
		URL:       strings.TrimRight(envOr("PUSHGATEWAY_URL", DefaultURL), "/"),
		TokenPath: envOr("SA_TOKEN_PATH", DefaultTokenPath),
		Timeout:   10 * time.Second,
	}
	if v := os.Getenv("PUSHGATEWAY_SKIP_TLS_VERIFY"); v == "1" || strings.EqualFold(v, "true") {
		cfg.SkipTLSVerify = true
	}
	if v := os.Getenv("PUSHGATEWAY_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Timeout = d
		}
	}
	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type Label struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Bucket struct {
	LE    float64 `json:"le"`
	Count uint64  `json:"count"`
}

type Quantile struct {
	Quantile float64 `json:"quantile"`
	Value    float64 `json:"value"`
}

type Metric struct {
	Name      string     `json:"name"`
	Help      string     `json:"help"`
	Type      string     `json:"type"`
	Labels    []Label    `json:"labels"`
	Value     *float64   `json:"value"`
	Buckets   []Bucket   `json:"buckets"`
	Quantiles []Quantile `json:"quantiles"`
	Sum       *float64   `json:"sum"`
	Count     *uint64    `json:"count"`
}

type Request struct {
	Mode           string   `json:"mode,omitempty"`
	Job            string   `json:"job"`
	GroupingLabels []Label  `json:"groupingLabels"`
	Metrics        []Metric `json:"metrics"`
}

type Pusher struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) *Pusher {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
	}
	if cfg.SkipTLSVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	return &Pusher{
		cfg: cfg,
		client: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: transport,
		},
	}
}

func (p *Pusher) URL() string { return p.cfg.URL }

func (p *Pusher) TokenPresent() bool {
	return token.Present(p.cfg.TokenPath)
}

func (p *Pusher) Push(ctx context.Context, req Request) error {
	if strings.TrimSpace(req.Job) == "" {
		return fmt.Errorf("job is required")
	}
	if len(req.Metrics) == 0 {
		return fmt.Errorf("at least one metric is required")
	}

	var buf bytes.Buffer
	enc := expfmt.NewEncoder(&buf, expfmt.NewFormat(expfmt.TypeTextPlain))
	for i, m := range req.Metrics {
		family, err := buildFamily(m)
		if err != nil {
			return fmt.Errorf("metric %d: %w", i, err)
		}
		if err := enc.Encode(family); err != nil {
			return fmt.Errorf("encode metric %d: %w", i, err)
		}
	}

	endpoint := p.metricsURL(req.Job, req.GroupingLabels)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, &buf)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", string(expfmt.FmtText))

	if token, err := p.readToken(); err != nil {
		return err
	} else if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("push to %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("pushgateway returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (p *Pusher) readToken() (string, error) {
	raw, err := token.Read(p.cfg.TokenPath)
	if err != nil {
		return "", fmt.Errorf("read service account token: %w", err)
	}
	return strings.TrimSpace(raw), nil
}

// BuildFamilies converts a push request into Prometheus metric families,
// shared by all transport implementations.
func BuildFamilies(req Request) ([]*dto.MetricFamily, error) {
	if len(req.Metrics) == 0 {
		return nil, fmt.Errorf("at least one metric is required")
	}
	families := make([]*dto.MetricFamily, 0, len(req.Metrics))
	for i, m := range req.Metrics {
		family, err := buildFamily(m)
		if err != nil {
			return nil, fmt.Errorf("metric %d: %w", i, err)
		}
		families = append(families, family)
	}
	return families, nil
}

func (p *Pusher) metricsURL(job string, grouping []Label) string {
	parts := []string{"job", job}
	for _, l := range grouping {
		if strings.TrimSpace(l.Name) == "" {
			continue
		}
		parts = append(parts, l.Name, encodeLabelValue(l.Value))
	}
	return p.cfg.URL + "/metrics/" + strings.Join(parts, "/")
}

// encodeLabelValue uses the pushgateway base64 convention for values that
// cannot be represented safely in a URL path segment.
func encodeLabelValue(v string) string {
	if v == "" || strings.ContainsAny(v, "/@") {
		return "@base64=" + base64.URLEncoding.EncodeToString([]byte(v))
	}
	return url.PathEscape(v)
}

func buildFamily(m Metric) (*dto.MetricFamily, error) {
	name := strings.TrimSpace(m.Name)
	if !validMetricName(name) {
		return nil, fmt.Errorf("invalid metric name %q", m.Name)
	}

	typ, err := metricType(m.Type)
	if err != nil {
		return nil, err
	}

	labels, err := labelPairs(m.Labels)
	if err != nil {
		return nil, err
	}

	help := m.Help
	if help == "" {
		help = name
	}

	dm := &dto.Metric{Label: labels}
	switch typ {
	case dto.MetricType_COUNTER:
		if m.Value == nil {
			return nil, fmt.Errorf("counter requires a value")
		}
		dm.Counter = &dto.Counter{Value: m.Value}
	case dto.MetricType_GAUGE:
		if m.Value == nil {
			return nil, fmt.Errorf("gauge requires a value")
		}
		dm.Gauge = &dto.Gauge{Value: m.Value}
	case dto.MetricType_HISTOGRAM:
		h, err := buildHistogram(m)
		if err != nil {
			return nil, err
		}
		dm.Histogram = h
	case dto.MetricType_SUMMARY:
		s, err := buildSummary(m)
		if err != nil {
			return nil, err
		}
		dm.Summary = s
	}

	return &dto.MetricFamily{
		Name:   &name,
		Help:   &help,
		Type:   &typ,
		Metric: []*dto.Metric{dm},
	}, nil
}

func buildHistogram(m Metric) (*dto.Histogram, error) {
	if len(m.Buckets) == 0 {
		return nil, fmt.Errorf("histogram requires buckets")
	}
	buckets := make([]Bucket, len(m.Buckets))
	copy(buckets, m.Buckets)
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].LE < buckets[j].LE })

	h := &dto.Histogram{}
	var prev uint64
	for _, b := range buckets {
		if math.IsInf(b.LE, 1) {
			continue // +Inf bucket is appended below
		}
		if b.Count < prev {
			return nil, fmt.Errorf("histogram bucket counts must be cumulative (le=%v)", b.LE)
		}
		ub := b.LE
		cnt := b.Count
		h.Bucket = append(h.Bucket, &dto.Bucket{UpperBound: &ub, CumulativeCount: &cnt})
		prev = b.Count
	}

	total := prev
	if m.Count != nil {
		if *m.Count < prev {
			return nil, fmt.Errorf("histogram count %d is less than last cumulative bucket %d", *m.Count, prev)
		}
		total = *m.Count
	}
	inf := math.Inf(1)
	infCount := total
	h.Bucket = append(h.Bucket, &dto.Bucket{UpperBound: &inf, CumulativeCount: &infCount})

	if m.Sum != nil {
		h.SampleSum = m.Sum
	} else {
		zero := 0.0
		h.SampleSum = &zero
	}
	h.SampleCount = &total
	return h, nil
}

func buildSummary(m Metric) (*dto.Summary, error) {
	if len(m.Quantiles) == 0 {
		return nil, fmt.Errorf("summary requires quantiles")
	}
	s := &dto.Summary{}
	for _, q := range m.Quantiles {
		if q.Quantile < 0 || q.Quantile > 1 {
			return nil, fmt.Errorf("quantile must be between 0 and 1, got %v", q.Quantile)
		}
		qq := q.Quantile
		vv := q.Value
		s.Quantile = append(s.Quantile, &dto.Quantile{Quantile: &qq, Value: &vv})
	}
	sort.Slice(s.Quantile, func(i, j int) bool { return s.Quantile[i].GetQuantile() < s.Quantile[j].GetQuantile() })

	if m.Sum != nil {
		s.SampleSum = m.Sum
	} else {
		zero := 0.0
		s.SampleSum = &zero
	}
	if m.Count != nil {
		s.SampleCount = m.Count
	} else {
		var zero uint64
		s.SampleCount = &zero
	}
	return s, nil
}

func labelPairs(labels []Label) ([]*dto.LabelPair, error) {
	seen := map[string]struct{}{}
	pairs := make([]*dto.LabelPair, 0, len(labels))
	for _, l := range labels {
		name := strings.TrimSpace(l.Name)
		if name == "" {
			continue
		}
		if !validLabelName(name) {
			return nil, fmt.Errorf("invalid label name %q", l.Name)
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("duplicate label %q", name)
		}
		seen[name] = struct{}{}
		n, v := name, l.Value
		pairs = append(pairs, &dto.LabelPair{Name: &n, Value: &v})
	}
	return pairs, nil
}

func metricType(t string) (dto.MetricType, error) {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "counter":
		return dto.MetricType_COUNTER, nil
	case "gauge":
		return dto.MetricType_GAUGE, nil
	case "histogram":
		return dto.MetricType_HISTOGRAM, nil
	case "summary":
		return dto.MetricType_SUMMARY, nil
	default:
		return dto.MetricType_UNTYPED, fmt.Errorf("unsupported metric type %q", t)
	}
}

func validMetricName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == ':' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func validLabelName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}
