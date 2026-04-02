#!/bin/bash
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -euo pipefail

command -v go >/dev/null 2>&1 || { echo "go not found. Install from: https://go.dev/dl/" >&2; exit 1; }
command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not found. Install from: https://golangci-lint.run/welcome/install/" >&2; exit 1; }

go mod tidy && git diff --exit-code -- go.mod go.sum
golangci-lint run --timeout 3m
