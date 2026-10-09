// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package alloydbconn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	alloydbadmin "cloud.google.com/go/alloydb/apiv1alpha"
	"cloud.google.com/go/alloydb/data/apiv1beta/datapb"
	"cloud.google.com/go/alloydbconn/errtype"
	"cloud.google.com/go/alloydbconn/internal/alloydb"
	"cloud.google.com/go/alloydbconn/internal/mock"
	"cloud.google.com/go/alloydbconn/internal/sqldataclient"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type fakeAlloyDBDataServiceServer struct {
	datapb.UnimplementedAlloyDbDataServiceServer
	onStreamSQLData func(datapb.AlloyDbDataService_StreamSqlDataServer) error
}

func (f *fakeAlloyDBDataServiceServer) StreamSqlData(stream datapb.AlloyDbDataService_StreamSqlDataServer) error {
	if f.onStreamSQLData != nil {
		return f.onStreamSQLData(stream)
	}
	return nil
}

func startMockServer(t *testing.T, handler func(datapb.AlloyDbDataService_StreamSqlDataServer) error) (string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer(
		grpc.MaxRecvMsgSize(100*1024*1024),
		grpc.MaxSendMsgSize(100*1024*1024),
	)
	fakeServer := &fakeAlloyDBDataServiceServer{
		onStreamSQLData: handler,
	}
	datapb.RegisterAlloyDbDataServiceServer(s, fakeServer)
	go func() {
		_ = s.Serve(lis)
	}()
	return lis.Addr().String(), func() { s.Stop() }
}

func newTestDialer(ctx context.Context, addr string, dialTimeout time.Duration, extraOpts ...Option) (*Dialer, error) {
	opts := []Option{
		WithTokenSource(stubTokenSource{}),
		WithOptOutOfBuiltInTelemetry(),
		WithDefaultDialOptions(WithSQLData()),
		WithSQLDataDialer(sqldataclient.NewGrpcDialer(addr, nil, "", nullLogger{}, true, dialTimeout, "")),
	}
	opts = append(opts, extraOpts...)
	return NewDialer(ctx, opts...)
}

func TestDialerWithSqlData(t *testing.T) {
	handler := func(stream datapb.AlloyDbDataService_StreamSqlDataServer) error {
		req, err := stream.Recv()
		if err != nil {
			return err
		}
		if req.GetStartSession() == nil {
			return fmt.Errorf("expected StartSession, got %v", req)
		}
		if req.GetStartSession().GetAuthenticationType() != datapb.StartSession_DATABASE_AUTHENTICATION_TYPE_UNSPECIFIED {
			return fmt.Errorf("expected DATABASE_AUTHENTICATION_TYPE_UNSPECIFIED auth type, got %v", req.GetStartSession().GetAuthenticationType())
		}
		for {
			req, err := stream.Recv()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			data := req.GetData()
			if data != nil {
				err := stream.Send(&datapb.StreamSqlDataResponse{
					Message: &datapb.StreamSqlDataResponse_Data{
						Data: &datapb.DataPacket{
							Data: data.GetData(),
						},
					},
				})
				if err != nil {
					return err
				}
			}
		}
	}

	addr, cleanup := startMockServer(t, handler)
	defer cleanup()

	ctx := context.Background()
	d, err := newTestDialer(ctx, addr, 45*time.Minute)
	if err != nil {
		t.Fatalf("NewDialer failed: %v", err)
	}
	defer d.Close()

	conn, err := d.Dial(ctx, testInstanceURI, WithSQLData())
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	msg := []byte("hello world")
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if string(buf) != string(msg) {
		t.Errorf("Got %q, want %q", string(buf), string(msg))
	}
}

func TestDialerWithSqlData_IAMAuthN(t *testing.T) {
	tcs := []struct {
		desc         string
		dialerOpts   []Option
		dialOpts     []DialOption
		wantAuthType datapb.StartSession_DatabaseAuthenticationType
	}{
		{
			desc:         "dialer WithIAMAuthN sets AUTO_IAM_AUTHENTICATION",
			dialerOpts:   []Option{WithIAMAuthN()},
			dialOpts:     []DialOption{WithSQLData()},
			wantAuthType: datapb.StartSession_AUTO_IAM_AUTHENTICATION,
		},
		{
			desc:         "dial WithDialIAMAuthN(true) sets AUTO_IAM_AUTHENTICATION",
			dialerOpts:   nil,
			dialOpts:     []DialOption{WithSQLData(), WithDialIAMAuthN(true)},
			wantAuthType: datapb.StartSession_AUTO_IAM_AUTHENTICATION,
		},
		{
			desc:         "dialer WithIAMAuthN overridden by WithDialIAMAuthN(false)",
			dialerOpts:   []Option{WithIAMAuthN()},
			dialOpts:     []DialOption{WithSQLData(), WithDialIAMAuthN(false)},
			wantAuthType: datapb.StartSession_DATABASE_AUTHENTICATION_TYPE_UNSPECIFIED,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			handler := func(stream datapb.AlloyDbDataService_StreamSqlDataServer) error {
				req, err := stream.Recv()
				if err != nil {
					return err
				}
				ss := req.GetStartSession()
				if ss == nil {
					return fmt.Errorf("expected StartSession, got %v", req)
				}
				if ss.GetAuthenticationType() != tc.wantAuthType {
					return fmt.Errorf("expected %v, got %v", tc.wantAuthType, ss.GetAuthenticationType())
				}
				return stream.Send(&datapb.StreamSqlDataResponse{
					Message: &datapb.StreamSqlDataResponse_Data{
						Data: &datapb.DataPacket{
							Data: []byte("iam-ok"),
						},
					},
				})
			}

			addr, cleanup := startMockServer(t, handler)
			defer cleanup()

			ctx := context.Background()
			d, err := newTestDialer(ctx, addr, 45*time.Minute, tc.dialerOpts...)
			if err != nil {
				t.Fatalf("NewDialer failed: %v", err)
			}
			defer d.Close()

			conn, err := d.Dial(ctx, testInstanceURI, tc.dialOpts...)
			if err != nil {
				t.Fatalf("Dial failed: %v", err)
			}
			defer conn.Close()

			buf := make([]byte, 6)
			if _, err := io.ReadFull(conn, buf); err != nil {
				t.Fatalf("Read failed: %v", err)
			}
			if string(buf) != "iam-ok" {
				t.Errorf("Got %q, want %q", string(buf), "iam-ok")
			}
		})
	}
}

func TestDialerWithSqlData_ConnectionFailure(t *testing.T) {
	handler := func(_ datapb.AlloyDbDataService_StreamSqlDataServer) error {
		return fmt.Errorf("simulated connection failure")
	}
	addr, cleanup := startMockServer(t, handler)
	defer cleanup()

	ctx := context.Background()
	d, err := newTestDialer(ctx, addr, 45*time.Minute)
	if err != nil {
		t.Fatalf("NewDialer failed: %v", err)
	}
	defer d.Close()

	conn, err := d.Dial(ctx, testInstanceURI, WithSQLData())
	if err != nil {
		return
	}
	defer conn.Close()

	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("Read expected error, got nil")
	}
}

func TestDialerWithSqlData_StreamInterruption(t *testing.T) {
	handler := func(stream datapb.AlloyDbDataService_StreamSqlDataServer) error {
		if _, err := stream.Recv(); err != nil {
			return err
		}
		req, err := stream.Recv()
		if err != nil {
			return err
		}
		if data := req.GetData(); data != nil {
			err := stream.Send(&datapb.StreamSqlDataResponse{
				Message: &datapb.StreamSqlDataResponse_Data{
					Data: &datapb.DataPacket{
						Data: data.GetData(),
					},
				},
			})
			if err != nil {
				return err
			}
		}
		return fmt.Errorf("simulated interruption")
	}
	addr, cleanup := startMockServer(t, handler)
	defer cleanup()

	ctx := context.Background()
	d, err := newTestDialer(ctx, addr, 45*time.Minute)
	if err != nil {
		t.Fatalf("NewDialer failed: %v", err)
	}
	defer d.Close()

	conn, err := d.Dial(ctx, testInstanceURI, WithSQLData())
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	msg := []byte("hello")
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("Read expected error, got nil")
	}
}

func TestDialerWithSqlData_ClientCancellation(t *testing.T) {
	handler := func(stream datapb.AlloyDbDataService_StreamSqlDataServer) error {
		for {
			req, err := stream.Recv()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			data := req.GetData()
			if data != nil {
				err := stream.Send(&datapb.StreamSqlDataResponse{
					Message: &datapb.StreamSqlDataResponse_Data{
						Data: &datapb.DataPacket{
							Data: data.GetData(),
						},
					},
				})
				if err != nil {
					return err
				}
			}
		}
	}
	addr, cleanup := startMockServer(t, handler)
	defer cleanup()

	ctx := context.Background()
	d, err := newTestDialer(ctx, addr, 45*time.Minute)
	if err != nil {
		t.Fatalf("NewDialer failed: %v", err)
	}
	defer d.Close()

	dialCtx, cancel := context.WithCancel(context.Background())
	conn, err := d.Dial(dialCtx, testInstanceURI, WithSQLData())
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	cancel()

	msg := []byte("hello")
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("Write failed after cancel: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("Read failed after cancel: %v", err)
	}
	if string(buf) != string(msg) {
		t.Errorf("Got %q, want %q", string(buf), string(msg))
	}
}

func TestDialerWithSqlData_ConcurrentConnections(t *testing.T) {
	handler := func(stream datapb.AlloyDbDataService_StreamSqlDataServer) error {
		for {
			req, err := stream.Recv()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			data := req.GetData()
			if data != nil {
				err := stream.Send(&datapb.StreamSqlDataResponse{
					Message: &datapb.StreamSqlDataResponse_Data{
						Data: &datapb.DataPacket{
							Data: data.GetData(),
						},
					},
				})
				if err != nil {
					return err
				}
			}
		}
	}
	addr, cleanup := startMockServer(t, handler)
	defer cleanup()

	ctx := context.Background()
	d, err := newTestDialer(ctx, addr, 45*time.Minute)
	if err != nil {
		t.Fatalf("NewDialer failed: %v", err)
	}
	defer d.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := d.Dial(ctx, testInstanceURI, WithSQLData())
			if err != nil {
				t.Errorf("Dial failed: %v", err)
				return
			}
			defer conn.Close()

			msg := []byte("hello")
			if _, err := conn.Write(msg); err != nil {
				t.Errorf("Write failed: %v", err)
				return
			}
			buf := make([]byte, len(msg))
			if _, err := io.ReadFull(conn, buf); err != nil {
				t.Errorf("Read failed: %v", err)
				return
			}
			if string(buf) != string(msg) {
				t.Errorf("Got %q, want %q", string(buf), string(msg))
			}
		}()
	}
	wg.Wait()
}

func TestDialerWithSqlData_HeadersAndSequence(t *testing.T) {
	errCh := make(chan error, 1)
	handler := func(stream datapb.AlloyDbDataService_StreamSqlDataServer) error {
		defer close(errCh)
		md, ok := metadata.FromIncomingContext(stream.Context())
		if !ok {
			err := fmt.Errorf("no metadata")
			errCh <- err
			return err
		}

		userAgents := md.Get("user-agent")
		foundUA := false
		for _, ua := range userAgents {
			if strings.Contains(ua, "userAgent") {
				foundUA = true
				break
			}
		}
		if !foundUA {
			err := fmt.Errorf("metadata user-agent mismatch: got %v, want to contain userAgent", userAgents)
			errCh <- err
			return err
		}

		params := md.Get("x-goog-request-params")
		wantParam := fmt.Sprintf("location_id=locations/my-region&name=%s", testInstanceURI)
		foundParam := false
		for _, p := range params {
			if strings.Contains(p, wantParam) {
				foundParam = true
				break
			}
		}
		if !foundParam {
			err := fmt.Errorf("metadata x-goog-request-params mismatch: got %v, want %q", params, wantParam)
			errCh <- err
			return err
		}

		req, err := stream.Recv()
		if err != nil {
			errCh <- err
			return err
		}
		settings := req.GetStartSession()
		if settings == nil {
			err := fmt.Errorf("expected StartSession in first message, got %v", req)
			errCh <- err
			return err
		}

		if settings.GetLocationId() != "locations/my-region" {
			err := fmt.Errorf("StartSession location_id mismatch: got %q, want locations/my-region", settings.GetLocationId())
			errCh <- err
			return err
		}
		if settings.GetName() != testInstanceURI {
			err := fmt.Errorf("StartSession name mismatch: got %q, want %q", settings.GetName(), testInstanceURI)
			errCh <- err
			return err
		}

		return nil
	}
	addr, cleanup := startMockServer(t, handler)
	defer cleanup()

	ctx := context.Background()
	d, err := NewDialer(ctx,
		WithQuotaProject("quotaProj"),
		WithTokenSource(stubTokenSource{}),
		WithOptOutOfBuiltInTelemetry(),
		WithDefaultDialOptions(WithSQLData()),
		WithSQLDataDialer(sqldataclient.NewGrpcDialer(addr, nil, "quotaProj", nullLogger{}, true, 45*time.Minute, "userAgent")),
	)
	if err != nil {
		t.Fatalf("NewDialer failed: %v", err)
	}
	defer d.Close()

	conn, err := d.Dial(ctx, testInstanceURI, WithSQLData())
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("Server verification failed: %v", err)
	}
	conn.Close()
}

func TestDialerWithSqlData_LargePayload(t *testing.T) {
	payloadSize := 10 * 1024 * 1024
	largeData := make([]byte, payloadSize)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	handler := func(stream datapb.AlloyDbDataService_StreamSqlDataServer) error {
		if _, err := stream.Recv(); err != nil {
			return err
		}
		for {
			req, err := stream.Recv()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			data := req.GetData()
			if data != nil {
				err := stream.Send(&datapb.StreamSqlDataResponse{
					Message: &datapb.StreamSqlDataResponse_Data{
						Data: &datapb.DataPacket{
							Data: data.GetData(),
						},
					},
				})
				if err != nil {
					return err
				}
			}
		}
	}
	addr, cleanup := startMockServer(t, handler)
	defer cleanup()

	ctx := context.Background()
	d, err := newTestDialer(ctx, addr, 45*time.Minute)
	if err != nil {
		t.Fatalf("NewDialer failed: %v", err)
	}
	defer d.Close()

	conn, err := d.Dial(ctx, testInstanceURI, WithSQLData())
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	go func() {
		if _, err := conn.Write(largeData); err != nil {
			t.Errorf("Write failed: %v", err)
		}
	}()

	recvBuf := make([]byte, payloadSize)
	if _, err := io.ReadFull(conn, recvBuf); err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	for i := range recvBuf {
		if recvBuf[i] != largeData[i] {
			t.Fatalf("Received data mismatch at index %d", i)
		}
	}
}

func TestDialerWithSqlDataTimeout(t *testing.T) {
	handler := func(stream datapb.AlloyDbDataService_StreamSqlDataServer) error {
		_, _ = stream.Recv()
		time.Sleep(5 * time.Second)
		return nil
	}

	addr, cleanup := startMockServer(t, handler)
	defer cleanup()

	ctx := context.Background()
	dialTimeout := 500 * time.Millisecond
	d, err := newTestDialer(ctx, addr, dialTimeout)
	if err != nil {
		t.Fatalf("NewDialer failed: %v", err)
	}
	defer d.Close()

	conn, err := d.Dial(ctx, testInstanceURI, WithSQLData())
	if err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "deadline") {
			t.Fatalf("expected deadline exceeded error during dial, got: %v", err)
		}
		return
	}
	defer conn.Close()

	_, err = conn.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("expected Read to fail due to timeout, but it succeeded")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "deadline") {
		t.Fatalf("expected deadline exceeded error during read, got: %v", err)
	}
}

func TestDialerWithSqlData_DialCancellation(t *testing.T) {
	handler := func(_ datapb.AlloyDbDataService_StreamSqlDataServer) error {
		return nil
	}
	addr, cleanup := startMockServer(t, handler)
	defer cleanup()

	ctx := context.Background()
	d, err := newTestDialer(ctx, addr, 45*time.Minute)
	if err != nil {
		t.Fatalf("NewDialer failed: %v", err)
	}
	defer d.Close()

	dialCtx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = d.Dial(dialCtx, testInstanceURI, WithSQLData())
	if err == nil {
		t.Fatal("Dial expected error with cancelled context, got nil")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("Expected context canceled error, got: %v", err)
	}
}

func TestDialerWithSqlData_FallbackToDirectIP(t *testing.T) {
	var grpcCalls int
	var mu sync.Mutex
	handler := func(stream datapb.AlloyDbDataService_StreamSqlDataServer) error {
		mu.Lock()
		grpcCalls++
		mu.Unlock()
		if _, err := stream.Recv(); err != nil {
			return err
		}
		return status.Error(codes.FailedPrecondition, "unsupported instance edition")
	}
	addr, grpcCleanup := startMockServer(t, handler)
	defer grpcCleanup()

	ctx := context.Background()
	inst := mock.NewFakeInstance(
		"my-project", "my-region", "my-cluster", "my-instance",
	)
	mc, url, httpCleanup := mock.HTTPClient(
		mock.InstanceGetSuccess(inst, 1),
		mock.CreateEphemeralSuccess(inst, 1),
	)
	stopProxy := mock.StartServerProxy(t, inst)
	defer func() {
		stopProxy()
		if err := httpCleanup(); err != nil {
			t.Fatalf("%v", err)
		}
	}()

	c, err := alloydbadmin.NewAlloyDBAdminRESTClient(
		ctx, option.WithHTTPClient(mc), option.WithEndpoint(url))
	if err != nil {
		t.Fatalf("expected NewClient to succeed, but got error: %v", err)
	}

	d, err := newTestDialer(ctx, addr, 45*time.Minute)
	if err != nil {
		t.Fatalf("NewDialer failed: %v", err)
	}
	d.client = c
	defer d.Close()

	// 1st Dial: starts via AlloyDbDataService, falls back on first Read to direct IP
	conn1, err := d.Dial(ctx, testInstanceURI, WithSQLData())
	if err != nil {
		t.Fatalf("Dial 1 failed: %v", err)
	}
	defer conn1.Close()

	data1, err := io.ReadAll(conn1)
	if err != nil {
		t.Fatalf("ReadAll 1 failed: %v", err)
	}
	if string(data1) != "my-instance" {
		t.Fatalf("got %q, want %q", string(data1), "my-instance")
	}

	// 2nd Dial: should bypass AlloyDbDataService because state.allowed is now false
	conn2, err := d.Dial(ctx, testInstanceURI, WithSQLData())
	if err != nil {
		t.Fatalf("Dial 2 failed: %v", err)
	}
	defer conn2.Close()

	data2, err := io.ReadAll(conn2)
	if err != nil {
		t.Fatalf("ReadAll 2 failed: %v", err)
	}
	if string(data2) != "my-instance" {
		t.Fatalf("got %q, want %q", string(data2), "my-instance")
	}

	mu.Lock()
	defer mu.Unlock()
	if grpcCalls != 1 {
		t.Errorf("expected exactly 1 call to AlloyDbDataService before sticky fallback, got %d", grpcCalls)
	}
}

func TestDialerWithSqlData_ResourceExhaustedCooldown(t *testing.T) {
	var callCount int
	var mu sync.Mutex
	handler := func(stream datapb.AlloyDbDataService_StreamSqlDataServer) error {
		mu.Lock()
		callCount++
		mu.Unlock()
		if _, err := stream.Recv(); err != nil {
			return err
		}
		return status.Error(codes.ResourceExhausted, "resource busy")
	}
	addr, cleanup := startMockServer(t, handler)
	defer cleanup()

	ctx := context.Background()
	cooldown := 500 * time.Millisecond
	d, err := newTestDialer(ctx, addr, 45*time.Minute, WithResourceExhaustedCooldownPeriod(cooldown))
	if err != nil {
		t.Fatalf("NewDialer failed: %v", err)
	}
	defer d.Close()

	conn, err := d.Dial(ctx, testInstanceURI, WithSQLData())
	if err == nil {
		defer conn.Close()
		_, err = conn.Read(make([]byte, 1))
	}
	if err == nil {
		t.Fatal("expected Dial or Read to fail")
	}
	if !strings.Contains(err.Error(), "resource busy") {
		t.Fatalf("expected resource busy error, got: %v", err)
	}

	mu.Lock()
	if callCount != 1 {
		t.Errorf("expected 1 call to server, got %d", callCount)
	}
	mu.Unlock()

	_, err = d.Dial(ctx, testInstanceURI, WithSQLData())
	if err == nil {
		t.Fatal("expected Dial to fail during cooldown")
	}
	var reErr *errtype.ResourceExhaustedError
	if !errors.As(err, &reErr) {
		t.Fatalf("expected ResourceExhaustedError, got: %T (%v)", err, err)
	}
	if !strings.Contains(err.Error(), "cooldown active") {
		t.Errorf("expected cooldown active message, got: %v", err)
	}

	mu.Lock()
	if callCount != 1 {
		t.Errorf("expected still 1 call to server (cooldown should prevent call), got %d", callCount)
	}
	mu.Unlock()

	time.Sleep(2 * cooldown)

	conn2, err := d.Dial(ctx, testInstanceURI, WithSQLData())
	if err == nil {
		defer conn2.Close()
		_, err = conn2.Read(make([]byte, 1))
	}
	if err == nil {
		t.Fatal("expected Dial or Read to fail after cooldown")
	}
	if errors.As(err, &reErr) {
		t.Fatalf("expected server error, got cooldown error: %v", err)
	}
	if !strings.Contains(err.Error(), "resource busy") {
		t.Fatalf("expected resource busy error from server, got: %v", err)
	}

	mu.Lock()
	if callCount != 2 {
		t.Errorf("expected 2 calls to server after cooldown, got %d", callCount)
	}
	mu.Unlock()
}

func TestDialerWithSqlData_ResourceExhaustedBackoff(t *testing.T) {
	var succeed bool
	var succeedMu sync.Mutex
	handler := func(stream datapb.AlloyDbDataService_StreamSqlDataServer) error {
		req, err := stream.Recv()
		if err != nil {
			return err
		}
		if req.GetStartSession() == nil {
			return fmt.Errorf("expected StartSession, got %v", req)
		}

		succeedMu.Lock()
		s := succeed
		succeedMu.Unlock()

		if !s {
			return status.Error(codes.ResourceExhausted, "resource busy")
		}

		for {
			req, err := stream.Recv()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			data := req.GetData()
			if data != nil {
				err := stream.Send(&datapb.StreamSqlDataResponse{
					Message: &datapb.StreamSqlDataResponse_Data{
						Data: &datapb.DataPacket{
							Data: data.GetData(),
						},
					},
				})
				if err != nil {
					return err
				}
			}
		}
	}
	addr, cleanup := startMockServer(t, handler)
	defer cleanup()

	ctx := context.Background()
	cooldown := 500 * time.Millisecond
	d, err := newTestDialer(ctx, addr, 45*time.Minute, WithResourceExhaustedCooldownPeriod(cooldown))
	if err != nil {
		t.Fatalf("NewDialer failed: %v", err)
	}
	defer d.Close()

	inst, _ := alloydb.ParseInstURI(testInstanceURI)

	getState := func() *sqlDataConnState {
		d.lock.RLock()
		state := d.sqlDataConnState[inst]
		d.lock.RUnlock()
		return state
	}

	state := getState()
	if state != nil {
		t.Fatalf("expected state to be nil before first dial, got: %+v", state)
	}

	conn, err := d.Dial(ctx, testInstanceURI, WithSQLData())
	if err != nil {
		t.Fatalf("first Dial failed: %v", err)
	}
	_, err = conn.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("expected Read to fail")
	}
	conn.Close()

	state = getState()
	if state == nil {
		t.Fatal("expected state to be initialized after failure")
	}
	state.mu.Lock()
	counter := state.backoffCounter
	cooldownUntil := state.cooldownUntil
	state.mu.Unlock()

	if counter != 1 {
		t.Errorf("expected backoffCounter to be 1, got: %d", counter)
	}
	firstCooldownDuration := time.Until(cooldownUntil)
	if firstCooldownDuration < 400*time.Millisecond || firstCooldownDuration > 900*time.Millisecond {
		t.Errorf("expected first cooldown duration to be ~500ms-809ms, got: %v", firstCooldownDuration)
	}

	_, err = d.Dial(ctx, testInstanceURI, WithSQLData())
	if err == nil {
		t.Fatal("expected Dial to fail during cooldown")
	}

	time.Sleep(firstCooldownDuration + 50*time.Millisecond)

	conn2, err := d.Dial(ctx, testInstanceURI, WithSQLData())
	if err != nil {
		t.Fatalf("second Dial failed: %v", err)
	}
	_, err = conn2.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("expected second Read to fail")
	}
	conn2.Close()

	state.mu.Lock()
	counter = state.backoffCounter
	cooldownUntil = state.cooldownUntil
	state.mu.Unlock()

	if counter != 2 {
		t.Errorf("expected backoffCounter to be 2, got: %d", counter)
	}
	secondCooldownDuration := time.Until(cooldownUntil)
	time.Sleep(secondCooldownDuration + 50*time.Millisecond)

	succeedMu.Lock()
	succeed = true
	succeedMu.Unlock()

	conn3, err := d.Dial(ctx, testInstanceURI, WithSQLData())
	if err != nil {
		t.Fatalf("third Dial failed: %v", err)
	}
	defer conn3.Close()

	want := []byte("test")
	if _, err := conn3.Write(want); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn3, got); err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	state.mu.Lock()
	counter = state.backoffCounter
	cooldownUntil = state.cooldownUntil
	state.mu.Unlock()

	if counter != 0 {
		t.Errorf("expected backoffCounter to be reset to 0, got: %d", counter)
	}
	if !cooldownUntil.IsZero() {
		t.Errorf("expected cooldownUntil to be cleared (zero time), got: %v", cooldownUntil)
	}
}
