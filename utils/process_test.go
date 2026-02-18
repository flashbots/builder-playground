package utils

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetCountFromOutput(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		myPid    int
		expected int
	}{
		{
			name:     "empty output",
			output:   "",
			myPid:    1000,
			expected: 0,
		},
		{
			name: "no playground processes",
			output: `  PID COMMAND
 1234 /usr/bin/bash
 5678 vim file.go
`,
			myPid:    1000,
			expected: 0,
		},
		{
			name: "one playground start process",
			output: `  PID COMMAND
 1234 builder-playground start l1
`,
			myPid:    1000,
			expected: 1,
		},
		{
			name: "one playground cook process",
			output: `  PID COMMAND
 1234 builder-playground cook l1
`,
			myPid:    1000,
			expected: 1,
		},
		{
			name: "excludes own process",
			output: `  PID COMMAND
 1000 builder-playground start l1
`,
			myPid:    1000,
			expected: 0,
		},
		{
			name: "multiple playground processes excluding self",
			output: `  PID COMMAND
 1000 builder-playground start l1
 1001 builder-playground start l1
 1002 builder-playground cook opstack
`,
			myPid:    1000,
			expected: 2,
		},
		{
			name: "ignores playground without start or cook",
			output: `  PID COMMAND
 1234 builder-playground list
 5678 builder-playground logs
`,
			myPid:    1000,
			expected: 0,
		},
		{
			name: "mixed processes",
			output: `  PID COMMAND
 100 /bin/bash
 200 builder-playground start l1
 300 vim
 400 builder-playground cook opstack
 500 builder-playground logs
`,
			myPid:    1000,
			expected: 2,
		},
		{
			name: "full path to binary",
			output: `  PID COMMAND
 1234 /usr/local/bin/builder-playground start l1
`,
			myPid:    1000,
			expected: 1,
		},
		{
			name: "with arguments after recipe",
			output: `  PID COMMAND
 1234 builder-playground start l1 --timeout 5m
`,
			myPid:    1000,
			expected: 1,
		},
		{
			name: "invalid pid field",
			output: `  PID COMMAND
 notapid builder-playground start l1
`,
			myPid:    1000,
			expected: 0,
		},
		{
			name: "line with only pid no command",
			output: `  PID COMMAND
 1234
`,
			myPid:    1000,
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getCountFromOutput([]byte(tt.output), tt.myPid)
			require.Equal(t, tt.expected, result)
		})
	}
}
