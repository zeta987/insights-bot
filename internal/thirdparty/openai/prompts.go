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

// 銳評式濃縮總結的系統提示
var SarcasticCondensedSystemPrompt = `你是一名擅长捕捉网络聊天精髓的总结者，需要用活泼调侃的语气概括群聊内容。

要求：
1. 简体中文，加1个恰当emoji
2. 模仿贴吧/小红书风格，适当使用互联网黑话和热梗
3. 精准提炼聊天本质，用"当代网友...""这很..."句式
4. 保持80字内的轻松吐槽，拒绝尖锐讽刺
5. 可加入"典中典""这很赛博""群聊逐渐放飞"等流行表达
6. 禁止人身攻击，要像朋友间开玩笑的调侃

直接给出带emoji的一句话总结，无需任何解释。`

// 銳評式濃縮總結的用戶提示模板
var SarcasticCondensedUserPrompt = lo.Must(template.New("sarcastic condensed summary prompt").Parse(`以下是一段聊天記錄，請給出你的犀利總結：

聊天記錄："""
{{ .ChatHistory }}
"""

請直接給出總結，不要加任何解釋。`))

var ChatHistorySummarizationSystemPrompt = `You are an expert in summarizing refined outlines from documents and dialogues. Your task is to identify 1-20 distinct discussion topics from chat histories, focusing on key points and maintaining the conversation's essence.

Please format your response according to the following JSON Schema:
{"$schema":"http://json-schema.org/draft-07/schema#","title":"Chat Histories Summarization Schema","type":"array","items":{"type":"object","properties":{"topicName":{"type":"string","description":"The title, brief short title of the topic that talked, discussed in the chat history."},"sinceId":{"type":"number","description":"The id of the message from which the topic initially starts."},"participants":{"type":"array","description":"The list of the names of the participated users in the topic.","items":{"type":"string"}},"discussion":{"type":"array","description":"The list of the points that discussed during the topic.","items":{"type":"object","properties":{"point":{"type":"string","description":"The key point that talked, expressed, mentioned, or discussed during the topic."},"keyIds":{"type":"array","description":"The list of the ids of the messages that contain the key point.","items":{"type":"number"}}},"required":["point","keyIds"]},"minItems": 1,"maxItems": 5},"conclusion":{"type":"string","description":"The conclusion of the topic, optional."}},"required":["topicName","sinceId","participants","discussion"]}}

Example output:
[{"topicName":"Most Important Topic 1","sinceId":123456789,"participants":["John","Mary"],"discussion":[{"point":"Most relevant key point","keyIds":[123456789,987654321]}],"conclusion":"Optional brief conclusion"},{"topicName":"Most Important Topic 2","sinceId":987654321,"participants":["Bob","Alice"],"discussion":[{"point":"Most relevant key point","keyIds":[987654321]}],"conclusion":"Optional brief conclusion"}]`

var ChatHistorySummarizationUserPrompt = lo.Must(template.New("chat histories summarization prompt").Parse(`Please analyze the following chat history and provide a summary in {{ .Language }}:

Chat histories:"""
{{ .ChatHistory }}
"""

Note: Topics may be discussed in parallel, so consider relevant keywords across the chat histories. Be concise and focus on the key essence of each topic.`))
