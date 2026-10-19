package rumourotel_test

import (
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// failingMeter fails to create the instrument kinds named in its flags.
type failingMeter struct {
	noop.Meter
	err                  error
	counter, gauge, hist bool
}

func (f failingMeter) Int64Counter(n string, o ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	if f.counter {
		return nil, f.err
	}
	return f.Meter.Int64Counter(n, o...)
}

func (f failingMeter) Int64Gauge(n string, o ...metric.Int64GaugeOption) (metric.Int64Gauge, error) {
	if f.gauge {
		return nil, f.err
	}
	return f.Meter.Int64Gauge(n, o...)
}

func (f failingMeter) Float64Histogram(n string, o ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	if f.hist {
		return nil, f.err
	}
	return f.Meter.Float64Histogram(n, o...)
}
