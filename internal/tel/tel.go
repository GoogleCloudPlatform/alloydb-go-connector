// Copyright 2024 Google LLC
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

/*
- type: alloydb.googleapis.com/internal/connector/dial_latencies
  labels:
  - key: connector_type
    description: AlloyDB Connector type, one of [auth-proxy, go, python, java].

- type: alloydb.googleapis.com/internal/connector/open_connections
  labels:
  - key: connector_type
    description: AlloyDB Connector type, one of [auth-proxy, go, python, java].
  - key: auth_type
    description: Authentication type, one of [native, iam].

- type: alloydb.googleapis.com/internal/connector/dial_count
  labels:
  - key: connector_type
    description: AlloyDB Connector type, one of [auth-proxy, go, python, java].
  - key: auth_type
    description: Authentication type, one of [native, iam].
  - key: is_cache_hit
    description: Whether the certificate cache had a valid certificate, one of [true, false].
  - key: status
    description: Status of request, one of [success, user-error, cache-error, tcp-error, tls-error, mdx-error].

- type: alloydb.googleapis.com/internal/connector/refresh_count
  labels:
  - key: connector_type
    description: AlloyDB Connector type, one of [auth-proxy, go, python, java].
  - key: status
    description: Status of request, one of [success, failure].
  - key: refresh_type
    description: Status of request, one of [refresh-ahead, lazy].

- type: alloydb.googleapis.com/internal/connector/bytes_sent_count
  labels:
  - key: connector_type
    description: AlloyDB Connector type, one of [auth-proxy, go, python, java].

alloydb.googleapis.com/internal/connector/bytes_received_count
  labels:
  - key: connector_type
    description: AlloyDB Connector type, one of [auth-proxy, go, python, java].
*/

package tel

import (
	"context"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

const (
	name = "go.opentelemetry.io/otel/example/dice"

	dialLatency = "alloydb.googleapis.com/internal/connectors/dial_latency"
	openConns   = "alloydb.googleapis.com/internal/connectors/open_connections"
	tx          = "alloydb.googleapis.com/internal/connectors/bytes_sent"
	rx          = "alloydb.googleapis.com/internal/connectors/bytes_received"
)

var (
	meter   = otel.Meter(name)
	rollCnt metric.Int64Counter
)

func init() {
	var err error
	rollCnt, err = meter.Int64Counter("dice.rolls",
		metric.WithDescription("The number of rolls by roll value"),
		metric.WithUnit("{roll}"))
	if err != nil {
		panic(err)
	}
}

func rolldice(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	roll := 1 + rand.Intn(6)

	rollValueAttr := attribute.Int("roll.value", roll)
	rollCnt.Add(ctx, 1, metric.WithAttributes(rollValueAttr))

	resp := strconv.Itoa(roll) + "\n"
	if _, err := io.WriteString(w, resp); err != nil {
		log.Printf("Write failed: %v\n", err)
	}
}

func doMain() {
	// Create resource.
	res, err := newResource()
	if err != nil {
		panic(err)
	}

	// Create a meter provider.
	// You can pass this instance directly to your instrumented code if it
	// accepts a MeterProvider instance.
	meterProvider, err := newMeterProvider(res)
	if err != nil {
		panic(err)
	}

	// Handle shutdown properly so nothing leaks.
	defer func() {
		if err := meterProvider.Shutdown(context.Background()); err != nil {
			log.Println(err)
		}
	}()

	// Register as global meter provider so that it can be used via otel.Meter
	// and accessed using otel.GetMeterProvider.
	// Most instrumentation libraries use the global meter provider as default.
	// If the global meter provider is not set then a no-op implementation
	// is used, which fails to generate data.
	otel.SetMeterProvider(meterProvider)
}

func newResource() (*resource.Resource, error) {
	return resource.Merge(resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL,
			semconv.ServiceName("my-service"),
			semconv.ServiceVersion("0.1.0"),
		))
}

func newMeterProvider(res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	metricExporter, err := stdoutmetric.New()
	if err != nil {
		return nil, err
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter,
			// Default is 1m. Set to 3s for demonstrative purposes.
			sdkmetric.WithInterval(3*time.Second))),
	)
	return meterProvider, nil
}
