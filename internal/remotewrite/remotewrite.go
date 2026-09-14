package remotewrite

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang/snappy"

	dto "github.com/prometheus/client_model/go"

	"metrics-generator/internal/token"
)

const (
	DefaultTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	WriteEndpoint    = "/api/v1/write"

	contentType     = "application/x-protobuf"
	contentEncoding = "snappy"
	remoteWriteVer  = "0.1.0"
)

type Config struct {
	URL           string
	TokenPath     string
	SkipTLSVerify bool
	Timeout       time.Duration
}

func ConfigFromEnv() Config {
	cfg := Config{
		URL:       strings.TrimRight(os.Getenv("REMOTE_WRITE_URL"), "/"),
		TokenPath: envOr("SA_TOKEN_PATH", DefaultTokenPath),
		Timeout:   10 * time.Second,
	}
	if v := os.Getenv("REMOTE_WRITE_SKIP_TLS_VERIFY"); v == "1" || strings.EqualFold(v, "true") {
		cfg.SkipTLSVerify = true
	}
	if v := os.Getenv("REMOTE_WRITE_TIMEOUT"); v != "" {
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

func (p *Pusher) Enabled() bool { return p.cfg.URL != "" }

func (p *Pusher) TokenPresent() bool {
	return token.Present(p.cfg.TokenPath)
}

func (p *Pusher) Push(ctx context.Context, families []*dto.MetricFamily) error {
	if !p.Enabled() {
		return fmt.Errorf("remote write is not configured (REMOTE_WRITE_URL is empty)")
	}

	req := familiesToWriteRequest(families, time.Now().UnixMilli())
	raw, err := req.Marshal()
	if err != nil {
		return fmt.Errorf("marshal write request: %w", err)
	}
	body := snappy.Encode(nil, raw)

	endpoint := p.cfg.URL + WriteEndpoint
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("Content-Encoding", contentEncoding)
	httpReq.Header.Set("X-Prometheus-Remote-Write-Version", remoteWriteVer)

	if rawToken, err := token.Read(p.cfg.TokenPath); err != nil {
		return fmt.Errorf("read service account token: %w", err)
	} else if t := strings.TrimSpace(rawToken); t != "" {
		httpReq.Header.Set("Authorization", "Bearer "+t)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("remote write to %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	bodyResp, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("remote write returned %s: %s", resp.Status, strings.TrimSpace(string(bodyResp)))
	}
	return nil
}
