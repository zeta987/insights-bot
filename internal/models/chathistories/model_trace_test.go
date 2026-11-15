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
		"> - 校對模型：checkModel",
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

	require.Contains(t, info, "> - 校對模型：checkBackup")
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

	require.Contains(t, info, "> - 校對模型：未設定")
	require.NotContains(t, info, "> - 校對模型：checkBackup")
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

	require.Contains(t, info, "> - 校對模型：resolvedCheck")
	require.NotContains(t, info, "> - 校對模型：checkAlias")
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
	require.Contains(t, info, "> - 校對模型：resolvedCheck")
}

func TestBuildRichRecapModelInfoKeepsConfiguredEffortSuffix(t *testing.T) {
	info := BuildRichRecapModelInfo(
		&openai.CondensedExecutionTrace{
			Generation: openai.GenerationModelExecutionTrace{
				PrimaryModel:     "gemini-3.8-flash-high",
				PrimaryUsedModel: "gemini-3.8-flash",
			},
			Check: openai.ConditionalModelExecutionTrace{
				Attempted: true,
				Succeeded: true,
				Generation: openai.GenerationModelExecutionTrace{
					PrimaryModel:     "gpt-6.1-sol-xhigh",
					PrimaryUsedModel: "gpt-6.1-sol",
				},
			},
		},
		&openai.RecapExecutionTrace{Generation: openai.GenerationModelExecutionTrace{
			PrimaryModel:         "gemini-4-carbon-high",
			PrimaryUsedModel:     "gemini-4-carbon",
			BackupModel:          "gemini-3.8-flash-high, gpt-6.1-sol-xhigh",
			BackupUsed:           true,
			BackupRequestedModel: "gpt-6.1-sol-xhigh",
			BackupUsedModel:      "gpt-6.1-sol",
			BackupSucceeded:      true,
		}},
	)

	require.Contains(t, info, "> - 濃縮總結："+EscapeRichMarkdownText("gemini-3.8-flash-high"))
	require.Contains(t, info, "> - 詳細總結："+EscapeRichMarkdownText("gemini-4-carbon-high、gpt-6.1-sol-xhigh"))
	require.Contains(t, info, "> - 校對模型："+EscapeRichMarkdownText("gpt-6.1-sol-xhigh"))
}

func TestGenerationModelNameDisplay(t *testing.T) {
	tables := []struct {
		name     string
		trace    openai.GenerationModelExecutionTrace
		expected string
	}{
		{
			name:     "suffix dropped by gateway",
			trace:    openai.GenerationModelExecutionTrace{PrimaryModel: "gpt-6.1-sol-xhigh", PrimaryUsedModel: "gpt-6.1-sol"},
			expected: "gpt-6.1-sol-xhigh",
		},
		{
			name:     "alias resolved to another model",
			trace:    openai.GenerationModelExecutionTrace{PrimaryModel: "checkAlias", PrimaryUsedModel: "resolvedCheck"},
			expected: "resolvedCheck",
		},
		{
			name:     "resolved name longer than configured",
			trace:    openai.GenerationModelExecutionTrace{PrimaryModel: "gpt-6.1-sol", PrimaryUsedModel: "gpt-6.1-sol-2026-10-01"},
			expected: "gpt-6.1-sol-2026-10-01",
		},
		{
			name: "backup found in configured list",
			trace: openai.GenerationModelExecutionTrace{
				PrimaryFailed:   true,
				BackupModel:     "gemini-4-carbon-high, gpt-6.1-sol-xhigh",
				BackupUsed:      true,
				BackupUsedModel: "gemini-4-carbon",
				BackupSucceeded: true,
			},
			expected: "gemini-4-carbon-high",
		},
		{
			name: "ambiguous backup list keeps resolved name",
			trace: openai.GenerationModelExecutionTrace{
				PrimaryFailed:   true,
				BackupModel:     "gpt-6.1-sol-high, gpt-6.1-sol-xhigh",
				BackupUsed:      true,
				BackupUsedModel: "gpt-6.1-sol",
				BackupSucceeded: true,
			},
			expected: "gpt-6.1-sol",
		},
		{
			name: "requested backup settles ambiguity",
			trace: openai.GenerationModelExecutionTrace{
				PrimaryFailed:        true,
				BackupModel:          "gpt-6.1-sol-high, gpt-6.1-sol-xhigh",
				BackupUsed:           true,
				BackupRequestedModel: "gpt-6.1-sol-xhigh",
				BackupUsedModel:      "gpt-6.1-sol",
				BackupSucceeded:      true,
			},
			expected: "gpt-6.1-sol-xhigh",
		},
		{
			name: "merged detail slices",
			trace: openai.GenerationModelExecutionTrace{
				PrimaryModel:     "gemini-4-carbon-high",
				PrimaryUsedModel: "gemini-4-carbon, gemini-4-carbon-preview",
			},
			expected: "gemini-4-carbon-high、gemini-4-carbon-preview",
		},
	}

	for _, table := range tables {
		t.Run(table.name, func(t *testing.T) {
			trace := table.trace
			require.Equal(t, table.expected, generationModelName(&trace))
		})
	}
}
