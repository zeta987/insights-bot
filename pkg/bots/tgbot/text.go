package tgbot

import "unicode/utf8"

// MessageLengthInUTF16Units reports the length of text the way the Telegram
// Bot API enforces message limits: in UTF-16 code units, so characters
// outside the Basic Multilingual Plane (such as most emoji) count as two.
func MessageLengthInUTF16Units(text string) int {
	units := 0
	for _, r := range text {
		units += utf16RuneLength(r)
	}

	return units
}

// RuneCountWithinUTF16Units reports how many leading runes fit within a
// budget of maxUnits UTF-16 code units. It never splits a surrogate pair: a
// rune that would exceed the budget is excluded entirely.
func RuneCountWithinUTF16Units(runes []rune, maxUnits int) int {
	units := 0
	for index, r := range runes {
		units += utf16RuneLength(r)
		if units > maxUnits {
			return index
		}
	}

	return len(runes)
}

func utf16RuneLength(r rune) int {
	if r >= 0x10000 && r <= utf8.MaxRune {
		return 2
	}
	// BMP runes take one unit; invalid runes are sent as the one-unit
	// replacement character.
	return 1
}
