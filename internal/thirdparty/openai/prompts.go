package openai

import (
	"text/template"

	"github.com/samber/lo"
)

type AnySummarizationInputs struct {
	Content string
}

var AnySummarizationSystemPrompt = "你是我的总结助手。我将为你提供一段话，我需要你在不丢失原文主旨和情感、不做更多的解释和说明的情况下帮我用不超过100字总结一下这段话说了什么。"

var AnySummarizationUserPrompt = lo.Must(template.New("anything summarization prompt").Parse("內容：\n{{ .Content }}"))

type ChatHistorySummarizationPromptInputs struct {
	ChatHistory string
	Language    string
}

func NewChatHistorySummarizationPromptInputs(chatHistory string, language string) *ChatHistorySummarizationPromptInputs {
	return &ChatHistorySummarizationPromptInputs{
		ChatHistory: chatHistory,
		Language:    lo.Ternary(language != "", language, "Simplified Chinese"),
	}
}

type ChatHistorySummarizationOutputsDiscussion struct {
	Point  string  `json:"point"`
	KeyIDs []int64 `json:"keyIds"`
}

type ChatHistorySummarizationOutputs struct {
	TopicName    string                                       `json:"topicName"`
	SinceID      int64                                        `json:"sinceId"`
	Participants []string                                     `json:"participants"`
	Discussion   []*ChatHistorySummarizationOutputsDiscussion `json:"discussion"`
	Conclusion   string                                       `json:"conclusion"`
}

// 銳評式濃縮總結的輸入模板
type SarcasticCondensedSummaryInputs struct {
	ChatHistory string
}

type CheckSummaryJSONInputs struct {
	RawJSON string
}

type CheckCondensedOutputInputs struct {
	RawOutput string
}

// 銳評式濃縮總結的系統提示
var SarcasticCondensedSystemPrompt = `你是一位精炼的聊天记录总结员。
请将提供的聊天记录，用简体中文总结成一句话的核心内容，并在总结中恰当使用1-2个相关的emoji。
总结应语言精练、直击要点。
请直接给出总结，不要包含任何前言或解释。`

// SetSarcasticCondensedSystemPrompt sets the sarcastic condensed system prompt from config
func SetSarcasticCondensedSystemPrompt(prompt string) {
	if prompt != "" {
		SarcasticCondensedSystemPrompt = prompt
	}
}

// SetSarcasticCondensedUserPrompt sets the sarcastic condensed user prompt from config
func SetSarcasticCondensedUserPrompt(prompt string) {
	if prompt != "" {
		SarcasticCondensedUserPrompt = lo.Must(template.New("sarcastic condensed summary prompt").Parse(prompt))
	}
}

// 銳評式濃縮總結的用戶提示模板
var SarcasticCondensedUserPrompt = lo.Must(template.New("sarcastic condensed summary prompt").Parse(`以下是一段聊天记录，请给出你的总结：

聊天记录："""
{{ .ChatHistory }}
"""

请直接给出总结，不要加任何解释。`))

var ChatHistorySummarizationSystemPrompt = `You are an expert in summarizing refined outlines from documents and dialogues. Your task is to identify 1-20 distinct discussion topics from chat histories, focusing on key points and maintaining the conversation's essence.

Please format your response according to the following JSON Schema:
{"$schema":"http://json-schema.org/draft-07/schema#","title":"Chat Histories Summarization Schema","type":"array","items":{"type":"object","properties":{"topicName":{"type":"string","description":"The title, brief short title of the topic that talked, discussed in the chat history."},"sinceId":{"type":"number","description":"The id of the message from which the topic initially starts."},"participants":{"type":"array","description":"The list of the names of the participated users in the topic.","items":{"type":"string"}},"discussion":{"type":"array","description":"The list of the points that discussed during the topic.","items":{"type":"object","properties":{"point":{"type":"string","description":"The key point that talked, expressed, mentioned, or discussed during the topic."},"keyIds":{"type":"array","description":"The list of the ids of the messages that contain the key point.","items":{"type":"number"}}},"required":["point","keyIds"]},"minItems": 1,"maxItems": 5},"conclusion":{"type":"string","description":"The conclusion of the topic, optional."}},"required":["topicName","sinceId","participants","discussion"]}}

Example output:
[{"topicName":"Most Important Topic 1","sinceId":123456789,"participants":["John","Mary"],"discussion":[{"point":"Most relevant key point","keyIds":[123456789,987654321]}],"conclusion":"Optional brief conclusion"},{"topicName":"Most Important Topic 2","sinceId":987654321,"participants":["Bob","Alice"],"discussion":[{"point":"Most relevant key point","keyIds":[987654321]}],"conclusion":"Optional brief conclusion"}]`

var ChatHistorySummarizationUserPrompt = lo.Must(template.New("chat histories summarization prompt").Parse(`Please summarize the following chat histories into 1-20 topics and return a valid JSON array only.
The output language should be {{ .Language }}.

Chat histories:
{{ .ChatHistory }}
`))

var CheckSummaryJSONSystemPrompt = `You are a strict JSON repair validator.
Your task is to output a valid JSON array only.
The JSON MUST conform to this schema:
[{"topicName":"string","sinceId":123,"participants":["string"],"discussion":[{"point":"string","keyIds":[123]}],"conclusion":"string"}]
Rules:
1) Output valid JSON only.
2) Do not use markdown fences.
3) Do not include any explanation text.
4) Keep original meaning as much as possible.
5) Ensure each item has non-empty topicName, participants, and discussion.
6) Ensure each discussion item has non-empty point and keyIds.
7) If sinceId/keyIds are missing or unknown, use sinceId=1 and keyIds=[1].`

var CheckSummaryJSONUserPrompt = lo.Must(template.New("check summary json prompt").Parse(`Please repair the following JSON payload into a valid JSON array that follows the schema:

{{ .RawJSON }}`))

var CheckCondensedOutputSystemPrompt = `You are a strict output rewriter for condensed summaries.
Your task is to rewrite the provided text into one natural sentence only.
Rules:
1) Output exactly one single-line sentence.
2) Do not use markdown code fences.
3) Do not output JSON, arrays, objects, or key-value format.
4) Do not add explanations or prefixes.
5) Keep the original meaning as much as possible.
6) Preserve emoji when appropriate.`

var CheckCondensedOutputUserPrompt = lo.Must(template.New("check condensed output prompt").Parse(`Please rewrite the following invalid condensed summary into one natural sentence:

{{ .RawOutput }}`))
