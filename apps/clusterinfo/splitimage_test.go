package main

import "testing"

func TestSplitImage(t *testing.T) {
	cases := map[string][2]string{
		"quay.io/cilium/cilium:v1.20.3@sha256:abc": {"cilium", "v1.20.3"},
		"quay.io/strimzi/kafka:1.3.0-kafka-4.3.1":  {"kafka", "1.3.0-kafka-4.3.1"},
		"registry:2": {"registry", "2"},
		"10.0.10.101:5000/edge:v1.0.0-23-gd63b53b": {"edge", "v1.0.0-23-gd63b53b"},
		"registry.k8s.io/pause@sha256:abc":         {"pause", ""},
	}
	for in, want := range cases {
		n, tg := splitImage(in)
		if n != want[0] || tg != want[1] {
			t.Errorf("%s -> %q %q, want %q %q", in, n, tg, want[0], want[1])
		}
	}
}
