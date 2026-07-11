package playground

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMevBoostRelayEndpointValidate(t *testing.T) {
	cases := []struct {
		name     string
		endpoint MevBoostRelayEndpoint
		ok       bool
		errMatch string
	}{
		{
			name:     "service only",
			endpoint: MevBoostRelayEndpoint{Service: "mev-boost-relay-1"},
			ok:       true,
		},
		{
			name:     "service with secret",
			endpoint: MevBoostRelayEndpoint{Service: "mev-boost-relay-1", SecretKey: "0xdeadbeef"},
			ok:       true,
		},
		{
			name:     "url only",
			endpoint: MevBoostRelayEndpoint{URL: "http://0xpub@host:5555"},
			ok:       true,
		},
		{
			name:     "neither",
			endpoint: MevBoostRelayEndpoint{},
			errMatch: "URL or Service",
		},
		{
			name:     "both url and service",
			endpoint: MevBoostRelayEndpoint{URL: "http://x", Service: "mev-boost-relay-1"},
			errMatch: "pick one",
		},
		{
			name:     "url and secret",
			endpoint: MevBoostRelayEndpoint{URL: "http://x", SecretKey: "0x01"},
			errMatch: "SecretKey must be empty",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.endpoint.validate()
			if tc.ok {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.errMatch)
		})
	}
}
