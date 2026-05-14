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

// Package tel provides tracing for the connector's internal operations using
// OpenTelemetry. The sibling v2 package provides metrics via OpenTelemetry.
//
// This package also continues to emit the connector's original
// OpenCensus-based metrics so that existing dashboards and alerts can
// continue to consume them while operators migrate to OpenTelemetry.
// The OpenCensus emission is scheduled for removal in the release after the
// next.
package tel
