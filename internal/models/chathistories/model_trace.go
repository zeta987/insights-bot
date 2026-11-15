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

func splitModelNames(names string) []string {
	parts := strings.Split(names, ",")
	models := make([]string, 0, len(parts))

	for _, part := range parts {
		if model := strings.TrimSpace(part); model != "" {
			models = append(models, model)
		}
	}

	return models
}

// displayModelName shows the configured name when the gateway answered with
// that name minus a suffix, such as a reasoning-effort tag: a request for
// "gpt-6.1-sol-xhigh" answered as "gpt-6.1-sol" is shown as the request.
// Any other answer, such as an alias mapped to a different model, is shown as
// the gateway reported it, and so is a name that more than one configured
// entry could have produced.
func displayModelName(resolved string, configured string) string {
	resolved = strings.TrimSpace(resolved)
	if resolved == "" {
		return ""
	}

	match := ""

	for _, candidate := range splitModelNames(configured) {
		if !strings.HasPrefix(candidate, resolved+"-") {
			continue
		}
		if match != "" && match != candidate {
			return resolved
		}

		match = candidate
	}
	if match == "" {
		return resolved
	}

	return match
}

// appendDisplayedModelNames adds each resolved name, which may be a merged
// comma-separated list, as it should be displayed.
func appendDisplayedModelNames(models []string, resolved string, configured string) []string {
	for _, model := range splitModelNames(resolved) {
		models = appendUniqueModelName(models, displayModelName(model, configured))
	}

	return models
}

func generationModelName(trace *openai.GenerationModelExecutionTrace) string {
	if trace == nil {
		return "資訊不可用"
	}

	usedModels := make([]string, 0, 2)
	usedModels = appendDisplayedModelNames(usedModels, trace.PrimaryUsedModel, trace.PrimaryModel)
	if trace.BackupUsed && trace.BackupSucceeded {
		backupConfigured := pickModelName(trace.BackupRequestedModel, trace.BackupModel)
		if strings.TrimSpace(trace.BackupUsedModel) != "" {
			usedModels = appendDisplayedModelNames(usedModels, trace.BackupUsedModel, backupConfigured)
		} else {
			usedModels = appendUniqueModelName(usedModels, backupConfigured)
		}
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
		"> - 校對模型：" + EscapeRichMarkdownText(checkModel),
	}, "\n")
}
