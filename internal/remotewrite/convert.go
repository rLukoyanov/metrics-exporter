package remotewrite

import (
	"strconv"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/prompb"
)

func familiesToWriteRequest(families []*dto.MetricFamily, nowMS int64) *prompb.WriteRequest {
	var series []prompb.TimeSeries
	for _, fam := range families {
		series = append(series, familyTimeSeries(fam, nowMS)...)
	}
	return &prompb.WriteRequest{Timeseries: series}
}

func familyTimeSeries(fam *dto.MetricFamily, nowMS int64) []prompb.TimeSeries {
	var out []prompb.TimeSeries
	baseName := fam.GetName()

	for _, m := range fam.Metric {
		base := make([]prompb.Label, 0, len(m.Label)+1)
		for _, lp := range m.Label {
			base = append(base, prompb.Label{Name: lp.GetName(), Value: lp.GetValue()})
		}

		push := func(name string, extra []prompb.Label, v float64) {
			labels := make([]prompb.Label, 0, len(base)+len(extra)+1)
			labels = append(labels, prompb.Label{Name: "__name__", Value: name})
			labels = append(labels, base...)
			labels = append(labels, extra...)
			out = append(out, prompb.TimeSeries{
				Labels:  labels,
				Samples: []prompb.Sample{{Value: v, Timestamp: nowMS}},
			})
		}

		switch fam.GetType() {
		case dto.MetricType_COUNTER:
			push(baseName, nil, m.GetCounter().GetValue())
		case dto.MetricType_GAUGE:
			push(baseName, nil, m.GetGauge().GetValue())
		case dto.MetricType_HISTOGRAM:
			h := m.GetHistogram()
			for _, b := range h.GetBucket() {
				push(baseName+"_bucket",
					[]prompb.Label{{Name: "le", Value: formatFloat(b.GetUpperBound())}},
					float64(b.GetCumulativeCount()))
			}
			push(baseName+"_sum", nil, h.GetSampleSum())
			push(baseName+"_count", nil, float64(h.GetSampleCount()))
		case dto.MetricType_SUMMARY:
			s := m.GetSummary()
			for _, q := range s.GetQuantile() {
				push(baseName,
					[]prompb.Label{{Name: "quantile", Value: formatFloat(q.GetQuantile())}},
					q.GetValue())
			}
			push(baseName+"_sum", nil, s.GetSampleSum())
			push(baseName+"_count", nil, float64(s.GetSampleCount()))
		}
	}
	return out
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}
