package remotewrite

import (
	"math"
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func p(v float64) *float64 { return &v }
func u(v uint64) *uint64   { return &v }

func TestFamiliesToWriteRequestHistogram(t *testing.T) {
	name := "latency_seconds"
	help := "lat"
	typ := dto.MetricType_HISTOGRAM
	le1, le2, leInf := 0.1, 1.0, math.Inf(1)
	c1, c2, cTotal := uint64(2), uint64(5), uint64(7)
	sum := 3.4

	family := &dto.MetricFamily{
		Name: &name,
		Help: &help,
		Type: &typ,
		Metric: []*dto.Metric{{
			Histogram: &dto.Histogram{
				SampleCount: &cTotal,
				SampleSum:   &sum,
				Bucket: []*dto.Bucket{
					{UpperBound: &le1, CumulativeCount: &c1},
					{UpperBound: &le2, CumulativeCount: &c2},
					{UpperBound: &leInf, CumulativeCount: &cTotal},
				},
			},
		}},
	}

	req := familiesToWriteRequest([]*dto.MetricFamily{family}, 1234)
	series := req.Timeseries
	if len(series) != 5 {
		t.Fatalf("expected 5 series, got %d", len(series))
	}

	want := map[string]float64{
		"latency_seconds_bucket/0.1":  2,
		"latency_seconds_bucket/1":    5,
		"latency_seconds_bucket/+Inf": 7,
		"latency_seconds_sum":         3.4,
		"latency_seconds_count":       7,
	}
	got := map[string]float64{}
	for _, ts := range series {
		var key, le string
		hasLE := false
		for _, l := range ts.Labels {
			switch l.Name {
			case "__name__":
				key = l.Value
			case "le":
				le = l.Value
				hasLE = true
			}
		}
		if hasLE {
			got[key+"/"+le] = ts.Samples[0].Value
		} else {
			got[key] = ts.Samples[0].Value
		}
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("series %q: got %v want %v", k, got[k], v)
		}
	}
}

func TestFamiliesToWriteRequestGauge(t *testing.T) {
	name := "temp"
	typ := dto.MetricType_GAUGE
	label := "room"
	labelVal := "kitchen"
	val := 21.5

	family := &dto.MetricFamily{
		Name: &name,
		Type: &typ,
		Metric: []*dto.Metric{{
			Label: []*dto.LabelPair{{Name: &label, Value: &labelVal}},
			Gauge: &dto.Gauge{Value: p(val)},
		}},
	}

	series := familiesToWriteRequest([]*dto.MetricFamily{family}, 1).Timeseries
	if len(series) != 1 {
		t.Fatalf("expected 1 series, got %d", len(series))
	}
	ts := series[0]
	if got := ts.Samples[0].Value; got != 21.5 {
		t.Fatalf("value = %v", got)
	}
	found := map[string]string{}
	for _, l := range ts.Labels {
		found[l.Name] = l.Value
	}
	if found["__name__"] != "temp" || found["room"] != "kitchen" {
		t.Fatalf("labels = %v", found)
	}
}

func TestSummaryNoQuantilesStillEmitsSumCount(t *testing.T) {
	name := "rpc"
	typ := dto.MetricType_SUMMARY
	sum := 1.0
	cnt := uint64(3)
	family := &dto.MetricFamily{
		Name: &name,
		Type: &typ,
		Metric: []*dto.Metric{{
			Summary: &dto.Summary{SampleSum: &sum, SampleCount: &cnt},
		}},
	}
	series := familiesToWriteRequest([]*dto.MetricFamily{family}, 1).Timeseries
	if len(series) != 2 {
		t.Fatalf("expected 2 series, got %d", len(series))
	}
}

func TestLabelsSortedWithNameFirst(t *testing.T) {
	name := "x"
	typ := dto.MetricType_GAUGE
	a, b := "b_label", "a_label"
	av, bv := "1", "2"
	val := 1.0
	family := &dto.MetricFamily{
		Name: &name,
		Type: &typ,
		Metric: []*dto.Metric{{
			Label: []*dto.LabelPair{{Name: &b, Value: &av}, {Name: &a, Value: &bv}},
			Gauge: &dto.Gauge{Value: p(val)},
		}},
	}
	ts := familiesToWriteRequest([]*dto.MetricFamily{family}, 1).Timeseries[0]
	if ts.Labels[0].Name != "__name__" {
		t.Fatalf("__name__ must be first, got %q", ts.Labels[0].Name)
	}
}
