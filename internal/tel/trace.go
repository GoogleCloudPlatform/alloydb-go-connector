// Copyright 2021 Google LLC
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

package tel

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// tracerName is the instrumentation name used for spans created by this
// package.
const tracerName = "cloud.google.com/go/alloydbconn"

// EndSpanFunc is a function that ends a span, reporting an error if necessary.
type EndSpanFunc func(error)

// Attribute annotates a span with additional data.
type Attribute struct {
	key   string
	value string
}

// AddInstanceName creates an attribute with the AlloyDB instance name.
func AddInstanceName(name string) Attribute {
	return Attribute{key: "alloydb.instance", value: name}
}

// AddDialerID creates an attribute to identify a particular dialer.
func AddDialerID(dialerID string) Attribute {
	return Attribute{key: "alloydb.dialer_id", value: dialerID}
}

// StartSpan begins a span with the provided name and returns a context and a
// function to end the created span. The span is created via the global
// OpenTelemetry TracerProvider so that any tracer registered by the caller
// will receive it.
func StartSpan(ctx context.Context, name string, attrs ...Attribute) (context.Context, EndSpanFunc) {
	tracer := otel.GetTracerProvider().Tracer(tracerName)
	ctx, span := tracer.Start(ctx, name)
	if len(attrs) > 0 {
		kvs := make([]attribute.KeyValue, 0, len(attrs))
		for _, a := range attrs {
			kvs = append(kvs, attribute.String(a.key, a.value))
		}
		span.SetAttributes(kvs...)
	}
	return ctx, func(err error) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}
}
