package pushgateway

import (
	"math"
	"strings"
	"testing"

	"github.com/prometheus/common/expfmt"
)

func f(v float64) *float64 { return &v }

func TestMetricsURL(t *testing.T) {
	p := New(Config{URL: "http://pg:9091"})

	got := p.metricsURL("my-job", []Label{{Name: "instance", Value: "host-1"}})
	want := "http://pg:9091/metrics/job/my-job/instance/host-1"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	got = p.metricsURL("j", []Label{{Name: "empty", Value: ""}})
	want = "http://pg:9091/metrics/job/j/empty/@base64="
	if got != want {
		t.Fatalf("empty label: got %q want %q", got, want)
	}
}

func TestBuildGauge(t *testing.T) {
	family, err := buildFamily(Metric{
		Name:   "temp_celsius",
		Type:   "gauge",
		Value:  f(21.5),
		Labels: []Label{{Name: "room", Value: "kitchen"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if family.GetType().String() != "GAUGE" {
		t.Fatalf("unexpected type %v", family.GetType())
	}
	if got := family.Metric[0].Gauge.GetValue(); got != 21.5 {
		t.Fatalf("value = %v", got)
	}
}

func TestBuildHistogramAddsInfBucket(t *testing.T) {
	family, err := buildFamily(Metric{
		Name:    "latency_seconds",
		Type:    "histogram",
		Buckets: []Bucket{{LE: 0.1, Count: 2}, {LE: 1, Count: 5}},
		Sum:     f(3.4),
	})
	if err != nil {
		t.Fatal(err)
	}
	h := family.Metric[0].Histogram
	if len(h.Bucket) != 3 {
		t.Fatalf("expected 3 buckets, got %d", len(h.Bucket))
	}
	if h.Bucket[2].GetUpperBound() != math.Inf(1) {
		t.Fatalf("last bucket upper bound = %v", h.Bucket[2].GetUpperBound())
	}
	if h.GetSampleCount() != 5 {
		t.Fatalf("sample count = %d", h.GetSampleCount())
	}
}

func TestEncodeCounter(t *testing.T) {
	family, err := buildFamily(Metric{Name: "requests_total", Type: "counter", Value: f(42)})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := expfmt.NewEncoder(&b, expfmt.NewFormat(expfmt.TypeTextPlain)).Encode(family); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "requests_total 42") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestInvalidType(t *testing.T) {
	if _, err := buildFamily(Metric{Name: "x", Type: "nope", Value: f(1)}); err == nil {
		t.Fatal("expected error for invalid type")
	}
}
