package chathistories

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/samber/lo"
	goopenai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nekomeowww/insights-bot/ent"
	"github.com/nekomeowww/insights-bot/ent/logchathistoriesrecap"
	"github.com/nekomeowww/insights-bot/internal/configs"
	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai/openaimock"
	"github.com/nekomeowww/insights-bot/pkg/types/telegram"
	"github.com/nekomeowww/xo"
)

func historiesWithIDs(ids ...int64) []*ent.ChatHistories {
	return lo.Map(ids, func(id int64, _ int) *ent.ChatHistories { return &ent.ChatHistories{MessageID: id} })
}

func recapCandidate(first, last int64) *ent.LogChatHistoriesRecap {
	return &ent.LogChatHistoriesRecap{FirstMessageID: first, LastMessageID: last, RecapOutputs: "recap"}
}

func chosenRanges(chosen []reusableRecap) [][2]int64 {
	return lo.Map(chosen, func(recap reusableRecap, _ int) [2]int64 {
		return [2]int64{recap.log.FirstMessageID, recap.log.LastMessageID}
	})
}

func TestChooseReusableRecapsStartsFromTheLatestAndSkipsNestedRecaps(t *testing.T) {
	histories := historiesWithIDs(lo.RangeFrom[int64](1, 30)...)

	chosen := chooseReusableRecaps(histories, []*ent.LogChatHistoriesRecap{
		recapCandidate(11, 20),
		recapCandidate(12, 18), // a manual recap inside the automatic one
		recapCandidate(21, 30),
	})

	assert.Equal(t, [][2]int64{{11, 20}, {21, 30}}, chosenRanges(chosen))
	assert.Equal(t, 10, chosen[0].start)
	assert.Equal(t, 19, chosen[0].end)
}

func TestChooseReusableRecapsAllowsASmallOverlap(t *testing.T) {
	histories := historiesWithIDs(lo.RangeFrom[int64](1, 30)...)

	chosen := chooseReusableRecaps(histories, []*ent.LogChatHistoriesRecap{
		recapCandidate(21, 30),
		recapCandidate(16, 25), // shares 5 messages with 21-30
	})
	assert.Equal(t, [][2]int64{{16, 25}, {21, 30}}, chosenRanges(chosen))

	chosen = chooseReusableRecaps(histories, []*ent.LogChatHistoriesRecap{
		recapCandidate(21, 30),
		recapCandidate(15, 26), // shares 6 messages
	})
	assert.Equal(t, [][2]int64{{21, 30}}, chosenRanges(chosen))
}

func TestChooseReusableRecapsSkipsRecapsWithNothingNew(t *testing.T) {
	histories := historiesWithIDs(1, 2, 3, 10, 11, 12, 13, 14, 15)

	chosen := chooseReusableRecaps(histories, []*ent.LogChatHistoriesRecap{
		recapCandidate(10, 15),
		recapCandidate(12, 13), // fully covered, even though it is small
		recapCandidate(4, 9),   // no window message in range
		recapCandidate(0, 3),   // written before the range was recorded
		{FirstMessageID: 1, LastMessageID: 3},
	})

	assert.Equal(t, [][2]int64{{10, 15}}, chosenRanges(chosen))
}

func TestChooseReusableRecapsNeedsHistoriesInMessageOrder(t *testing.T) {
	assert.Empty(t, chooseReusableRecaps(historiesWithIDs(3, 1, 2), []*ent.LogChatHistoriesRecap{recapCandidate(1, 3)}))
}

func TestUncoveredHistoriesKeepsGapsAroundChosenRecaps(t *testing.T) {
	histories := historiesWithIDs(lo.RangeFrom[int64](1, 10)...)
	chosen := chooseReusableRecaps(histories, []*ent.LogChatHistoriesRecap{recapCandidate(3, 4), recapCandidate(7, 8)})

	uncovered := lo.Map(uncoveredHistories(histories, chosen), func(history *ent.ChatHistories, _ int) int64 { return history.MessageID })
	assert.Equal(t, []int64{1, 2, 5, 6, 9, 10}, uncovered)
}

func TestStoredRecapToMarkersKeepsOnlyThisChat(t *testing.T) {
	space := newRecapMarkerSpace()
	stored := strings.Join([]string{
		"## 話題",
		"",
		"- 決定 A [1](https://t.me/c/1234567/101) [2](https://t.me/c/1234567/102)",
		"- 別的群 [1](https://t.me/c/999/5)",
		"- 混合 [1](https://t.me/c/999/6) [2](https://t.me/c/1234567/101)",
		"- 管線 a \\| b",
	}, "\n")

	marked := storedRecapToMarkers(stored, -1001234567, space)

	assert.Equal(t, strings.Join([]string{
		"## 話題",
		"",
		"- 決定 A {{tg-ref:1,2}}",
		"- 別的群 ",
		"- 混合 {{tg-ref:1}}",
		"- 管線 a | b",
	}, "\n"), marked)
	assert.Equal(t, map[int64]int64{1: 101, 2: 102}, space.virtualToReal)

	prompt := space.appendHistories([]*ent.ChatHistories{{MessageID: 200, FullName: "Zeta", Text: "hi"}})
	assert.Equal(t, "msgId:3: Zeta sent: hi", prompt)
	assert.Equal(t, int64(200), space.virtualToReal[3])
}

func TestStoredRecapRoundTripsThroughSanitizeAndResolve(t *testing.T) {
	chatID := int64(-1001234567)
	stored := strings.Join([]string{
		"## 話題",
		"",
		"- 決定 A [1](https://t.me/c/1234567/101) [2](https://t.me/c/1234567/102)",
		"- 管線 a \\| b [1](https://t.me/c/1234567/103)",
	}, "\n")

	space := newRecapMarkerSpace()
	marked := storedRecapToMarkers(stored, chatID, space)
	sanitized := SanitizeDetailedRecapMarkdown(marked)
	resolved := ResolveRichRecapReferences(sanitized, chatID, telegram.ChatTypeSuperGroup, space.virtualToReal)

	assert.Equal(t, stored, resolved)
}

func TestStripRecapMarkersLeavesPlainRecap(t *testing.T) {
	assert.Equal(t, "## 話題\n\n- 決定 A\n- 決定 B", stripRecapMarkers("## 話題\n\n- 決定 A {{tg-ref:1,2}}\n- 決定 B {{tg-ref:3}}  "))
}

const reuseTestModelName = "primary-model"

type reuseTestFixture struct {
	t         *testing.T
	model     *Model
	openAI    *openaimock.MockClient
	chatID    int64
	histories []*ent.ChatHistories
}

// newReuseTestFixture returns a model with its own OpenAI mock and a window
// of messages 101-115 sent over the last 15 minutes in a new supergroup.
func newReuseTestFixture(t *testing.T) *reuseTestFixture {
	t.Helper()

	config := configs.NewTestConfig()()
	config.OpenAI.TokenLimit = 100000
	config.OpenAI.ChatHistoriesRecapTokenLimit = 2000
	config.OpenAI.ChatHistoriesRecapReuseEnabled = true

	openAI := &openaimock.MockClient{}
	openAI.GetModelNameReturns(reuseTestModelName)
	openAI.SplitContentBasedByTokenLimitationsStub = func(content string, _ int) []string { return []string{content} }

	chatID := -1000000000000 - xo.RandomInt64()%1000000000
	now := time.Now()
	histories := lo.Map(lo.RangeFrom[int64](101, 15), func(id int64, index int) *ent.ChatHistories {
		return &ent.ChatHistories{
			ChatID:    chatID,
			ChatType:  string(telegram.ChatTypeSuperGroup),
			MessageID: id,
			FullName:  "Zeta",
			Text:      fmt.Sprintf("message %d", id),
			ChattedAt: now.Add(time.Duration(index-15) * time.Minute).UnixMilli(),
		}
	})

	return &reuseTestFixture{
		t:         t,
		model:     &Model{config: config, logger: model.logger, ent: model.ent, openAI: openAI},
		openAI:    openAI,
		chatID:    chatID,
		histories: histories,
	}
}

func (f *reuseTestFixture) link(messageID int64) string {
	return fmt.Sprintf("https://t.me/c/%s/%d", formatChatID(f.chatID), messageID)
}

// saveRecap stores an earlier recap; edit adjusts it before saving.
func (f *reuseTestFixture) saveRecap(first, last int64, outputs string, edit func(*ent.LogChatHistoriesRecapCreate)) *ent.LogChatHistoriesRecap {
	f.t.Helper()

	create := f.model.ent.LogChatHistoriesRecap.
		Create().
		SetChatID(f.chatID).
		SetRecapOutputs(outputs).
		SetRecapType(int(RecapTypeForGroup)).
		SetModelName("old-model").
		SetFirstMessageID(first).
		SetLastMessageID(last)
	if edit != nil {
		edit(create)
	}

	saved, err := create.Save(context.Background())
	require.NoError(f.t, err)

	return saved
}

func (f *reuseTestFixture) savedRecap(id fmt.Stringer) *ent.LogChatHistoriesRecap {
	f.t.Helper()

	rows, err := f.model.ent.LogChatHistoriesRecap.Query().All(context.Background())
	require.NoError(f.t, err)

	row, found := lo.Find(rows, func(row *ent.LogChatHistoriesRecap) bool { return row.ID.String() == id.String() })
	require.True(f.t, found)

	return row
}

func completion(content string) *goopenai.ChatCompletionResponse {
	return &goopenai.ChatCompletionResponse{
		Choices: []goopenai.ChatCompletionChoice{{Message: goopenai.ChatCompletionMessage{Content: content}}},
		Usage:   goopenai.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
	}
}

func (f *reuseTestFixture) summarize() *ManualRecapResult {
	f.t.Helper()

	result, err := f.model.SummarizeChatHistoriesReusingRecaps(f.chatID, telegram.ChatTypeSuperGroup, f.histories)
	require.NoError(f.t, err)
	require.NotNil(f.t, result)

	return result
}

func TestSummarizeChatHistoriesReusingRecapsMergesEarlierRecap(t *testing.T) {
	f := newReuseTestFixture(t)
	f.saveRecap(101, 110, "## 舊話題\n\n- 舊結論 [1]("+f.link(103)+")", nil)

	// The earlier recap's message 103 becomes msgId 1, so the uncovered
	// messages 111-115 are numbered from 2.
	f.openAI.SummarizeChatHistoriesStub = func(_ context.Context, prompt string, _ string) (*goopenai.ChatCompletionResponse, error) {
		assert.Contains(t, prompt, "msgId:2: Zeta sent: message 111")
		assert.Contains(t, prompt, "msgId:6: Zeta sent: message 115")
		assert.NotContains(t, prompt, "message 110")

		return completion("## 新話題\n\n- 新結論 {{tg-ref:3}}"), nil
	}
	f.openAI.MergeChatHistoriesRecapsStub = func(_ context.Context, parts []string, _ string) (*goopenai.ChatCompletionResponse, error) {
		assert.Equal(t, []string{"## 舊話題\n\n- 舊結論 {{tg-ref:1}}", "## 新話題\n\n- 新結論 {{tg-ref:3}}"}, parts)

		return completion("## 合併\n\n- 舊結論 {{tg-ref:1}}\n- 新結論 {{tg-ref:3,99}}"), nil
	}

	result := f.summarize()

	require.Equal(t, 1, f.openAI.SummarizeChatHistoriesCallCount())
	require.Equal(t, 1, f.openAI.MergeChatHistoriesRecapsCallCount())
	assert.Equal(t, []string{"## 合併\n\n- 舊結論 [1](" + f.link(103) + ")\n- 新結論 [1](" + f.link(112) + ")"}, result.Summaries)
	assert.Equal(t, "## 合併\n\n- 舊結論\n- 新結論", result.CondensedSource)

	require.NotNil(t, result.Reuse)
	assert.Equal(t, 1, result.Reuse.ReusedRecaps)
	assert.Equal(t, 5, result.Reuse.SummarizedMessages)
	assert.InDelta(t, (6 * time.Minute).Seconds(), result.Reuse.LastReusedMessageAge.Seconds(), 5)

	saved := f.savedRecap(result.LogID)
	assert.Equal(t, int64(101), saved.FirstMessageID)
	assert.Equal(t, int64(115), saved.LastMessageID)
	assert.Equal(t, 1, saved.ReusedRecapCount)
	assert.Equal(t, result.Summaries[0], saved.RecapOutputs)
	assert.Equal(t, reuseTestModelName, saved.ModelName)
	assert.Equal(t, 10, saved.TotalTokenUsage)
}

func TestSummarizeChatHistoriesReusingRecapsIgnoresIneligibleRows(t *testing.T) {
	f := newReuseTestFixture(t)
	outputs := "## 舊話題\n\n- 舊結論"
	windowStart := f.histories[0].ChattedAt

	f.saveRecap(101, 110, outputs, func(c *ent.LogChatHistoriesRecapCreate) { c.SetChatID(f.chatID - 1) })
	f.saveRecap(101, 110, outputs, func(c *ent.LogChatHistoriesRecapCreate) { c.SetRecapType(int(RecapTypeForPrivateForwarded)) })
	f.saveRecap(101, 110, "", nil)
	f.saveRecap(0, 0, outputs, nil)
	f.saveRecap(101, 110, outputs, func(c *ent.LogChatHistoriesRecapCreate) { c.SetReusedRecapCount(2) })
	f.saveRecap(90, 110, outputs, nil)
	f.saveRecap(101, 110, outputs, func(c *ent.LogChatHistoriesRecapCreate) { c.SetCreatedAt(windowStart - time.Minute.Milliseconds()) })

	f.openAI.SummarizeChatHistoriesStub = func(_ context.Context, prompt string, _ string) (*goopenai.ChatCompletionResponse, error) {
		assert.Contains(t, prompt, "msgId:1: Zeta sent: message 101")
		assert.Contains(t, prompt, "msgId:15: Zeta sent: message 115")

		return completion("## 話題\n\n- 結論 {{tg-ref:1}}"), nil
	}

	result := f.summarize()

	assert.Equal(t, 1, f.openAI.SummarizeChatHistoriesCallCount())
	assert.Zero(t, f.openAI.MergeChatHistoriesRecapsCallCount())
	assert.Nil(t, result.Reuse)
	assert.Equal(t, []string{"## 話題\n\n- 結論 [1](" + f.link(101) + ")"}, result.Summaries)

	saved := f.savedRecap(result.LogID)
	assert.Equal(t, int64(101), saved.FirstMessageID)
	assert.Equal(t, int64(115), saved.LastMessageID)
	assert.Zero(t, saved.ReusedRecapCount)
}

func TestSummarizeChatHistoriesReusingRecapsHonorsKillSwitch(t *testing.T) {
	f := newReuseTestFixture(t)
	f.model.config.OpenAI.ChatHistoriesRecapReuseEnabled = false
	f.saveRecap(101, 110, "## 舊話題\n\n- 舊結論", nil)
	f.openAI.SummarizeChatHistoriesReturns(completion("## 話題\n\n- 結論"), nil)

	result := f.summarize()

	assert.Equal(t, 1, f.openAI.SummarizeChatHistoriesCallCount())
	_, prompt, _ := f.openAI.SummarizeChatHistoriesArgsForCall(0)
	assert.Contains(t, prompt, "message 101")
	assert.Zero(t, f.openAI.MergeChatHistoriesRecapsCallCount())
	assert.Nil(t, result.Reuse)
}

func TestSummarizeChatHistoriesReusingRecapsRechecksACompleteEarlierRecap(t *testing.T) {
	f := newReuseTestFixture(t)
	stored := "## 舊話題\n\n- 有人聲稱烏拉圭總統今天死亡 [1](" + f.link(115) + ")"
	earlier := f.saveRecap(101, 115, stored, nil)
	f.openAI.MergeChatHistoriesRecapsStub = func(_ context.Context, parts []string, _ string) (*goopenai.ChatCompletionResponse, error) {
		assert.Equal(t, []string{"## 舊話題\n\n- 有人聲稱烏拉圭總統今天死亡 {{tg-ref:1}}"}, parts)
		return completion("## 舊話題\n\n- 有人聲稱烏拉圭總統今天死亡，消息未證實 {{tg-ref:1}}"), nil
	}

	result := f.summarize()

	assert.Zero(t, f.openAI.SummarizeChatHistoriesCallCount())
	assert.Equal(t, 1, f.openAI.MergeChatHistoriesRecapsCallCount())
	assert.Equal(t, []string{"## 舊話題\n\n- 有人聲稱烏拉圭總統今天死亡，消息未證實 [1](" + f.link(115) + ")"}, result.Summaries)
	assert.Equal(t, "## 舊話題\n\n- 有人聲稱烏拉圭總統今天死亡，消息未證實", result.CondensedSource)
	require.NotNil(t, result.Reuse)
	assert.Equal(t, 1, result.Reuse.ReusedRecaps)
	assert.Zero(t, result.Reuse.SummarizedMessages)

	assert.NotEqual(t, earlier.ID, result.LogID)
	saved := f.savedRecap(result.LogID)
	assert.Equal(t, 1, saved.ReusedRecapCount)
	assert.Equal(t, result.Summaries[0], saved.RecapOutputs)
	assert.Equal(t, reuseTestModelName, saved.ModelName)
}

func TestSummarizeChatHistoriesReusingRecapsFallsBackWhenMergeFails(t *testing.T) {
	f := newReuseTestFixture(t)
	f.saveRecap(101, 110, "## 舊話題\n\n- 舊結論", nil)
	f.openAI.SummarizeChatHistoriesReturns(completion("## 話題\n\n- 結論"), nil)
	f.openAI.MergeChatHistoriesRecapsReturns(nil, errors.New("gateway timeout"))

	result := f.summarize()

	assert.Equal(t, recapMergeAttempts, f.openAI.MergeChatHistoriesRecapsCallCount())
	require.Equal(t, 2, f.openAI.SummarizeChatHistoriesCallCount())
	_, partialPrompt, _ := f.openAI.SummarizeChatHistoriesArgsForCall(0)
	assert.NotContains(t, partialPrompt, "message 101")
	_, fullPrompt, _ := f.openAI.SummarizeChatHistoriesArgsForCall(1)
	assert.Contains(t, fullPrompt, "msgId:1: Zeta sent: message 101")
	assert.Nil(t, result.Reuse)

	merged, err := f.model.ent.LogChatHistoriesRecap.Query().
		Where(logchathistoriesrecap.ChatID(f.chatID), logchathistoriesrecap.ReusedRecapCountGT(0)).
		Count(context.Background())
	require.NoError(t, err)
	assert.Zero(t, merged)
}

func TestSummarizeChatHistoriesReusingRecapsFallsBackWhenRecapsAreTooLarge(t *testing.T) {
	f := newReuseTestFixture(t)
	f.saveRecap(101, 110, "## 舊話題\n\n- 舊結論", nil)
	f.openAI.SplitContentBasedByTokenLimitationsStub = func(content string, _ int) []string {
		if strings.Contains(content, "舊話題") {
			return []string{content, content}
		}

		return []string{content}
	}
	f.openAI.SummarizeChatHistoriesReturns(completion("## 話題\n\n- 結論"), nil)

	result := f.summarize()

	assert.Equal(t, 1, f.openAI.SummarizeChatHistoriesCallCount())
	assert.Zero(t, f.openAI.MergeChatHistoriesRecapsCallCount())
	assert.Nil(t, result.Reuse)
}

func TestGenRecapCondensedUsesMergedRecapWhenReused(t *testing.T) {
	f := newReuseTestFixture(t)
	f.openAI.SarcasticCondenseReturns("概覽", nil)

	_, _, err := f.model.GenRecapCondensed(f.chatID, f.histories, &ManualRecapResult{
		Reuse:           &RecapReuse{ReusedRecaps: 1},
		CondensedSource: "## 合併\n\n- 結論",
	})
	require.NoError(t, err)
	_, source := f.openAI.SarcasticCondenseArgsForCall(0)
	assert.Equal(t, "## 合併\n\n- 結論", source)

	_, _, err = f.model.GenRecapCondensed(f.chatID, f.histories, &ManualRecapResult{})
	require.NoError(t, err)
	_, source = f.openAI.SarcasticCondenseArgsForCall(1)
	assert.Contains(t, source, "Zeta: message 101")
}
