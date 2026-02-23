package chathistories

import (
	"fmt"

	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai"
)

func pickModelName(candidates ...string) string {
	for _, candidate := range candidates {
		if candidate != "" {
			return candidate
		}
	}

	return "unknown-model"
}

func formatGenerationStatusLine(trace *openai.GenerationModelExecutionTrace, summaryName string) string {
	if trace == nil {
		return fmt.Sprintf("🤖️ %s模型資訊不可用", summaryName)
	}

	// Prefer configured model names from env for user-facing status text.
	primaryModel := pickModelName(trace.PrimaryModel, trace.PrimaryUsedModel)
	backupModel := pickModelName(trace.BackupUsedModel, trace.BackupModel)

	if trace.BackupUsed && trace.BackupSucceeded {
		return fmt.Sprintf("🤖️ 由 %s 生成 %s失敗（備用 %s 成功）", primaryModel, summaryName, backupModel)
	}

	if trace.BackupUsed && !trace.BackupSucceeded {
		return fmt.Sprintf("🤖️ 由 %s 生成 %s失敗（備用 %s 失敗）", primaryModel, summaryName, backupModel)
	}

	if trace.PrimaryFailed {
		return fmt.Sprintf("🤖️ 由 %s 生成 %s失敗", primaryModel, summaryName)
	}

	return fmt.Sprintf("🤖️ 由 %s 生成 %s", primaryModel, summaryName)
}

func formatCheckStatusLine(checkTrace *openai.CheckModelExecutionTrace, _ *openai.GenerationModelExecutionTrace) string {
	if checkTrace == nil || checkTrace.Model == "" {
		return "🤖️ 未配置校驗模型"
	}

	backupModel := pickModelName(checkTrace.BackupUsedModel, checkTrace.BackupModel)

	if checkTrace.Attempted && checkTrace.Succeeded && checkTrace.BackupUsed {
		return fmt.Sprintf("🤖️ 由 %s 校驗格式失敗（備用 %s 成功）", checkTrace.Model, backupModel)
	}

	if checkTrace.Attempted && checkTrace.Failed {
		if checkTrace.BackupUsed {
			return fmt.Sprintf("🤖️ 由 %s 校驗格式失敗（備用 %s 失敗）", checkTrace.Model, backupModel)
		}
		return fmt.Sprintf("🤖️ 由 %s 校驗格式失敗", checkTrace.Model)
	}

	if checkTrace.Attempted && checkTrace.Succeeded {
		return fmt.Sprintf("🤖️ 由 %s 校驗格式", checkTrace.Model)
	}

	return fmt.Sprintf("🤖️ 由 %s 校驗格式未觸發", checkTrace.Model)
}

func BuildModelExecutionStatusLines(condensedTrace *openai.CondensedExecutionTrace, recapTrace *openai.RecapExecutionTrace) []string {
	lines := make([]string, 0, 3)

	if condensedTrace != nil {
		lines = append(lines, formatGenerationStatusLine(&condensedTrace.Generation, "濃縮總結"))
	} else {
		lines = append(lines, "🤖️ 濃縮總結模型資訊不可用")
	}

	if recapTrace != nil {
		lines = append(lines, formatGenerationStatusLine(&recapTrace.Generation, "分段總結"))
		lines = append(lines, formatCheckStatusLine(&recapTrace.Check, &recapTrace.Generation))
	} else {
		lines = append(lines, "🤖️ 分段總結模型資訊不可用")
		lines = append(lines, "🤖️ 校驗模型資訊不可用")
	}

	return lines
}
