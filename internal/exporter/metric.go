// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package exporter is a modified version of
// github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric.
//
// It is meant to write system metrics soley for the
// alloydb.googleapis.com/InstanceClient resource type and provides an
// implemtation of OpenTelemetry's metric.Exporter's interface.
package exporter

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/api/option"
	"google.golang.org/genproto/googleapis/api/distribution"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	googlemetricpb "google.golang.org/genproto/googleapis/api/metric"
	monitoredrespb "google.golang.org/genproto/googleapis/api/monitoredres"
)

const (
	builtInMetricsMeterName = "alloydb.googleapis.com/client/connectors"
	// The number of timeserieses to send to GCM in a single request. This
	// is a hard limit in the GCM API, so we never want to exceed 200.
	sendBatchSize = 200
	// resource labels
	ProjectID = "project_id"
	Location  = "location"
	Cluster   = "cluster_id"
	Instance  = "instance_id"
	ClientID  = "client_uid"
)

var (
	// resourceLabelSet identifies the labels that must be attached to the
	// monitored resource and not the metric.
	resourceLabelSet = map[string]bool{
		ProjectID: true,
		Location:  true,
		Cluster:   true,
		Instance:  true,
		ClientID:  true,
	}
)

// Ensure MetricExporter adhers to metric.Exporter interface
var _ metric.Exporter = (*MetricExporter)(nil)

// MetricExporter is the implementation of OpenTelemetry's
// go.opentelemetry.io/otel/sdk/metric.Exporter interface for Google Cloud
// Monitoring.
type MetricExporter struct {
	shutdown     chan struct{}
	client       *monitoring.MetricClient
	shutdownOnce sync.Once
	projectID    string
}

// NewMetricExporter returns an exporter that uploads OTel metric data to
// Google Cloud Monitoring.
func NewMetricExporter(ctx context.Context, projectID string, opts ...option.ClientOption) (*MetricExporter, error) {
	client, err := monitoring.NewMetricClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	e := &MetricExporter{
		client:    client,
		shutdown:  make(chan struct{}),
		projectID: projectID,
	}
	return e, nil
}

// Temporality returns the Temporality to use for an instrument kind.
func (me *MetricExporter) Temporality(ik metric.InstrumentKind) metricdata.Temporality {
	return metric.DefaultTemporalitySelector(ik)
}

// Aggregation returns the Aggregation to use for an instrument kind.
func (me *MetricExporter) Aggregation(ik metric.InstrumentKind) metric.Aggregation {
	return metric.DefaultAggregationSelector(ik)
}

// ForceFlush does nothing, the exporter holds no state.
func (e *MetricExporter) ForceFlush(ctx context.Context) error { return ctx.Err() }

var errShutdown = fmt.Errorf("exporter is shutdown")

// Shutdown shuts down the client connections.
func (e *MetricExporter) Shutdown(ctx context.Context) error {
	err := errShutdown
	e.shutdownOnce.Do(func() {
		close(e.shutdown)
		err = errors.Join(ctx.Err(), e.client.Close())
	})
	return err
}

// Export exports OpenTelemetry Metrics to Google Cloud Monitoring.
func (me *MetricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	select {
	case <-me.shutdown:
		return errShutdown
	default:
	}
	return me.exportTimeSeries(ctx, rm)
}

// exportTimeSeries create TimeSeries from the records in cps.
// res should be the common resource among all TimeSeries, such as instance id, application name and so on.
func (me *MetricExporter) exportTimeSeries(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	tss, err := me.recordsToTspbs(rm)
	if len(tss) == 0 {
		return err
	}

	name := fmt.Sprintf("projects/%s", me.projectID)

	errs := []error{err}
	for i := 0; i < len(tss); i += sendBatchSize {
		j := i + sendBatchSize
		if j >= len(tss) {
			j = len(tss)
		}

		// TODO: When this exporter is rewritten, support writing to multiple
		// projects based on the "gcp.project.id" resource.
		req := &monitoringpb.CreateTimeSeriesRequest{
			Name:       name,
			TimeSeries: tss[i:j],
		}
		errs = append(errs, me.client.CreateServiceTimeSeries(ctx, req))
	}

	return errors.Join(errs...)
}

type errUnexpectedAggregationKind struct {
	kind string
}

func (e errUnexpectedAggregationKind) Error() string {
	return fmt.Sprintf("the metric kind is unexpected: %v", e.kind)
}

// recordToTspb converts record to TimeSeries proto type with common resource.
// ref. https://cloud.google.com/monitoring/api/ref_v3/rest/v3/TimeSeries
func (me *MetricExporter) recordToTspb(m metricdata.Metrics) ([]*monitoringpb.TimeSeries, error) {
	var tss []*monitoringpb.TimeSeries
	var errs []error
	if m.Data == nil {
		return nil, nil
	}
	switch a := m.Data.(type) {
	case metricdata.Gauge[int64]:
		for _, point := range a.DataPoints {
			metric, mr := me.recordToMetricAndMonitoredResourcePbs(m, point.Attributes)
			ts, err := gaugeToTimeSeries[int64](point, m, mr)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			ts.Metric = metric
			tss = append(tss, ts)
		}
	case metricdata.Gauge[float64]:
		for _, point := range a.DataPoints {
			metric, mr := me.recordToMetricAndMonitoredResourcePbs(m, point.Attributes)
			ts, err := gaugeToTimeSeries[float64](point, m, mr)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			ts.Metric = metric
			tss = append(tss, ts)
		}
	case metricdata.Sum[int64]:
		for _, point := range a.DataPoints {
			var ts *monitoringpb.TimeSeries
			var err error
			metric, mr := me.recordToMetricAndMonitoredResourcePbs(m, point.Attributes)
			if a.IsMonotonic {
				ts, err = sumToTimeSeries[int64](point, m, mr)
			} else {
				// Send non-monotonic sums as gauges
				ts, err = gaugeToTimeSeries[int64](point, m, mr)
			}
			if err != nil {
				errs = append(errs, err)
				continue
			}
			ts.Metric = metric
			tss = append(tss, ts)
		}
	case metricdata.Sum[float64]:
		for _, point := range a.DataPoints {
			var ts *monitoringpb.TimeSeries
			var err error
			metric, mr := me.recordToMetricAndMonitoredResourcePbs(m, point.Attributes)
			if a.IsMonotonic {
				ts, err = sumToTimeSeries[float64](point, m, mr)
			} else {
				// Send non-monotonic sums as gauges
				ts, err = gaugeToTimeSeries[float64](point, m, mr)
			}
			if err != nil {
				errs = append(errs, err)
				continue
			}
			ts.Metric = metric
			tss = append(tss, ts)
		}
	case metricdata.Histogram[int64]:
		for _, point := range a.DataPoints {
			metric, mr := me.recordToMetricAndMonitoredResourcePbs(m, point.Attributes)
			ts, err := histogramToTimeSeries(point, m, mr, me.projectID)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			ts.Metric = metric
			tss = append(tss, ts)
		}
	case metricdata.Histogram[float64]:
		for _, point := range a.DataPoints {
			metric, mr := me.recordToMetricAndMonitoredResourcePbs(m, point.Attributes)
			ts, err := histogramToTimeSeries(point, m, mr, me.projectID)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			ts.Metric = metric
			tss = append(tss, ts)
		}
	case metricdata.ExponentialHistogram[int64]:
		for _, point := range a.DataPoints {
			metric, mr := me.recordToMetricAndMonitoredResourcePbs(m, point.Attributes)
			ts, err := expHistogramToTimeSeries(point, m, mr, me.projectID)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			ts.Metric = metric
			tss = append(tss, ts)
		}
	case metricdata.ExponentialHistogram[float64]:
		for _, point := range a.DataPoints {
			metric, mr := me.recordToMetricAndMonitoredResourcePbs(m, point.Attributes)
			ts, err := expHistogramToTimeSeries(point, m, mr, me.projectID)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			ts.Metric = metric
			tss = append(tss, ts)
		}
	default:
		errs = append(errs, errUnexpectedAggregationKind{kind: reflect.TypeOf(m.Data).String()})
	}
	return tss, errors.Join(errs...)
}

// recordToMetricAndMonitoredResourcePbs converts data from records to Metric
// and Monitored resource proto type for Cloud Monitoring.
func (me *MetricExporter) recordToMetricAndMonitoredResourcePbs(metrics metricdata.Metrics, attributes attribute.Set) (*googlemetricpb.Metric, *monitoredrespb.MonitoredResource) {
	resourceLabels := make(map[string]string)
	metricLabels := make(map[string]string)

	iter := attributes.Iter()
	for iter.Next() {
		kv := iter.Attribute()
		k := string(kv.Key)

		// When the label is a resource label, add it to the resource labels.
		// Otherwise, add it to the metric label.
		if _, ok := resourceLabelSet[k]; ok {
			resourceLabels[k] = kv.Value.Emit()
		} else {
			metricLabels[k] = kv.Value.Emit()

		}
	}
	metric := &googlemetricpb.Metric{
		Type:   fmt.Sprintf("%v/%v", builtInMetricsMeterName, metrics.Name),
		Labels: metricLabels,
	}
	mr := &monitoredrespb.MonitoredResource{
		Type:   "alloydb.googleapis.com/InstanceClient",
		Labels: resourceLabels,
	}
	return metric, mr
}

func (me *MetricExporter) recordsToTspbs(rm *metricdata.ResourceMetrics) ([]*monitoringpb.TimeSeries, error) {
	var (
		tss  []*monitoringpb.TimeSeries
		errs []error
	)
	for _, scope := range rm.ScopeMetrics {
		for _, metrics := range scope.Metrics {
			if scope.Scope.Name != builtInMetricsMeterName {
				// Filter out metric data for instruments that are not part of the
				// bigtable builtin metrics
				continue
			}
			ts, err := me.recordToTspb(metrics)
			errs = append(errs, err)
			tss = append(tss, ts...)
		}
	}

	return tss, errors.Join(errs...)
}

func sanitizeUTF8(s string) string {
	return strings.ToValidUTF8(s, "�")
}

func gaugeToTimeSeries[N int64 | float64](point metricdata.DataPoint[N], metrics metricdata.Metrics, mr *monitoredrespb.MonitoredResource) (*monitoringpb.TimeSeries, error) {
	value, valueType := numberDataPointToValue(point)
	timestamp := timestamppb.New(point.Time)
	if err := timestamp.CheckValid(); err != nil {
		return nil, err
	}
	return &monitoringpb.TimeSeries{
		Resource:   mr,
		Unit:       string(metrics.Unit),
		MetricKind: googlemetricpb.MetricDescriptor_GAUGE,
		ValueType:  valueType,
		Points: []*monitoringpb.Point{{
			Interval: &monitoringpb.TimeInterval{
				EndTime: timestamp,
			},
			Value: value,
		}},
	}, nil
}

func sumToTimeSeries[N int64 | float64](point metricdata.DataPoint[N], metrics metricdata.Metrics, mr *monitoredrespb.MonitoredResource) (*monitoringpb.TimeSeries, error) {
	interval, err := toNonemptyTimeIntervalpb(point.StartTime, point.Time)
	if err != nil {
		return nil, err
	}
	value, valueType := numberDataPointToValue[N](point)
	return &monitoringpb.TimeSeries{
		Resource:   mr,
		Unit:       string(metrics.Unit),
		MetricKind: googlemetricpb.MetricDescriptor_CUMULATIVE,
		ValueType:  valueType,
		Points: []*monitoringpb.Point{{
			Interval: interval,
			Value:    value,
		}},
	}, nil
}

func histogramToTimeSeries[N int64 | float64](point metricdata.HistogramDataPoint[N], metrics metricdata.Metrics, mr *monitoredrespb.MonitoredResource, projectID string) (*monitoringpb.TimeSeries, error) {
	interval, err := toNonemptyTimeIntervalpb(point.StartTime, point.Time)
	if err != nil {
		return nil, err
	}
	distributionValue := histToDistribution(point, projectID)
	return &monitoringpb.TimeSeries{
		Resource:   mr,
		Unit:       string(metrics.Unit),
		MetricKind: googlemetricpb.MetricDescriptor_CUMULATIVE,
		ValueType:  googlemetricpb.MetricDescriptor_DISTRIBUTION,
		Points: []*monitoringpb.Point{{
			Interval: interval,
			Value: &monitoringpb.TypedValue{
				Value: &monitoringpb.TypedValue_DistributionValue{
					DistributionValue: distributionValue,
				},
			},
		}},
	}, nil
}

func expHistogramToTimeSeries[N int64 | float64](point metricdata.ExponentialHistogramDataPoint[N], metrics metricdata.Metrics, mr *monitoredrespb.MonitoredResource, projectID string) (*monitoringpb.TimeSeries, error) {
	interval, err := toNonemptyTimeIntervalpb(point.StartTime, point.Time)
	if err != nil {
		return nil, err
	}
	distributionValue := expHistToDistribution(point, projectID)
	return &monitoringpb.TimeSeries{
		Resource:   mr,
		Unit:       string(metrics.Unit),
		MetricKind: googlemetricpb.MetricDescriptor_CUMULATIVE,
		ValueType:  googlemetricpb.MetricDescriptor_DISTRIBUTION,
		Points: []*monitoringpb.Point{{
			Interval: interval,
			Value: &monitoringpb.TypedValue{
				Value: &monitoringpb.TypedValue_DistributionValue{
					DistributionValue: distributionValue,
				},
			},
		}},
	}, nil
}

func toNonemptyTimeIntervalpb(start, end time.Time) (*monitoringpb.TimeInterval, error) {
	// The end time of a new interval must be at least a millisecond after the end time of the
	// previous interval, for all non-gauge types.
	// https://cloud.google.com/monitoring/api/ref_v3/rpc/google.monitoring.v3#timeinterval
	if end.Sub(start).Milliseconds() <= 1 {
		end = start.Add(time.Millisecond)
	}
	startpb := timestamppb.New(start)
	endpb := timestamppb.New(end)
	err := errors.Join(
		startpb.CheckValid(),
		endpb.CheckValid(),
	)
	if err != nil {
		return nil, err
	}

	return &monitoringpb.TimeInterval{
		StartTime: startpb,
		EndTime:   endpb,
	}, nil
}

func histToDistribution[N int64 | float64](hist metricdata.HistogramDataPoint[N], projectID string) *distribution.Distribution {
	counts := make([]int64, len(hist.BucketCounts))
	for i, v := range hist.BucketCounts {
		counts[i] = int64(v)
	}
	var mean float64
	if !math.IsNaN(float64(hist.Sum)) && hist.Count > 0 { // Avoid divide-by-zero
		mean = float64(hist.Sum) / float64(hist.Count)
	}
	return &distribution.Distribution{
		Count:        int64(hist.Count),
		Mean:         mean,
		BucketCounts: counts,
		BucketOptions: &distribution.Distribution_BucketOptions{
			Options: &distribution.Distribution_BucketOptions_ExplicitBuckets{
				ExplicitBuckets: &distribution.Distribution_BucketOptions_Explicit{
					Bounds: hist.Bounds,
				},
			},
		},
		Exemplars: toDistributionExemplar[N](hist.Exemplars),
	}
}

func expHistToDistribution[N int64 | float64](hist metricdata.ExponentialHistogramDataPoint[N], projectID string) *distribution.Distribution {
	// First calculate underflow bucket with all negatives + zeros.
	underflow := hist.ZeroCount
	negativeBuckets := hist.NegativeBucket.Counts
	for i := 0; i < len(negativeBuckets); i++ {
		underflow += negativeBuckets[i]
	}

	// Next, pull in remaining buckets.
	counts := make([]int64, len(hist.PositiveBucket.Counts)+2)
	bucketOptions := &distribution.Distribution_BucketOptions{}
	counts[0] = int64(underflow)
	positiveBuckets := hist.PositiveBucket.Counts
	for i := 0; i < len(positiveBuckets); i++ {
		counts[i+1] = int64(positiveBuckets[i])
	}
	// Overflow bucket is always empty
	counts[len(counts)-1] = 0

	if len(hist.PositiveBucket.Counts) == 0 {
		// We cannot send exponential distributions with no positive buckets,
		// instead we send a simple overflow/underflow histogram.
		bucketOptions.Options = &distribution.Distribution_BucketOptions_ExplicitBuckets{
			ExplicitBuckets: &distribution.Distribution_BucketOptions_Explicit{
				Bounds: []float64{0},
			},
		}
	} else {
		// Exponential histogram
		growth := math.Exp2(math.Exp2(-float64(hist.Scale)))
		scale := math.Pow(growth, float64(hist.PositiveBucket.Offset))
		bucketOptions.Options = &distribution.Distribution_BucketOptions_ExponentialBuckets{
			ExponentialBuckets: &distribution.Distribution_BucketOptions_Exponential{
				GrowthFactor:     growth,
				Scale:            scale,
				NumFiniteBuckets: int32(len(counts) - 2),
			},
		}
	}

	var mean float64
	if !math.IsNaN(float64(hist.Sum)) && hist.Count > 0 { // Avoid divide-by-zero
		mean = float64(hist.Sum) / float64(hist.Count)
	}

	return &distribution.Distribution{
		Count:         int64(hist.Count),
		Mean:          mean,
		BucketCounts:  counts,
		BucketOptions: bucketOptions,
		Exemplars:     toDistributionExemplar[N](hist.Exemplars),
	}
}

func toDistributionExemplar[N int64 | float64](Exemplars []metricdata.Exemplar[N]) []*distribution.Distribution_Exemplar {
	var exemplars []*distribution.Distribution_Exemplar
	for _, e := range Exemplars {
		attachments := []*anypb.Any{}
		if len(e.FilteredAttributes) > 0 {
			attr, err := anypb.New(&monitoringpb.DroppedLabels{
				Label: attributesToLabels(e.FilteredAttributes),
			})
			if err == nil {
				attachments = append(attachments, attr)
			}
		}
		exemplars = append(exemplars, &distribution.Distribution_Exemplar{
			Value:       float64(e.Value),
			Timestamp:   timestamppb.New(e.Time),
			Attachments: attachments,
		})
	}
	sort.Slice(exemplars, func(i, j int) bool {
		return exemplars[i].Value < exemplars[j].Value
	})
	return exemplars
}

func attributesToLabels(attrs []attribute.KeyValue) map[string]string {
	labels := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		labels[normalizeLabelKey(string(attr.Key))] = sanitizeUTF8(attr.Value.Emit())
	}
	return labels
}

var (
	nilTraceID trace.TraceID
	nilSpanID  trace.SpanID
)

func numberDataPointToValue[N int64 | float64](
	point metricdata.DataPoint[N],
) (*monitoringpb.TypedValue, googlemetricpb.MetricDescriptor_ValueType) {
	switch v := any(point.Value).(type) {
	case int64:
		return &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{
				Int64Value: v,
			}},
			googlemetricpb.MetricDescriptor_INT64
	case float64:
		return &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{
				DoubleValue: v,
			}},
			googlemetricpb.MetricDescriptor_DOUBLE
	}
	// It is impossible to reach this statement
	return nil, googlemetricpb.MetricDescriptor_INT64
}

// https://github.com/googleapis/googleapis/blob/c4c562f89acce603fb189679836712d08c7f8584/google/api/metric.proto#L149
//
// > The label key name must follow:
// >
// > * Only upper and lower-case letters, digits and underscores (_) are
// >   allowed.
// > * Label name must start with a letter or digit.
// > * The maximum length of a label name is 100 characters.
//
//	Note: this does not truncate if a label is too long.
func normalizeLabelKey(s string) string {
	if len(s) == 0 {
		return s
	}
	s = strings.Map(sanitizeRune, s)
	if unicode.IsDigit(rune(s[0])) {
		s = "key_" + s
	}
	return s
}

// converts anything that is not a letter or digit to an underscore.
func sanitizeRune(r rune) rune {
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return r
	}
	// Everything else turns into an underscore
	return '_'
}
