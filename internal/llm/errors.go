package llm

import (
	"errors"
	"regexp"
)

// ErrContextOverflow reports the request did not fit the model context window.
var ErrContextOverflow = &ContextOverflowError{}

// errModelLooping reports the model got stuck repeating itself.
var errModelLooping = errors.New("model started repeating itself; the response was stopped")

// ContextOverflowError is a context window overflow.
//
// Providers word this differently, so the answer text is kept: it reaches
// the user verbatim.
type ContextOverflowError struct {
	// Detail is the provider answer.
	Detail string
}

// Error returns the error text.
func (e *ContextOverflowError) Error() string {
	if e.Detail == "" {
		return "request exceeds the model context window"
	}
	return "request exceeds the model context window: " + e.Detail
}

// Is allows comparison through errors.Is.
func (e *ContextOverflowError) Is(target error) bool {
	_, ok := target.(*ContextOverflowError)
	return ok
}

// overflowPatterns are overflow wordings used by different providers.
//
// The list is deliberately broad: catching the real case matters more than
// strictness, and a false hit only adds advice to compact the history.
var overflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)context[_\s-]?length[_\s-]?exceeded`),
	regexp.MustCompile(`(?i)\bmaximum\s+context\s+(length|window)\b`),
	regexp.MustCompile(`(?i)\bmax(imum)?[\s_-]*(allowed|supported)?[\s_-]*context\b`),
	regexp.MustCompile(`(?i)\bcontext\s+(window|length)\b.*\b(exceed|too\s+(long|large|big))`),
	regexp.MustCompile(`(?i)\b(prompt|input|request|messages?)\s+(is\s+)?too\s+(long|large|big)\b`),
	regexp.MustCompile(`(?i)\btoo\s+many\s+tokens\b`),
	regexp.MustCompile(`(?i)\breduce\s+(the\s+)?(length|size)\s+of\s+(the\s+)?(messages?|prompt|input)\b`),
	regexp.MustCompile(`(?i)\bexceeds?\s+(the\s+)?(model'?s?\s+)?context\b`),
	regexp.MustCompile(`(?i)\btoken\s+limit\s+exceeded\b`),
}

// IsContextOverflow reports whether the answer text looks like an overflow.
func IsContextOverflow(detail string) bool {
	if detail == "" {
		return false
	}
	for _, re := range overflowPatterns {
		if re.MatchString(detail) {
			return true
		}
	}
	return false
}
