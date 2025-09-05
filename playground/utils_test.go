package playground_test

import (
	"testing"

	"github.com/flashbots/builder-playground/playground"
	"github.com/stretchr/testify/require"
)

func TestGetGatewayFromCIDR(t *testing.T) {
	testCases := []struct {
		name          string
		cidr          string
		expected      string
		expectError   bool
	}{
		{
			name:          "valid cidr /16",
			cidr:          "172.18.0.0/16",
			expected:      "172.18.0.1",
			expectError:   false,
		},
		{
			name:          "valid cidr /24",
			cidr:          "192.168.1.0/24",
			expected:      "192.168.1.1",
			expectError:   false,
		},
		{
			name:          "invalid cidr",
			cidr:          "invalid",
			expected:      "",
			expectError:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gateway, err := playground.GetGatewayFromCIDR(tc.cidr)
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expected, gateway)
			}
		})
	}
}
