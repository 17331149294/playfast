package core

import (
	"context"
	"testing"
)

func TestBox(t *testing.T) {
	box := New(context.Background())
	err := box.Start("kr", false)
	if err != nil {
		t.Error(err)
	}
	defer func() { _ = box.Stop() }()
	select {}
}
