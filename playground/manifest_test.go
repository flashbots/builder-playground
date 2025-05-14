package playground

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNodeRefString(t *testing.T) {
	var testCases = []struct {
		protocol string
		service  string
		port     int
		user     string
		expected string
	}{
		{
			protocol: "",
			service:  "test",
			port:     80,
			user:     "",
			expected: "test:80",
		},
		{
			protocol: "",
			service:  "test",
			port:     80,
			user:     "test",
			expected: "test@test:test",
		},
		{
			protocol: "http",
			service:  "test",
			port:     80,
			user:     "",
			expected: "http://test:80",
		},
		{
			protocol: "http",
			service:  "test",
			port:     80,
			user:     "test",
			expected: "http://test@test:test",
		},
		{
			protocol: "enode",
			service:  "test",
			port:     80,
			user:     "",
			expected: "enode://test:80",
		},
	}

	for _, testCase := range testCases {
		result := printAddr(testCase.protocol, testCase.service, testCase.port, testCase.user)
		if result != testCase.expected {
			t.Errorf("expected %s, got %s", testCase.expected, result)
		}
	}
}

func TestManifestWriteRead(t *testing.T) {
	out := newTestOutput(t)

	recipe := &L1Recipe{}

	builder := recipe.Artifacts()
	builder.OutputDir(out.dst)

	artifacts, err := builder.Build()
	assert.NoError(t, err)

	manifest, err := recipe.Apply(&ExContext{}, artifacts)
	assert.NoError(t, err)

	assert.NoError(t, manifest.SaveJson())

	manifest2, err := ReadManifest(out.dst)
	assert.NoError(t, err)

	for _, svc := range manifest.Services {
		svc2 := manifest2.MustGetService(svc.Name)
		assert.Equal(t, svc.Name, svc2.Name)
		assert.Equal(t, svc.Args, svc2.Args)
		assert.Equal(t, svc.Env, svc2.Env)
		assert.Equal(t, svc.Labels, svc2.Labels)
		assert.Equal(t, svc.VolumesMapped, svc2.VolumesMapped)
	}
}
