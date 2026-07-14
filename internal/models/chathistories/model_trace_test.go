package chathistories

import (
	"strings"
	"testing"

	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai"
	"github.com/stretchr/testify/require"
)

func TestBuildRichRecapModelInfoShowsConfiguredCheckModelInStandby(t *testing.T) {
	info := BuildRichRecapModelInfo(
		&openai.CondensedExecutionTrace{
			Generation: openai.GenerationModelExecutionTrace{PrimaryModel: "condensedModel"},
			Check: openai.ConditionalModelExecutionTrace{
				Generation: openai.GenerationModelExecutionTrace{PrimaryModel: "checkModel"},
			},
		},
		&openai.RecapExecutionTrace{Generation: openai.GenerationModelExecutionTrace{PrimaryModel: "detailModel"}},
	)

	require.Equal(t, strings.Join([]string{
		"> **模型資訊**",
		">",
		"> - 濃縮總結：condensedModel",
		"> - 詳細總結：detailModel",
		"> - Check：checkModel",
	}, "\n"), info)
	require.NotContains(t, info, "完成")
	require.NotContains(t, info, "待命")
	require.NotContains(t, info, "🤖")
	require.NotContains(t, info, "✅")
}

func TestBuildRichRecapModelInfoShowsCheckBackupRepair(t *testing.T) {
	info := BuildRichRecapModelInfo(
		&openai.CondensedExecutionTrace{
			Generation: openai.GenerationModelExecutionTrace{PrimaryModel: "condensedModel"},
			Check: openai.ConditionalModelExecutionTrace{
				Attempted: true,
				Succeeded: true,
				Generation: openai.GenerationModelExecutionTrace{
					PrimaryModel:    "checkPrimary",
					PrimaryFailed:   true,
					BackupModel:     "checkBackup",
					BackupUsed:      true,
					BackupUsedModel: "checkBackup",
					BackupSucceeded: true,
				},
			},
		},
		&openai.RecapExecutionTrace{Generation: openai.GenerationModelExecutionTrace{PrimaryModel: "detailModel"}},
	)

	require.Contains(t, info, "> - Check：checkBackup")
	require.NotContains(t, info, "修復")
	require.NotContains(t, info, "備用")
	require.Equal(t, 5, len(strings.Split(info, "\n")))
}

func TestBuildRichRecapModelInfoTreatsBackupOnlyCheckConfigAsUnset(t *testing.T) {
	info := BuildRichRecapModelInfo(
		&openai.CondensedExecutionTrace{
			Generation: openai.GenerationModelExecutionTrace{PrimaryModel: "condensedModel"},
			Check: openai.ConditionalModelExecutionTrace{
				Generation: openai.GenerationModelExecutionTrace{BackupModel: "checkBackup"},
			},
		},
		&openai.RecapExecutionTrace{Generation: openai.GenerationModelExecutionTrace{PrimaryModel: "detailModel"}},
	)

	require.Contains(t, info, "> - Check：未設定")
	require.NotContains(t, info, "> - Check：checkBackup")
}

func TestBuildRichRecapModelInfoShowsEveryModelThatProducedDetailSlices(t *testing.T) {
	info := BuildRichRecapModelInfo(
		&openai.CondensedExecutionTrace{
			Generation: openai.GenerationModelExecutionTrace{PrimaryUsedModel: "condensedModel"},
		},
		&openai.RecapExecutionTrace{Generation: openai.GenerationModelExecutionTrace{
			PrimaryUsedModel: "detailPrimary",
			BackupUsed:       true,
			BackupUsedModel:  "detailBackup",
			BackupSucceeded:  true,
		}},
	)

	require.Contains(t, info, "> - 詳細總結：detailPrimary、detailBackup")
}

func TestBuildRichRecapModelInfoUsesResolvedSuccessfulCheckModel(t *testing.T) {
	info := BuildRichRecapModelInfo(
		&openai.CondensedExecutionTrace{
			Generation: openai.GenerationModelExecutionTrace{PrimaryUsedModel: "condensedModel"},
			Check: openai.ConditionalModelExecutionTrace{
				Attempted: true,
				Succeeded: true,
				Generation: openai.GenerationModelExecutionTrace{
					PrimaryModel:     "checkAlias",
					PrimaryUsedModel: "resolvedCheck",
				},
			},
		},
		&openai.RecapExecutionTrace{Generation: openai.GenerationModelExecutionTrace{PrimaryUsedModel: "detailModel"}},
	)

	require.Contains(t, info, "> - Check：resolvedCheck")
	require.NotContains(t, info, "> - Check：checkAlias")
}

func TestBuildRichRecapModelInfoHidesFailedBackupModel(t *testing.T) {
	info := BuildRichRecapModelInfo(
		&openai.CondensedExecutionTrace{
			Generation: openai.GenerationModelExecutionTrace{
				PrimaryModel:    "condensedPrimary",
				PrimaryFailed:   true,
				BackupModel:     "condensedBackup",
				BackupUsed:      true,
				BackupUsedModel: "resolvedFailedBackup",
				BackupSucceeded: false,
			},
		},
		&openai.RecapExecutionTrace{Generation: openai.GenerationModelExecutionTrace{PrimaryUsedModel: "detailModel"}},
	)

	require.Contains(t, info, "> - 濃縮總結：資訊不可用")
	require.NotContains(t, info, "resolvedFailedBackup")
}

func TestBuildRichRecapModelInfoShowsBackupThatSuppliedCheckRepair(t *testing.T) {
	info := BuildRichRecapModelInfo(
		&openai.CondensedExecutionTrace{
			Generation: openai.GenerationModelExecutionTrace{
				PrimaryModel:    "condensedPrimary",
				PrimaryFailed:   true,
				BackupModel:     "condensedBackup",
				BackupUsed:      true,
				BackupUsedModel: "resolvedBackupSource",
				BackupSucceeded: false,
			},
			Check: openai.ConditionalModelExecutionTrace{
				Attempted: true,
				Succeeded: true,
				Generation: openai.GenerationModelExecutionTrace{
					PrimaryModel:     "checkAlias",
					PrimaryUsedModel: "resolvedCheck",
				},
			},
		},
		&openai.RecapExecutionTrace{Generation: openai.GenerationModelExecutionTrace{PrimaryUsedModel: "detailModel"}},
	)

	require.Contains(t, info, "> - 濃縮總結：resolvedBackupSource")
	require.Contains(t, info, "> - Check：resolvedCheck")
}
