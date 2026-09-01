package processor

import (
	"context"
	"testing"
)

func TestRegistryContainsAllProcessorCategories(t *testing.T) {
	registry := NewRegistry()
	items := registry.List()
	if len(items) != 7 {
		t.Fatalf("expected 7 processors, got %d", len(items))
	}
	for _, item := range items {
		implementation, ok := registry.Get(item.Name)
		if !ok {
			t.Fatalf("missing implementation for %s", item.Name)
		}
		if _, err := implementation.Analyze(context.Background(), []string{"sample" + item.Name}); err != nil {
			t.Fatalf("%s Analyze failed: %v", item.Name, err)
		}
		if err := implementation.Validate(context.Background(), Result{Output: "output"}); err != nil {
			t.Fatalf("%s Validate failed: %v", item.Name, err)
		}
	}
}
