package instance_test

import (
	"testing"

	"cloud.google.com/go/alloydbconn/instance"
)


func TestParseInstURI(t *testing.T) {
	tcs := []struct {
		desc string
		in   string
		want instance.URI
	}{
		{
			desc: "vanilla instance URI",
			in:   "projects/proj/locations/reg/clusters/clust/instances/name",
			want: instance.URI{
				Project: "proj",
				Region:  "reg",
				Cluster: "clust",
				Name:    "name",
			},
		},
		{
			desc: "with legacy domain-scoped project",
			in:   "projects/google.com:proj/locations/reg/clusters/clust/instances/name",
			want: instance.URI{
				Project: "google.com:proj",
				Region:  "reg",
				Cluster: "clust",
				Name:    "name",
			},
		},
		{
			desc: "with psuedo-DNS style",
			in:   "proj.reg.clust.name",
			want: instance.URI{
				Project: "proj",
				Region:  "reg",
				Cluster: "clust",
				Name:    "name",
			},
		},
		{
			desc: "with psuedo-DNS style and legacy domain-scoped project",
			in:   "google.com:proj.reg.clust.name",
			want: instance.URI{
				Project: "proj",
				Region:  "reg",
				Cluster: "clust",
				Name:    "name",
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			got, err := instance.ParseURI(tc.in)
			if err != nil {
				t.Fatalf("got = %v, want no error", err)
			}
			if got != tc.want {
				t.Fatalf("got = %v, want = %v", got, tc.want)
			}
		})
	}
}

func TestParseConnNameErrors(t *testing.T) {
	tcs := []struct {
		desc string
		in   string
	}{
		{
			desc: "malformatted",
			in:   "not-correct",
		},
		{
			desc: "missing project",
			in:   "reg:clust:name",
		},
		{
			desc: "missing cluster",
			in:   "proj:reg:name",
		},
		{
			desc: "empty",
			in:   "::::",
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := instance.ParseURI(tc.in)
			if err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

