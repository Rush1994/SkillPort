package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionJSON(t *testing.T) {
	var out, diagnostics bytes.Buffer
	code := Run(context.Background(), []string{"version", "--format", "json", "--non-interactive", "--quiet"}, &out, &diagnostics, BuildInfo{Version: "test"})
	var result struct {
		SchemaVersion int
		Success       bool
		Data          BuildInfo
		Error         *Error
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if code != 0 || !result.Success || result.SchemaVersion != 1 || result.Error != nil || result.Data.Version != "test" || result.Data.OS == "" || diagnostics.Len() != 0 {
		t.Fatalf("unexpected result: %d %+v %s", code, result, diagnostics.String())
	}
}

func TestHelp(t *testing.T) {
	var out, diagnostics bytes.Buffer
	if code := Run(context.Background(), []string{"--help"}, &out, &diagnostics, BuildInfo{}); code != 0 || !strings.Contains(out.String(), "version") {
		t.Fatalf("help failed: %d %s", code, out.String())
	}
}

func TestInvalidArgumentsAreRedacted(t *testing.T) {
	for _, args := range [][]string{{"--format=json", "secret-value"}, {"version", "--format=json", "--secret-value"}, {"version", "secret-value", "--format=json"}} {
		var out, diagnostics bytes.Buffer
		code := Run(context.Background(), args, &out, &diagnostics, BuildInfo{})
		if code != 2 || strings.Contains(out.String()+diagnostics.String(), "secret-value") {
			t.Fatalf("unsafe failure: %d %s %s", code, out.String(), diagnostics.String())
		}
		var envelope Envelope
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Success || envelope.Error == nil || envelope.Error.Code != 2 {
			t.Fatalf("bad error: %+v", envelope)
		}
	}
}
