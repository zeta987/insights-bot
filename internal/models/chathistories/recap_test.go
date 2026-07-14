package chathistories

import (
	"testing"

	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatFullNameAndUsername(t *testing.T) {
	assert.Equal(t, "小明", formatFullNameAndUsername("小明", "ming"))
	assert.Equal(t, "long_username", formatFullNameAndUsername("very long display name", "long_username"))
	assert.Equal(t, "TopicName", formatFullNameAndUsername("#Topic#Name", ""))
}

func TestMergeRecapExecutionTracePreservesBackupUsageAcrossSlices(t *testing.T) {
	aggregated := &openai.RecapExecutionTrace{}
	mergeRecapExecutionTrace(aggregated, &openai.RecapExecutionTrace{
		Generation: openai.GenerationModelExecutionTrace{
			PrimaryModel:         "detailPrimary",
			PrimaryFailed:        true,
			PrimaryFailureReason: "temporary failure",
			BackupModel:          "detailBackup",
			BackupUsed:           true,
			BackupUsedModel:      "detailBackup",
			BackupSucceeded:      true,
		},
	})
	mergeRecapExecutionTrace(aggregated, &openai.RecapExecutionTrace{
		Generation: openai.GenerationModelExecutionTrace{
			PrimaryModel:     "detailPrimary",
			PrimaryUsedModel: "detailPrimary",
			BackupModel:      "detailBackup",
		},
	})

	require.Equal(t, "detailPrimary", aggregated.Generation.PrimaryModel)
	require.Equal(t, "detailPrimary", aggregated.Generation.PrimaryUsedModel)
	require.True(t, aggregated.Generation.PrimaryFailed)
	require.Equal(t, "temporary failure", aggregated.Generation.PrimaryFailureReason)
	require.Equal(t, "detailBackup", aggregated.Generation.BackupModel)
	require.True(t, aggregated.Generation.BackupUsed)
	require.Equal(t, "detailBackup", aggregated.Generation.BackupUsedModel)
	require.True(t, aggregated.Generation.BackupSucceeded)
}
