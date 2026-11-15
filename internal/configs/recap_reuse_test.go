package configs

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseEnabledByDefault(t *testing.T) {
	for _, value := range []string{"", "1", "true", "TRUE", "yes"} {
		require.True(t, parseEnabledByDefault(value), value)
	}

	for _, value := range []string{"0", "false", " False "} {
		require.False(t, parseEnabledByDefault(value), value)
	}
}
