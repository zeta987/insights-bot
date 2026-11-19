package chathistories

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/samber/lo"
	goopenai "github.com/sashabaranov/go-openai"
	"go.uber.org/zap"

	"github.com/nekomeowww/insights-bot/ent"
	"github.com/nekomeowww/insights-bot/ent/logchathistoriesrecap"
	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai"
	"github.com/nekomeowww/insights-bot/pkg/types/telegram"
)

const (
	// maxReusedRecapOverlap is how many messages an earlier recap may share
	// with recaps already chosen. Automatic recaps that run back to back can
	// share a few messages at their edges.
	maxReusedRecapOverlap = 5
	// maxReusableRecapCandidates caps how many stored recaps are considered.
	maxReusableRecapCandidates = 50
	// recapMergeAttempts is how many times the merge call is tried before
	// falling back to summarizing every message.
	recapMergeAttempts = 2
)

// RecapReuse describes how a recap reused earlier recaps of the same chat.
type RecapReuse struct {
	// ReusedRecaps is how many earlier recaps were merged.
	ReusedRecaps int
	// SummarizedMessages is how many messages no earlier recap covered; they
	// were summarized again before merging.
	SummarizedMessages int
	// LastReusedMessageAge is how long before the recap the last message
	// covered by an earlier recap was sent.
	LastReusedMessageAge time.Duration
}

// ManualRecapResult is a detailed recap ready for delivery.
type ManualRecapResult struct {
	LogID     uuid.UUID
	Summaries []string
	Trace     *openai.RecapExecutionTrace
	// Reuse is nil when every message was summarized.
	Reuse *RecapReuse
	// CondensedSource is the merged detailed recap without source markers.
	// It is set with Reuse, since the condensed summary of a merged recap is
	// generated from it instead of from the messages.
	CondensedSource string
}

// reusableRecap is a stored recap chosen for a window, with the positions of
// the first and last window messages it covers.
type reusableRecap struct {
	log   *ent.LogChatHistoriesRecap
	start int
	end   int
}

// SummarizeChatHistoriesReusingRecaps summarizes the messages of a manual
// recap. When earlier recaps of the same chat cover part of the window, only
// the other messages are summarized and the results are merged. When there is
// nothing to reuse, reuse is turned off, or merging fails, every message is
// summarized as SummarizeChatHistories does.
func (m *Model) SummarizeChatHistoriesReusingRecaps(chatID int64, chatType telegram.ChatType, histories []*ent.ChatHistories) (*ManualRecapResult, error) {
	if m.config != nil && m.config.OpenAI.ChatHistoriesRecapReuseEnabled && len(histories) > 0 {
		result, err := m.summarizeReusingRecaps(chatID, chatType, histories)
		if err != nil {
			m.logger.Warn("♻️ failed to reuse earlier recaps, summarizing every message",
				zap.Int64("chat_id", chatID),
				zap.Error(err),
			)
		} else if result != nil {
			return result, nil
		}
	}

	logID, summaries, trace, err := m.SummarizeChatHistories(chatID, chatType, histories)
	if err != nil {
		return nil, err
	}

	return &ManualRecapResult{LogID: logID, Summaries: summaries, Trace: trace}, nil
}

// GenRecapCondensed generates the condensed summary of a recap. A recap that
// reused earlier recaps is condensed from its merged detailed text, so the
// condensed and detailed summaries tell the same story.
func (m *Model) GenRecapCondensed(chatID int64, histories []*ent.ChatHistories, result *ManualRecapResult) (string, *openai.CondensedExecutionTrace, error) {
	if result != nil && result.Reuse != nil && strings.TrimSpace(result.CondensedSource) != "" {
		return m.GenSarcasticCondensedFromText(chatID, result.CondensedSource)
	}

	return m.GenSarcasticCondensed(chatID, histories)
}

// summarizeReusingRecaps returns nil without an error when no earlier recap
// can be reused.
func (m *Model) summarizeReusingRecaps(chatID int64, chatType telegram.ChatType, histories []*ent.ChatHistories) (*ManualRecapResult, error) {
	candidates, err := m.findReusableRecapCandidates(chatID, histories)
	if err != nil {
		return nil, fmt.Errorf("find earlier recaps: %w", err)
	}

	chosen := chooseReusableRecaps(histories, candidates)
	if len(chosen) == 0 {
		m.logger.Info("♻️ no earlier recap to reuse",
			zap.Int64("chat_id", chatID),
			zap.Int("candidates", len(candidates)),
		)

		return nil, nil
	}

	uncovered := uncoveredHistories(histories, chosen)
	reuse := &RecapReuse{
		ReusedRecaps:         len(chosen),
		SummarizedMessages:   len(uncovered),
		LastReusedMessageAge: lastReusedMessageAge(histories, chosen),
	}

	m.logger.Info("♻️ reusing earlier recaps",
		zap.Int64("chat_id", chatID),
		zap.Int("reused_recaps", len(chosen)),
		zap.Int("window_messages", len(histories)),
		zap.Int("uncovered_messages", len(uncovered)),
	)

	return m.mergeReusedRecaps(chatID, chatType, histories, chosen, uncovered, reuse)
}

func (m *Model) findReusableRecapCandidates(chatID int64, histories []*ent.ChatHistories) ([]*ent.LogChatHistoriesRecap, error) {
	firstMessageID, lastMessageID := messageIDRange(histories)
	windowStart := lo.MinBy(histories, func(a, b *ent.ChatHistories) bool { return a.ChattedAt < b.ChattedAt }).ChattedAt

	return m.ent.LogChatHistoriesRecap.
		Query().
		Where(
			logchathistoriesrecap.ChatID(chatID),
			logchathistoriesrecap.RecapType(int(RecapTypeForGroup)),
			logchathistoriesrecap.ReusedRecapCount(0),
			logchathistoriesrecap.FirstMessageIDGT(0),
			logchathistoriesrecap.FirstMessageIDGTE(firstMessageID),
			logchathistoriesrecap.LastMessageIDLTE(lastMessageID),
			logchathistoriesrecap.CreatedAtGTE(windowStart),
			logchathistoriesrecap.RecapOutputsNEQ(""),
		).
		Order(
			logchathistoriesrecap.ByLastMessageID(sql.OrderDesc()),
			logchathistoriesrecap.ByCreatedAt(sql.OrderDesc()),
		).
		Limit(maxReusableRecapCandidates).
		All(context.Background())
}

// chooseReusableRecaps picks earlier recaps for the window, starting from the
// one that ends last. A recap is skipped when it shares more than
// maxReusedRecapOverlap messages with recaps already picked, or when every
// message it covers is already covered. The result is ordered by position in
// the window.
func chooseReusableRecaps(histories []*ent.ChatHistories, candidates []*ent.LogChatHistoriesRecap) []reusableRecap {
	// Positions are found by binary search over message IDs.
	if !slices.IsSortedFunc(histories, func(a, b *ent.ChatHistories) int { return cmp.Compare(a.MessageID, b.MessageID) }) {
		return nil
	}

	sorted := slices.Clone(candidates)
	slices.SortStableFunc(sorted, func(a, b *ent.LogChatHistoriesRecap) int {
		if a.LastMessageID != b.LastMessageID {
			return cmp.Compare(b.LastMessageID, a.LastMessageID)
		}

		return cmp.Compare(b.CreatedAt, a.CreatedAt)
	})

	covered := make([]bool, len(histories))
	chosen := make([]reusableRecap, 0, len(sorted))

	for _, candidate := range sorted {
		if candidate.FirstMessageID <= 0 || candidate.LastMessageID < candidate.FirstMessageID ||
			strings.TrimSpace(candidate.RecapOutputs) == "" {
			continue
		}

		start, _ := slices.BinarySearchFunc(histories, candidate.FirstMessageID, compareHistoryMessageID)

		end, found := slices.BinarySearchFunc(histories, candidate.LastMessageID, compareHistoryMessageID)
		if !found {
			end--
		}
		if start > end {
			continue
		}

		overlap := lo.CountBy(covered[start:end+1], func(isCovered bool) bool { return isCovered })
		if overlap > maxReusedRecapOverlap || overlap == end-start+1 {
			continue
		}

		for index := start; index <= end; index++ {
			covered[index] = true
		}

		chosen = append(chosen, reusableRecap{log: candidate, start: start, end: end})
	}

	slices.SortFunc(chosen, func(a, b reusableRecap) int { return a.start - b.start })

	return chosen
}

func compareHistoryMessageID(history *ent.ChatHistories, messageID int64) int {
	return cmp.Compare(history.MessageID, messageID)
}

// uncoveredHistories returns the window messages that no chosen recap covers.
func uncoveredHistories(histories []*ent.ChatHistories, chosen []reusableRecap) []*ent.ChatHistories {
	covered := make([]bool, len(histories))

	for _, recap := range chosen {
		for index := recap.start; index <= recap.end; index++ {
			covered[index] = true
		}
	}

	return lo.Filter(histories, func(_ *ent.ChatHistories, index int) bool { return !covered[index] })
}

// lastReusedMessageAge is how long ago the last message covered by a chosen
// recap was sent.
func lastReusedMessageAge(histories []*ent.ChatHistories, chosen []reusableRecap) time.Duration {
	lastEnd := lo.MaxBy(chosen, func(a, b reusableRecap) bool { return a.end > b.end }).end
	age := time.Since(time.UnixMilli(histories[lastEnd].ChattedAt))

	return max(age, 0)
}

// The application writes one link per source message, separated by single
// spaces, so consecutive links come from one source marker.
var (
	storedRecapReferencePattern    = regexp.MustCompile(`\[\d+\]\(https://t\.me/c/(\d+)/(\d+)\)`)
	storedRecapReferenceRunPattern = regexp.MustCompile(`\[\d+\]\(https://t\.me/c/\d+/\d+\)(?: \[\d+\]\(https://t\.me/c/\d+/\d+\))*`)
)

// recapMarkerSpace hands out virtual message IDs shared by every part of a
// merge, so markers from different parts never collide.
type recapMarkerSpace struct {
	virtualToReal map[int64]int64
	realToVirtual map[int64]int64
	nextVirtualID int64
}

func newRecapMarkerSpace() *recapMarkerSpace {
	return &recapMarkerSpace{
		virtualToReal: make(map[int64]int64),
		realToVirtual: make(map[int64]int64),
		nextVirtualID: 1,
	}
}

func (s *recapMarkerSpace) virtualID(realID int64) int64 {
	if virtualID, ok := s.realToVirtual[realID]; ok {
		return virtualID
	}

	virtualID := s.nextVirtualID
	s.nextVirtualID++
	s.virtualToReal[virtualID] = realID
	s.realToVirtual[realID] = virtualID

	return virtualID
}

// appendHistories formats histories with fresh virtual IDs.
func (s *recapMarkerSpace) appendHistories(histories []*ent.ChatHistories) string {
	prompt, nextVirtualID := appendRichRecapPrompt(histories, s.virtualToReal, s.nextVirtualID)
	s.nextVirtualID = nextVirtualID

	return prompt
}

// storedRecapToMarkers turns a stored detailed recap back into model input:
// links to messages of chatID become source markers in space, links to other
// chats are dropped, and the escaping added by SanitizeDetailedRecapMarkdown
// is undone so sanitizing the merged recap does not escape it twice.
func storedRecapToMarkers(recap string, chatID int64, space *recapMarkerSpace) string {
	chatPath := formatChatID(chatID)

	converted := storedRecapReferenceRunPattern.ReplaceAllStringFunc(recap, func(run string) string {
		virtualIDs := make([]string, 0, maxReferencesPerMarker)

		for _, match := range storedRecapReferencePattern.FindAllStringSubmatch(run, -1) {
			if match[1] != chatPath || len(virtualIDs) == maxReferencesPerMarker {
				continue
			}

			realID, err := strconv.ParseInt(match[2], 10, 64)
			if err != nil || realID <= 0 {
				continue
			}

			virtualID := strconv.FormatInt(space.virtualID(realID), 10)
			if !lo.Contains(virtualIDs, virtualID) {
				virtualIDs = append(virtualIDs, virtualID)
			}
		}

		if len(virtualIDs) == 0 {
			return ""
		}

		return "{{tg-ref:" + strings.Join(virtualIDs, ",") + "}}"
	})

	return strings.TrimSpace(strings.ReplaceAll(converted, `\|`, "|"))
}

// stripRecapMarkers removes source markers and the spaces left before them.
func stripRecapMarkers(markdown string) string {
	stripped := richRecapReferencePattern.ReplaceAllString(markdown, "")
	lines := strings.Split(stripped, "\n")

	for index, line := range lines {
		lines[index] = strings.TrimRight(line, " \t")
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (m *Model) mergeReusedRecaps(
	chatID int64,
	chatType telegram.ChatType,
	histories []*ent.ChatHistories,
	chosen []reusableRecap,
	uncovered []*ent.ChatHistories,
	reuse *RecapReuse,
) (*ManualRecapResult, error) {
	space := newRecapMarkerSpace()
	parts := make([]string, 0, len(chosen)+1)
	partStarts := make([]int64, 0, len(chosen)+1)

	for _, recap := range chosen {
		parts = append(parts, storedRecapToMarkers(recap.log.RecapOutputs, chatID, space))
		partStarts = append(partStarts, histories[recap.start].MessageID)
	}

	tokenLimit := int(m.config.OpenAI.TokenLimit - m.config.OpenAI.ChatHistoriesRecapTokenLimit)
	if m.exceedsTokenLimit(strings.Join(parts, "\n\n"), tokenLimit) {
		return nil, errors.New("earlier recaps do not fit in one merge request")
	}

	trace := &openai.RecapExecutionTrace{}

	var usage goopenai.Usage

	if len(uncovered) > 0 {
		partial, partialUsage, partialTrace, err := m.summarizeChatHistories(chatID, space.appendHistories(uncovered))
		addUsage(&usage, partialUsage)
		mergeRecapExecutionTrace(trace, partialTrace)

		if err != nil {
			return nil, fmt.Errorf("summarize messages no earlier recap covers: %w", err)
		}

		partialRecap := joinRichMarkdownSummaries(partial)
		if partialRecap == "" {
			return nil, errors.New("summary of messages no earlier recap covers is empty")
		}

		parts = append(parts, partialRecap)
		partStarts = append(partStarts, uncovered[0].MessageID)
	}

	parts = orderMergeParts(parts, partStarts)

	if m.exceedsTokenLimit(strings.Join(parts, "\n\n"), tokenLimit) {
		return nil, errors.New("recaps to merge do not fit in one merge request")
	}

	merged, mergeUsage, err := m.mergeRecapParts(chatID, parts, trace)
	addUsage(&usage, mergeUsage)

	if err != nil {
		return nil, fmt.Errorf("merge recaps: %w", err)
	}

	sanitized := SanitizeDetailedRecapMarkdown(merged)
	resolved := strings.TrimSpace(ResolveRichRecapReferences(sanitized, chatID, chatType, space.virtualToReal))

	if resolved == "" {
		return nil, errors.New("merged recap is empty after sanitizing")
	}

	logID, err := m.saveReusedRecap(chatID, histories, strings.Join(parts, "\n\n"), resolved, usage, m.recapModelName(trace), len(chosen))
	if err != nil {
		return nil, err
	}

	return &ManualRecapResult{
		LogID:           logID,
		Summaries:       []string{resolved},
		Trace:           trace,
		Reuse:           reuse,
		CondensedSource: stripRecapMarkers(sanitized),
	}, nil
}

// orderMergeParts orders parts by the first message each one covers.
func orderMergeParts(parts []string, starts []int64) []string {
	indexes := lo.Range(len(parts))
	slices.SortStableFunc(indexes, func(a, b int) int { return cmp.Compare(starts[a], starts[b]) })

	return lo.Map(indexes, func(index int, _ int) string { return parts[index] })
}

func (m *Model) exceedsTokenLimit(content string, tokenLimit int) bool {
	if tokenLimit <= 0 {
		return true
	}

	return len(m.openAI.SplitContentBasedByTokenLimitations(content, tokenLimit)) > 1
}

func addUsage(total *goopenai.Usage, usage goopenai.Usage) {
	total.PromptTokens += usage.PromptTokens
	total.CompletionTokens += usage.CompletionTokens
	total.TotalTokens += usage.TotalTokens
}

// mergeRecapParts asks the recap model to merge parts and adds what the call
// did to trace.
func (m *Model) mergeRecapParts(chatID int64, parts []string, trace *openai.RecapExecutionTrace) (string, goopenai.Usage, error) {
	language := m.config.OpenAI.ChatHistoriesSummarizationLanguage

	var (
		merged    string
		usage     goopenai.Usage
		lastTrace *openai.RecapExecutionTrace
	)

	_, _, err := lo.AttemptWithDelay(recapMergeAttempts, time.Second, func(tried int, _ time.Duration) error {
		lastTrace = &openai.RecapExecutionTrace{}

		response, err := m.openAI.MergeChatHistoriesRecaps(
			openai.WithRecapExecutionTrace(context.Background(), lastTrace),
			parts,
			language,
		)
		if response != nil {
			addUsage(&usage, response.Usage)
		}

		if err == nil && (response == nil || len(response.Choices) == 0 || strings.TrimSpace(response.Choices[0].Message.Content) == "") {
			err = errors.New("recap model returned an empty merged recap")
		}
		if err != nil {
			m.logger.Warn("♻️ failed to merge recaps",
				zap.Int64("chat_id", chatID),
				zap.Int("tried", tried),
				zap.Error(err),
			)

			return err
		}

		merged = strings.TrimSpace(response.Choices[0].Message.Content)

		return nil
	})

	// The trace shows the attempt that answered, or the last one that failed.
	mergeRecapExecutionTrace(trace, lastTrace)

	return merged, usage, err
}

func (m *Model) saveReusedRecap(
	chatID int64,
	histories []*ent.ChatHistories,
	inputs string,
	outputs string,
	usage goopenai.Usage,
	modelName string,
	reusedRecaps int,
) (uuid.UUID, error) {
	firstMessageID, lastMessageID := messageIDRange(histories)

	saved, err := m.ent.LogChatHistoriesRecap.
		Create().
		SetChatID(chatID).
		SetRecapInputs(inputs).
		SetRecapOutputs(outputs).
		SetCompletionTokenUsage(usage.CompletionTokens).
		SetPromptTokenUsage(usage.PromptTokens).
		SetTotalTokenUsage(usage.TotalTokens).
		SetFromPlatform(int(FromPlatformTelegram)).
		SetRecapType(int(RecapTypeForGroup)).
		SetModelName(modelName).
		SetFirstMessageID(firstMessageID).
		SetLastMessageID(lastMessageID).
		SetReusedRecapCount(reusedRecaps).
		Save(context.Background())
	if err != nil {
		return uuid.Nil, err
	}

	return saved.ID, nil
}
