package plugin

import (
	"strings"
	"testing"
)

func TestAPIDocCoversHTTPContext(t *testing.T) {
	doc := RenderAPIDoc()
	for _, want := range []string{
		"HTTP handler context",
		"ctx.bindJSON()",
		"ctx.json(code, value)",
		"ctx.abortWithJSON(code, value)",
		"i18n:register",
		"i18n:catalog",
		"plugin:enabled",
		"Order bands",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("the API reference does not mention %q", want)
		}
	}
}
