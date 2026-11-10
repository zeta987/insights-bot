package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	goopenai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/require"
	"go.uber.org/ratelimit"
)

func TestNumberRecapLines(t *testing.T) {
	require.Equal(t, "1| ## 主題\n2| \n3| - 重點", numberRecapLines([]string{"## 主題", "", "- 重點"}))
}

func TestParseRecapCleanupVerdict(t *testing.T) {
	verdict, err := parseRecapCleanupVerdict("```json\n{\"remove\":[{\"from\":1,\"to\":3}],\"reason\":\"code\"}\n```")
	require.NoError(t, err)
	require.Equal(t, []recapCleanupRange{{From: 1, To: 3}}, verdict.Remove)
	require.Equal(t, "code", verdict.Reason)

	verdict, err = parseRecapCleanupVerdict(`{"remove":[],"reason":""}`)
	require.NoError(t, err)
	require.Empty(t, verdict.Remove)

	_, err = parseRecapCleanupVerdict("looks fine to me")
	require.Error(t, err)

	_, err = parseRecapCleanupVerdict(`{"rewritten":"## 主題"}`)
	require.Error(t, err)
}

func TestApplyRecapCleanup(t *testing.T) {
	lines := []string{"ids = [1,2]", "import re", "## 主題", "", "- 重點 {{tg-ref:1,2}}"}

	cleaned, removed, err := applyRecapCleanup(lines, []recapCleanupRange{{From: 1, To: 2}})
	require.NoError(t, err)
	require.Equal(t, 2, removed)
	require.Equal(t, "## 主題\n\n- 重點 {{tg-ref:1,2}}", cleaned)

	// Overlapping ranges count each line once.
	_, removed, err = applyRecapCleanup(lines, []recapCleanupRange{{From: 1, To: 2}, {From: 2, To: 2}})
	require.NoError(t, err)
	require.Equal(t, 2, removed)

	cleaned, removed, err = applyRecapCleanup(lines, nil)
	require.NoError(t, err)
	require.Zero(t, removed)
	require.Equal(t, strings.Join(lines, "\n"), cleaned)

	for _, invalid := range []recapCleanupRange{{From: 0, To: 1}, {From: 3, To: 2}, {From: 5, To: 6}} {
		_, _, err = applyRecapCleanup(lines, []recapCleanupRange{invalid})
		require.Error(t, err, "range %+v", invalid)
	}
}

type recapCleanupFixture struct {
	recap        string
	backupRecap  string
	checkAnswer  string
	checkStatus  int
	requested    []string
	checkPrompts []string
}

func newRecapCleanupClient(t *testing.T, fixture *recapCleanupFixture) *OpenAIClient {
	t.Helper()

	server := httptest.NewServer(streamChatCompletions(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}

		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		fixture.requested = append(fixture.requested, payload.Model)

		content := fixture.recap

		switch payload.Model {
		case "detailBackup":
			content = fixture.backupRecap
		case "checkModel":
			checkPrompt := payload.Messages[len(payload.Messages)-1].Content
			fixture.checkPrompts = append(fixture.checkPrompts, checkPrompt)

			// The scripted answer targets the primary recap; a backup recap is
			// already clean, so the check model has nothing to delete there.
			if strings.Contains(checkPrompt, "備援") {
				content = `{"remove":[],"reason":""}`

				break
			}

			if fixture.checkStatus != 0 {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(fixture.checkStatus)
				_, err := writer.Write([]byte(`{"error":{"message":"check unavailable","type":"server_error"}}`))
				require.NoError(t, err)

				return
			}

			content = fixture.checkAnswer
		}

		writer.Header().Set("Content-Type", "application/json")
		_, err := fmt.Fprintf(writer,
			`{"id":"chatcmpl-test","object":"chat.completion","created":1730000000,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":5,"total_tokens":9}}`,
			"resolved-"+payload.Model,
			content,
		)
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	config := goopenai.DefaultConfig("test-secret")
	config.BaseURL = server.URL + "/v1"

	return &OpenAIClient{
		modelName:                    "detailPrimary",
		modelNameBackup:              "detailBackup",
		checkModelName:               "checkModel",
		defaultSummarizationLanguage: "Traditional Chinese",
		client:                       goopenai.NewClientWithConfig(config),
		logger:                       client.logger,
		limiter:                      ratelimit.New(1000),
	}
}

const leakedRecapFixture = "chat_ids = [ 1,2 ]\nprint(len(chat_ids))\nTotal messages: 2\ndraft = \"\"\"## 草稿主題\n- 草稿重點 {{tg-ref:1}}\n\"\"\"\n## 主題\n\n- 正式重點 {{tg-ref:1,2}}"

func TestSummarizeChatHistoriesLetsCheckModelRemoveLeakedLines(t *testing.T) {
	fixture := &recapCleanupFixture{
		recap:       leakedRecapFixture,
		backupRecap: "## 備援\n\n- 不應該被用到",
		checkAnswer: `{"remove":[{"from":1,"to":6}],"reason":"scratch code and an earlier draft"}`,
	}
	richClient := newRecapCleanupClient(t, fixture)
	trace := &RecapExecutionTrace{}

	response, err := richClient.SummarizeChatHistories(
		WithRecapExecutionTrace(context.Background(), trace),
		"msgId:1: Zeta sent: hello",
		"Traditional Chinese",
	)

	require.NoError(t, err)
	require.Equal(t, "## 主題\n\n- 正式重點 {{tg-ref:1,2}}", response.Choices[0].Message.Content)
	require.Equal(t, []string{"detailPrimary", "checkModel"}, fixture.requested)
	require.False(t, trace.Generation.BackupUsed)
	require.Equal(t, "resolved-detailPrimary", trace.Generation.PrimaryUsedModel)

	require.Len(t, fixture.checkPrompts, 1)
	require.Contains(t, fixture.checkPrompts[0], "1| chat_ids = [ 1,2 ]")
	require.Contains(t, fixture.checkPrompts[0], "9| - 正式重點 {{tg-ref:1,2}}")
}

func TestSummarizeChatHistoriesKeepsCleanRecapWhenCheckModelFindsNothing(t *testing.T) {
	cleanRecap := "## 主題\n\n- 正式重點 {{tg-ref:1,2}}"
	fixture := &recapCleanupFixture{recap: cleanRecap, checkAnswer: `{"remove":[],"reason":""}`}
	richClient := newRecapCleanupClient(t, fixture)

	response, err := richClient.SummarizeChatHistories(context.Background(), "msgId:1: Zeta sent: hello", "Traditional Chinese")

	require.NoError(t, err)
	require.Equal(t, cleanRecap, response.Choices[0].Message.Content)
	require.Equal(t, []string{"detailPrimary", "checkModel"}, fixture.requested)
}

func TestSummarizeChatHistoriesFallsBackWhenCheckModelCannotClean(t *testing.T) {
	testCases := []struct {
		name        string
		checkAnswer string
		checkStatus int
	}{
		{name: "CheckModelUnavailable", checkStatus: http.StatusBadGateway},
		{name: "AnswerIsNotJSON", checkAnswer: "The recap looks odd."},
		{name: "RangeOutsideRecap", checkAnswer: `{"remove":[{"from":1,"to":99}],"reason":"all"}`},
		{name: "DeletesEverything", checkAnswer: `{"remove":[{"from":1,"to":9}],"reason":"all"}`},
		{name: "LeavesLeakedLines", checkAnswer: `{"remove":[{"from":3,"to":3}],"reason":"output line"}`},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := &recapCleanupFixture{
				recap:       leakedRecapFixture,
				backupRecap: "## 備援\n\n- 備援重點 {{tg-ref:1}}",
				checkAnswer: testCase.checkAnswer,
				checkStatus: testCase.checkStatus,
			}
			richClient := newRecapCleanupClient(t, fixture)

			response, err := richClient.SummarizeChatHistories(context.Background(), "msgId:1: Zeta sent: hello", "Traditional Chinese")

			// The raw answer still fails the rule check, so the backup model is
			// the last resort and its clean answer is delivered.
			require.NoError(t, err)
			require.Equal(t, "## 備援\n\n- 備援重點 {{tg-ref:1}}", response.Choices[0].Message.Content)
			require.Contains(t, fixture.requested, "detailBackup")
		})
	}
}
