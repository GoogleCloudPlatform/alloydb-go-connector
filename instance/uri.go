// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package instance

import (
	"fmt"
	"regexp"

	"cloud.google.com/go/alloydbconn/errtype"
)

var (
	// Instance URI is in the format:
	// 'projects/<PROJECT>/locations/<REGION>/clusters/<CLUSTER>/instances/<INSTANCE>'
	// Additionally, we have to support legacy "domain-scoped" projects
	// (e.g. "google.com:PROJECT")
	instURIRegex = regexp.MustCompile("projects/([^:]+(:[^:]+)?)/locations/([^:]+)/clusters/([^:]+)/instances/([^:]+)")

	shortURI = regexp.MustCompile(`([^:]+)\.([^:]+)\.([^:]+)\.([^:]+)`)
)

// URI represents an AlloyDB instance.
type URI struct {
	Project string
	Region  string
	Cluster string
	Name    string
}

// URI returns the full URI specifying an instance.
func (i *URI) URI() string {
	return fmt.Sprintf(
		"projects/%s/locations/%s/clusters/%s/instances/%s",
		i.Project, i.Region, i.Cluster, i.Name,
	)
}

// Parent returns the URI specifying an instance's parent (aka cluster).
func (i *URI) Parent() string {
	return fmt.Sprintf(
		"projects/%s/locations/%s/clusters/%s",
		i.Project, i.Region, i.Cluster,
	)
}

// String returns a short-hand representation of an instance URI.
func (i *URI) String() string {
	return fmt.Sprintf("%s.%s.%s.%s", i.Project, i.Region, i.Cluster, i.Name)
}

// ParseURI initializes a new InstanceURI struct.
func ParseURI(uri string) (URI, error) {
	b := []byte(uri)
	m := instURIRegex.FindSubmatch(b)
	if m == nil {
		m2 := shortURI.FindSubmatch(b)
		if m2 == nil {
			err := errtype.NewConfigError(
				"invalid instance URI, expected "+
					"projects/<PROJECT>/locations/<REGION>/clusters/<CLUSTER>/instances/<INSTANCE> "+
					"or <PROJECT>.<REGION>.<CLUSTER>.<INSTANCE>",
				uri,
			)
			return URI{}, err
		}
		return URI{
			Project: string(m2[1]),
			Region:  string(m2[2]),
			Cluster: string(m2[3]),
			Name:    string(m2[4]),
		}, nil
	}

	c := URI{
		Project: string(m[1]),
		Region:  string(m[3]),
		Cluster: string(m[4]),
		Name:    string(m[5]),
	}
	return c, nil
}
