package configs

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseMediaUnderstanding(t *testing.T) {
	env := func(values map[string]string) func(string) string {
		return func(name string) string { return values[name] }
	}

	defaults := parseMediaUnderstanding(env(nil))
	require.False(t, defaults.Enabled)
	require.Empty(t, defaults.ModelName)
	require.Equal(t, int64(10<<20), defaults.MaxFileBytes)
	require.Equal(t, 300, defaults.MaxVoiceSeconds)
	require.Equal(t, 180, defaults.MaxVideoSeconds)

	custom := parseMediaUnderstanding(env(map[string]string{
		EnvMediaUnderstandingEnabled:         "1",
		EnvMediaUnderstandingModelName:       " gemini-4-hydrogen ",
		EnvMediaUnderstandingMaxFileMB:       "50",
		EnvMediaUnderstandingMaxVoiceSeconds: "120",
	}))
	require.True(t, custom.Enabled)
	require.Equal(t, "gemini-4-hydrogen", custom.ModelName)
	require.Equal(t, int64(20<<20), custom.MaxFileBytes, "capped at the Bot API download limit")
	require.Equal(t, 120, custom.MaxVoiceSeconds)

	invalid := parseMediaUnderstanding(env(map[string]string{
		EnvMediaUnderstandingEnabled:         "yes",
		EnvMediaUnderstandingMaxFileMB:       "-3",
		EnvMediaUnderstandingMaxVoiceSeconds: "abc",
	}))
	require.False(t, invalid.Enabled)
	require.Equal(t, int64(10<<20), invalid.MaxFileBytes)
	require.Equal(t, 300, invalid.MaxVoiceSeconds)
}
