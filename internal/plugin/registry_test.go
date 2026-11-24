package plugin

import (
	"context"
	"strings"
	"testing"
)

func TestCompleteWithoutDefaultModel(t *testing.T) {
	reg := NewRegistry()
	completed := false
	reg.RegisterProvider(ProviderEntry{
		PluginID: "mock",
		ListModels: func(context.Context) ([]ModelInfo, error) {
			return []ModelInfo{{ID: "a/one"}, {ID: "b/two"}}, nil
		},
		Complete: func(context.Context, CompleteRequest, func(StreamEvent)) (*CompleteResponse, error) {
			completed = true
			return &CompleteResponse{Content: "unexpected"}, nil
		},
	})

	_, err := reg.Complete(context.Background(), CompleteRequest{}, nil)
	if err == nil || !strings.Contains(err.Error(), "no default model is selected") {
		t.Fatalf("Complete error = %v, want a missing default model", err)
	}
	if completed {
		t.Fatal("provider completed without a selected model")
	}
}
