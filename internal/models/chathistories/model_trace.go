package chathistories

import (
	"strings"

	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai"
)

func pickModelName(candidates ...string) string {
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) != "" {
			return candidate
		}
	}

	return "unknown-model"
}

func appendUniqueModelName(models []string, candidate string) []string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" || candidate == "unknown-model" {
		return models
	}
	for _, model := range models {
		if model == candidate {
			return models
		}
	}
	return append(models, candidate)
}

func generationModelName(trace *openai.GenerationModelExecutionTrace) string {
	if trace == nil {
		return "資訊不可用"
	}

	usedModels := make([]string, 0, 2)
	usedModels = appendUniqueModelName(usedModels, trace.PrimaryUsedModel)
	if trace.BackupUsed && trace.BackupSucceeded {
		usedModels = appendUniqueModelName(usedModels, pickModelName(trace.BackupUsedModel, trace.BackupModel))
	}
	if len(usedModels) > 0 {
		return strings.Join(usedModels, "、")
	}

	if trace.PrimaryFailed {
		return "資訊不可用"
	}

	return pickModelName(trace.PrimaryUsedModel, trace.PrimaryModel)
}

// BuildRichRecapModelInfo renders model provenance as a quiet Rich Markdown
// quote. The conditional Check repair remains visible without implying that it
// ran for every recap.
func BuildRichRecapModelInfo(condensedTrace *openai.CondensedExecutionTrace, recapTrace *openai.RecapExecutionTrace) string {
	condensedModel := "資訊不可用"
	if condensedTrace != nil {
		generation := condensedTrace.Generation
		if condensedTrace.Check.Succeeded && generation.BackupUsed && strings.TrimSpace(generation.BackupUsedModel) != "" {
			generation.BackupSucceeded = true
		}
		condensedModel = generationModelName(&generation)
	}

	detailModel := "資訊不可用"
	if recapTrace != nil {
		detailModel = generationModelName(&recapTrace.Generation)
	}

	checkModel := "資訊不可用"
	if condensedTrace != nil {
		checkModel = strings.TrimSpace(condensedTrace.Check.Generation.PrimaryModel)
		if checkModel == "" {
			checkModel = "未設定"
		} else if condensedTrace.Check.Attempted {
			checkModel = generationModelName(&condensedTrace.Check.Generation)
		}
	}

	return strings.Join([]string{
		"> **模型資訊**",
		">",
		"> - 濃縮總結：" + EscapeRichMarkdownText(condensedModel),
		"> - 詳細總結：" + EscapeRichMarkdownText(detailModel),
		"> - Check：" + EscapeRichMarkdownText(checkModel),
	}, "\n")
}
