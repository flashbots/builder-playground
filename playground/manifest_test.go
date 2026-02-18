package playground

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNodeRefString(t *testing.T) {
	testCases := []struct {
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
			expected: "test@test:80",
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
			expected: "http://test@test:80",
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

	err := builder.Build(out)
	assert.NoError(t, err)

	components := recipe.Apply(&ExContext{Contender: &ContenderContext{}})
	manifest := NewManifest("", components)
	assert.NoError(t, manifest.SaveJson(out))

	manifest2, err := ReadManifest(out.sessionDir)
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

func TestComponent_RemoveService(t *testing.T) {
	root := &Component{
		Name: "root",
		Services: []*Service{
			{Name: "svc1"},
			{Name: "svc2"},
			{Name: "svc3"},
		},
	}

	root.RemoveService("svc2")

	require.Len(t, root.Services, 2)
	for _, s := range root.Services {
		require.NotEqual(t, "svc2", s.Name)
	}
}

func TestComponent_RemoveService_Nested(t *testing.T) {
	root := &Component{
		Name: "root",
		Inner: []*Component{
			{
				Name: "child",
				Services: []*Service{
					{Name: "nested-svc1"},
					{Name: "nested-svc2"},
				},
			},
		},
	}

	root.RemoveService("nested-svc1")

	require.Len(t, root.Inner[0].Services, 1)
	require.Equal(t, "nested-svc2", root.Inner[0].Services[0].Name)
}

func TestComponent_FindService(t *testing.T) {
	root := &Component{
		Name:     "root",
		Services: []*Service{{Name: "root-svc"}},
		Inner: []*Component{
			{
				Name:     "child",
				Services: []*Service{{Name: "child-svc"}},
			},
		},
	}

	tests := []struct {
		name     string
		search   string
		expected bool
	}{
		{"find root service", "root-svc", true},
		{"find child service", "child-svc", true},
		{"not found", "nonexistent", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := root.FindService(tt.search)
			if tt.expected {
				require.NotNil(t, result)
			} else {
				require.Nil(t, result)
			}
		})
	}
}
