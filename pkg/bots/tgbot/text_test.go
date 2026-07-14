package tgbot

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMessageLengthInUTF16Units(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{name: "empty", text: "", want: 0},
		{name: "ascii", text: "recap", want: 5},
		{name: "cjk is one unit per rune", text: "聊天回顧", want: 4},
		{name: "astral emoji counts as two units", text: "🧾", want: 2},
		{name: "mixed content", text: "📬 聊天回顧 💡!", want: 11},
		{name: "variation selector stays in the bmp", text: "🤖️", want: 3},
		{name: "invalid utf-8 byte counts as one replacement unit", text: string([]byte{0xff}), want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, MessageLengthInUTF16Units(tt.text))
		})
	}
}

func TestRuneCountWithinUTF16Units(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		maxUnits int
		want     int
	}{
		{name: "everything fits", text: "recap", maxUnits: 10, want: 5},
		{name: "exact ascii fit", text: "recap", maxUnits: 5, want: 5},
		{name: "ascii truncated", text: "recap", maxUnits: 3, want: 3},
		{name: "astral emoji needs two units", text: "🧾🧾", maxUnits: 4, want: 2},
		{name: "astral emoji cannot span the budget", text: "🧾🧾", maxUnits: 3, want: 1},
		{name: "budget smaller than first astral rune", text: "🧾", maxUnits: 1, want: 0},
		{name: "zero budget", text: "recap", maxUnits: 0, want: 0},
		{name: "mixed bmp and astral", text: "a🧾b", maxUnits: 3, want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, RuneCountWithinUTF16Units([]rune(tt.text), tt.maxUnits))
		})
	}
}

func TestMessageLengthMatchesRuneCountForLongMixedText(t *testing.T) {
	text := strings.Repeat("界🧾a", 1000)

	length := MessageLengthInUTF16Units(text)
	assert.Equal(t, 4000, length)
	assert.Equal(t, len([]rune(text)), RuneCountWithinUTF16Units([]rune(text), length))
}
