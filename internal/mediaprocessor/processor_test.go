package mediaprocessor

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestProcessRejectsOversizedInputBeforeExternalTools(t *testing.T) {
	processor := New(Config{
		ClamAVAddress: "127.0.0.1:1",
		MinDuration:   15 * time.Second,
		MaxDuration:   30 * time.Second,
		FrameCount:    8,
		MaxBytes:      4,
	})
	_, err := processor.Process(context.Background(), bytes.NewReader([]byte("12345")), 5)
	var rejected *RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("error = %v, want RejectedError", err)
	}
}
