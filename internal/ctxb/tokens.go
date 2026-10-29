// Package ctxb builds the model context: token estimation and history compression.
package ctxb

import "unicode/utf8"

// EstimateTokens roughly estimates the token count of a text.
//
// Latin gives about four characters per token, Cyrillic about two, so the
// two are counted separately. Precision does not matter here: the estimate
// only decides whether to compress.
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}

	ascii, other := 0, 0
	for _, r := range text {
		if r < utf8.RuneSelf {
			ascii++
			continue
		}
		other++
	}

	return ascii/4 + other/2 + 1
}

// EstimateMessages estimates the combined size of the messages.
func EstimateMessages(texts []string) int {
	total := 0
	for _, text := range texts {
		total += EstimateTokens(text)
	}
	return total
}
