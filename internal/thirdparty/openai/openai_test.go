package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nekomeowww/insights-bot/internal/configs"
	"github.com/nekomeowww/insights-bot/internal/datastore"
	"github.com/nekomeowww/insights-bot/internal/lib"
	"github.com/nekomeowww/insights-bot/pkg/tutils"
	goopenai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/require"
	"go.uber.org/ratelimit"
)

var client *OpenAIClient

func TestMain(m *testing.M) {
	logger, err := lib.NewLogger()(lib.NewLoggerParams{
		Configs: configs.NewTestConfig()(),
	})
	if err != nil {
		panic(err)
	}

	ent, err := datastore.NewEnt()(datastore.NewEntParams{
		Lifecycle: tutils.NewEmtpyLifecycle(),
		Configs:   configs.NewTestConfig()(),
	})
	if err != nil {
		panic(err)
	}

	c, err := NewClient(false)(NewClientParams{
		Logger: logger,
		Config: &configs.Config{
			OpenAI: configs.SectionOpenAI{
				Host:   "",
				Secret: "",
			},
		},
		Ent: ent,
	})
	if err != nil {
		panic(err)
	}

	var ok bool

	client, ok = c.(*OpenAIClient)
	if !ok {
		panic("failed to cast to OpenAIClient")
	}

	os.Exit(m.Run())
}

func TestTruncateContentBasedOnTokens(t *testing.T) {
	tables := []struct {
		textContent string
		limits      int
		expected    string
	}{
		{
			textContent: "小溪河水清澈见底",
			limits:      3,
			expected:    "小溪",
		},
		{
			textContent: "小溪河水清澈见底",
			limits:      4,
			expected:    "小溪",
		},
		{
			textContent: "小溪河水清澈见底",
			limits:      5,
			expected:    "小溪河",
		},
	}

	for _, table := range tables {
		t.Run(table.textContent, func(t *testing.T) {
			actual := client.TruncateContentBasedOnTokens(table.textContent, table.limits)
			require.Equal(t, table.expected, actual)
		})
	}
}

func TestSplitContentBasedOnTokenLimitations(t *testing.T) {
	tables := []struct {
		textContent string
		limits      int
		expected    []string
	}{
		{
			textContent: strings.Repeat("a", 40000),
			limits:      3900,
			expected:    []string{strings.Repeat("a", 31200), strings.Repeat("a", 8800)},
		},
		{
			textContent: "小溪河水清澈见底，沿岸芦苇丛生。远处山峰耸立，白云飘渺。一只黄鹂停在枝头，唱起了优美的歌曲，引来了不少路人驻足欣赏。",
			limits:      20,
			expected:    []string{"小溪河水清澈见底，沿岸芦", "苇丛生。远处山峰耸立，白", "云飘渺。一只黄鹂停在枝头，", "唱起了优美的歌曲，引来了不少路人", "驻足欣赏。"},
		},
	}

	for i, table := range tables {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			actual := client.SplitContentBasedByTokenLimitations(table.textContent, table.limits)
			require.Equal(t, table.expected, actual)
		})
	}
}

func TestShouldFallbackByError(t *testing.T) {
	require.True(t, shouldFallbackByError(errors.New("mock error")))
	require.False(t, shouldFallbackByError(context.Canceled))
}

func TestForceRepairSummaryJSON(t *testing.T) {
	raw := "```json\n[\n  {\n    \"topicName\": \"Topic\",\n    \"sinceId\": 1,\n    \"participants\": [\"insights-bot\"],\n    \"discussion\": [\n      {\n        \"point\": \"P\",\n        \"keyIds\": [1,],\n      }\n    ],\n    \"conclusion\": \"C\",\n  },\n]\n```"

	repaired := forceRepairSummaryJSON(raw)
	_, err := validateAndCompactSummaryJSON(repaired)
	require.NoError(t, err)
}

func TestNewClientBackupModelDefaults(t *testing.T) {
	logger, err := lib.NewLogger()(lib.NewLoggerParams{
		Configs: configs.NewTestConfig()(),
	})
	require.NoError(t, err)

	ent, err := datastore.NewEnt()(datastore.NewEntParams{
		Lifecycle: tutils.NewEmtpyLifecycle(),
		Configs:   configs.NewTestConfig()(),
	})
	require.NoError(t, err)

	c, err := NewClient(false)(NewClientParams{
		Logger: logger,
		Config: &configs.Config{
			OpenAI: configs.SectionOpenAI{
				ModelName:                   "primary-model",
				ModelNameBackup:             "",
				SarcasticCondensedModelName: "",
			},
		},
		Ent: ent,
	})
	require.NoError(t, err)

	impl, ok := c.(*OpenAIClient)
	require.True(t, ok)
	require.Equal(t, "primary-model", impl.modelName)
	require.Equal(t, "primary-model", impl.modelNameBackup)
	require.Equal(t, "primary-model", impl.sarcasticCondensedModelName)
	require.Equal(t, "primary-model", impl.sarcasticCondensedModelBackup)
}

func readBuildFixture(t *testing.T, fileName string) string {
	t.Helper()

	content, err := os.ReadFile(filepath.Join("..", "..", "..", "build", fileName))
	require.NoError(t, err)

	return string(content)
}

func TestNormalizeSummaryJSONContentFromBuildValidFixture(t *testing.T) {
	raw := readBuildFixture(t, "g3.1p合法json範本.json")

	normalized, err := client.normalizeSummaryJSONContent(context.Background(), raw, nil)
	require.NoError(t, err)
	require.True(t, json.Valid([]byte(normalized)))

	var outputs []*ChatHistorySummarizationOutputs
	require.NoError(t, json.Unmarshal([]byte(normalized), &outputs))
	require.NotEmpty(t, outputs)
}

func TestNormalizeSummaryJSONContentFromBuildInvalidFixtureByCheckModel(t *testing.T) {
	rawInvalid := readBuildFixture(t, "g3.1p不合法json範本.json")
	rawValid := readBuildFixture(t, "g3.1p合法json範本.json")

	var callCount int32

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "/v1/chat/completions", request.URL.Path)
		atomic.AddInt32(&callCount, 1)

		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), `"model":"check-model"`)

		writer.Header().Set("Content-Type", "application/json")
		_, err = writer.Write([]byte(fmt.Sprintf(
			`{"id":"chatcmpl-test","object":"chat.completion","created":1730000000,"model":"check-model","choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			rawValid,
		)))
		require.NoError(t, err)
	}))
	defer server.Close()

	config := goopenai.DefaultConfig("test-secret")
	config.BaseURL = server.URL + "/v1"

	checkClient := &OpenAIClient{
		checkModelName: "check-model",
		client:         goopenai.NewClientWithConfig(config),
		limiter:        ratelimit.New(1000),
	}

	normalized, err := checkClient.normalizeSummaryJSONContent(context.Background(), rawInvalid, nil)
	require.NoError(t, err)
	require.True(t, json.Valid([]byte(normalized)))
	require.Greater(t, atomic.LoadInt32(&callCount), int32(0))

	var outputs []*ChatHistorySummarizationOutputs
	require.NoError(t, json.Unmarshal([]byte(normalized), &outputs))
	require.NotEmpty(t, outputs)
}
