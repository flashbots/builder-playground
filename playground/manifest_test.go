package playground

import (
	"testing"
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
