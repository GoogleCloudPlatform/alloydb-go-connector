// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package tel_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"

	telv2 "cloud.google.com/go/alloydbconn/internal/tel/v2"
	monitoringpb "cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
)

const bufSize = 4 * 1024 * 1024

type nullLogger struct {
	t *testing.T
}

func (n nullLogger) Debugf(_ context.Context, format string, args ...any) {
	n.t.Logf(format, args...)
}

type mockServer struct {
	mu      sync.Mutex
	gotReqs []*monitoringpb.CreateTimeSeriesRequest
	monitoringpb.MetricServiceServer
}

func (m *mockServer) CreateServiceTimeSeries(_ context.Context, req *monitoringpb.CreateTimeSeriesRequest) (*emptypb.Empty, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gotReqs = append(m.gotReqs, req)
	return &emptypb.Empty{}, nil
}

func equalLabels(want, got map[string]string) bool {
	return cmp.Diff(want, got) == ""
}

func verifyTimeSeries(resourceName, metricType string, resourceLabels, metricLabels map[string]string, tss []*monitoringpb.TimeSeries) bool {
	for _, ts := range tss {
		equalResource := ts.GetResource().GetType() == resourceName
		equalResourceLabels := equalLabels(resourceLabels, ts.GetResource().GetLabels())
		equalMetric := ts.GetMetric().GetType() == metricType
		equalMetricLabels := equalLabels(metricLabels, ts.GetMetric().GetLabels())
		if equalResource && equalResourceLabels && equalMetric && equalMetricLabels {
			return true
		}
	}
	return false
}

func (m *mockServer) Verify(t *testing.T, wantProjectName, wantResourceType, wantMetricType string, wantResourceLabels, wantMetricLabels map[string]string) {
	t.Helper()

	// Try for at least 2s to find the expected request.
	var lastReq *monitoringpb.CreateTimeSeriesRequest
	for range 4 {
		m.mu.Lock()
		gotReqs := m.gotReqs
		m.mu.Unlock()

		for _, req := range gotReqs {
			if req.GetName() == wantProjectName && verifyTimeSeries(wantResourceType, wantMetricType, wantResourceLabels, wantMetricLabels, req.GetTimeSeries()) {
				return
			}
		}
		// Capture last request to attempt a helpful diff on failure.
		if len(gotReqs) > 0 {
			lastReq = gotReqs[len(gotReqs)-1]
		}

		time.Sleep(250 * time.Millisecond)
	}

	gotProjectName := lastReq.GetName()
	if gotProjectName != wantProjectName {
		t.Fatalf("got = %v, want = %v", gotProjectName, wantProjectName)
	}

	var ts *monitoringpb.TimeSeries
	if tss := lastReq.GetTimeSeries(); len(tss) > 0 {
		ts = tss[len(tss)-1]
	}
	gotResourceType := ts.GetResource().GetType()
	if gotResourceType != wantResourceType {
		t.Fatalf("got = %v, want = %v", gotResourceType, wantResourceType)
	}
	gotResourceLabels := ts.GetResource().GetLabels()
	if diff := cmp.Diff(wantResourceLabels, gotResourceLabels); diff != "" {
		t.Fatalf("unexpected diff in resource labels (-want, +got) = %v", diff)
	}
	gotMetricType := ts.GetMetric().GetType()
	if gotMetricType != wantMetricType {
		t.Fatalf("got = %v, want = %v", gotMetricType, wantMetricType)
	}
	gotMetricLabels := ts.GetMetric().GetLabels()
	if diff := cmp.Diff(wantMetricLabels, gotMetricLabels); diff != "" {
		t.Fatalf("unexpected diff in metric labels (-want, +got) = %v", diff)
	}

	t.Fatal("failed to find matching request with unknown diff")

}

func setupMockServer(t *testing.T) (*mockServer, *grpc.ClientConn, func()) {
	t.Helper()

	s := grpc.NewServer()
	mock := &mockServer{}
	monitoringpb.RegisterMetricServiceServer(s, mock)

	lis := bufconn.Listen(bufSize)
	go func() { s.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}

	return mock, conn, func() {
		lis.Close()
	}
}

func TestMetricRecorder(t *testing.T) {
	telv2.DefaultExportInterval = 100 * time.Millisecond
	t.Cleanup(func() { telv2.DefaultExportInterval = 60 * time.Second })
	defaultCfg := telv2.Config{
		Enabled:   true,
		Version:   "1.2.3",
		ClientID:  "some-uid",
		ProjectID: "myproject",
		Location:  "some-location",
		Cluster:   "some-cluster",
		Instance:  "some-instance",
	}
	wantProject := "projects/myproject"
	wantResourceType := "alloydb.googleapis.com/InstanceClient"
	wantResourceLabels := map[string]string{
		"project_id":  "myproject",
		"location":    "some-location",
		"cluster_id":  "some-cluster",
		"instance_id": "some-instance",
		"client_uid":  "some-uid",
	}
	mock, conn, cleanup := setupMockServer(t)
	t.Cleanup(cleanup)

	tcs := []struct {
		desc               string
		cfg                telv2.Config
		attrs              telv2.Attributes
		action             func(context.Context, telv2.MetricRecorder, telv2.Attributes)
		wantProject        string
		wantResourceType   string
		wantResourceLabels map[string]string
		wantMetricType     string
		wantMetricLabels   map[string]string
	}{
		{
			desc: "dial_count",
			cfg:  defaultCfg,
			attrs: telv2.Attributes{
				UserAgent:  "alloydb-go-connector/1.11.0 alloy-db-auth-proxy/1.10.1+container",
				IAMAuthN:   true,
				CacheHit:   true,
				DialStatus: telv2.DialSuccess,
			},
			action: func(ctx context.Context, mr telv2.MetricRecorder, attrs telv2.Attributes) {
				mr.RecordDialCount(ctx, attrs)
			},
			wantProject:        wantProject,
			wantResourceType:   wantResourceType,
			wantResourceLabels: wantResourceLabels,
			wantMetricType:     "alloydb.googleapis.com/client/connector/dial_count",
			wantMetricLabels: map[string]string{
				"connector_type": "auth_proxy",
				"auth_type":      "iam",
				"is_cache_hit":   "true",
				"status":         "success",
			},
		},
		{
			desc: "dial_latencies",
			cfg:  defaultCfg,
			attrs: telv2.Attributes{
				UserAgent: "alloydb-go-connector/1.2.3",
			},
			action: func(ctx context.Context, mr telv2.MetricRecorder, attrs telv2.Attributes) {
				mr.RecordDialLatency(ctx, 1, attrs)
			},
			wantProject:        wantProject,
			wantResourceType:   wantResourceType,
			wantResourceLabels: wantResourceLabels,
			wantMetricType:     "alloydb.googleapis.com/client/connector/dial_latencies",
			wantMetricLabels: map[string]string{
				"connector_type": "go",
			},
		},
		{
			desc: "open_connections (inc)",
			cfg:  defaultCfg,
			attrs: telv2.Attributes{
				UserAgent: "alloydb-go-connector/1.2.3",
				IAMAuthN:  false,
			},
			action: func(ctx context.Context, mr telv2.MetricRecorder, attrs telv2.Attributes) {
				mr.RecordOpenConnection(ctx, attrs)
			},
			wantProject:        wantProject,
			wantResourceType:   wantResourceType,
			wantResourceLabels: wantResourceLabels,
			wantMetricType:     "alloydb.googleapis.com/client/connector/open_connections",
			wantMetricLabels: map[string]string{
				"connector_type": "go",
				"auth_type":      "built_in",
			},
		},
		{
			desc: "open_connections (dec)",
			cfg:  defaultCfg,
			attrs: telv2.Attributes{
				UserAgent: "alloydb-go-connector/1.2.3",
				IAMAuthN:  false,
			},
			action: func(ctx context.Context, mr telv2.MetricRecorder, attrs telv2.Attributes) {
				mr.RecordClosedConnection(ctx, attrs)
			},
			wantProject:        wantProject,
			wantResourceType:   wantResourceType,
			wantResourceLabels: wantResourceLabels,
			wantMetricType:     "alloydb.googleapis.com/client/connector/open_connections",
			wantMetricLabels: map[string]string{
				"connector_type": "go",
				"auth_type":      "built_in",
			},
		},
		{
			desc: "bytes_sent_count",
			cfg:  defaultCfg,
			attrs: telv2.Attributes{
				UserAgent: "alloydb-go-connector/1.2.3",
			},
			action: func(ctx context.Context, mr telv2.MetricRecorder, attrs telv2.Attributes) {
				mr.RecordBytesTxCount(ctx, 1, attrs)
			},
			wantProject:        wantProject,
			wantResourceType:   wantResourceType,
			wantResourceLabels: wantResourceLabels,
			wantMetricType:     "alloydb.googleapis.com/client/connector/bytes_sent_count",
			wantMetricLabels: map[string]string{
				"connector_type": "go",
			},
		},
		{
			desc: "bytes_received_count",
			cfg:  defaultCfg,
			attrs: telv2.Attributes{
				UserAgent: "alloydb-go-connector/1.2.3",
			},
			action: func(ctx context.Context, mr telv2.MetricRecorder, attrs telv2.Attributes) {
				mr.RecordBytesRxCount(ctx, 1, attrs)
			},
			wantProject:        wantProject,
			wantResourceType:   wantResourceType,
			wantResourceLabels: wantResourceLabels,
			wantMetricType:     "alloydb.googleapis.com/client/connector/bytes_received_count",
			wantMetricLabels: map[string]string{
				"connector_type": "go",
			},
		},
		{
			desc: "refresh_count",
			cfg:  defaultCfg,
			attrs: telv2.Attributes{
				UserAgent:     "alloydb-go-connector/1.2.3",
				RefreshStatus: telv2.RefreshSuccess,
				RefreshType:   telv2.RefreshAheadType,
			},
			action: func(ctx context.Context, mr telv2.MetricRecorder, attrs telv2.Attributes) {
				mr.RecordRefreshCount(ctx, attrs)
			},
			wantProject:        wantProject,
			wantResourceType:   wantResourceType,
			wantResourceLabels: wantResourceLabels,
			wantMetricType:     "alloydb.googleapis.com/client/connector/refresh_count",
			wantMetricLabels: map[string]string{
				"connector_type": "go",
				"status":         "success",
				"refresh_type":   "refresh_ahead",
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := context.Background()
			mr := telv2.NewMetricRecorder(ctx, nullLogger{t}, tc.cfg, option.WithGRPCConn(conn))

			tc.action(ctx, mr, tc.attrs)

			mock.Verify(t, tc.wantProject, tc.wantResourceType, tc.wantMetricType, tc.wantResourceLabels, tc.wantMetricLabels)
		})
	}

}
