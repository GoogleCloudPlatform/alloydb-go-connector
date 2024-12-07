package main

import (
	"context"
	"log"
	"time"

	"cloud.google.com/go/alloydbconn"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/genproto/googleapis/api/metric"
	"google.golang.org/genproto/googleapis/api/monitoredres"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

func main() {
	if err := metricTest(); err != nil {
		log.Fatal(err)
	}
}

func metricTest() error {
	ctx := context.Background()
	client, err := monitoring.NewMetricClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	req := &monitoringpb.CreateTimeSeriesRequest{
		Name: "projects/enocom-experiments-304623",
		TimeSeries: []*monitoringpb.TimeSeries{
			&monitoringpb.TimeSeries{
				Metric: &metric.Metric{
					Type: "alloydb.googleapis.com/internal/connector/dial_count",
					Labels: map[string]string{
						"auth_type":      "native",
						"connector_type": "go",
						"is_cache_hit":   "false",
						"status":         "success",
					},
				},
				Resource: &monitoredres.MonitoredResource{
					Type: "gcm_580867343844.InternalInstanceNode",
					Labels: map[string]string{
                        "project_id":"enocom-experiments-304623",

						"location":    "us-central1",
						"cluster_id":  "enocom-cluster",
						"instance_id": "enocom-primary",

						"zone": "us-central1-a",

						"instance_type": "PRIMARY",
						"node_id":       "unknown",
						"service":       "alloydb.googleapis.com",
					},
				},
				MetricKind: metric.MetricDescriptor_CUMULATIVE,
				ValueType:  metric.MetricDescriptor_INT64,
				Points: []*monitoringpb.Point{
					&monitoringpb.Point{
						Interval: &monitoringpb.TimeInterval{
							StartTime: &timestamppb.Timestamp{
								Seconds: 1733427250,
								Nanos:   569108512,
							},
							EndTime: &timestamppb.Timestamp{
								Seconds: 1733427253,
								Nanos:   571053348,
							},
						},
						Value: &monitoringpb.TypedValue{
							Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 1},
						},
					},
				},
			},
		},
	}
	return client.CreateServiceTimeSeries(ctx, req)
}

const instanceURI = "projects/enocom-experiments-304623/locations/us-central1/clusters/enocom-cluster/instances/enocom-primary"

func connectorTest() {
	d, err := alloydbconn.NewDialer(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	conn, err := d.Dial(context.Background(), instanceURI, alloydbconn.WithPublicIP())
	if err != nil {
		log.Println("FAILURE sleeping for 90 seconds...")
		time.Sleep(90 * time.Second)
		log.Fatal(err)
	}

	log.Println("sleeping for 90 seconds...")
	time.Sleep(90 * time.Second)

	err = conn.Close()
	if err != nil {
		log.Fatal(err)
	}
}
