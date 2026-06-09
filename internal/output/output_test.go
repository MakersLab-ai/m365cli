package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, b)
	}
	return m
}

func TestWriteJSONWrapsDataInOKEnvelope(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, map[string]string{"id": "abc"}); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	m := decode(t, buf.Bytes())
	if m["ok"] != true {
		t.Errorf("ok = %v, want true", m["ok"])
	}
	data, _ := m["data"].(map[string]any)
	if data["id"] != "abc" {
		t.Errorf("data.id = %v, want abc", data["id"])
	}
	if _, hasErr := m["error"]; hasErr {
		t.Error("success envelope must not contain error")
	}
}

func TestWriteErrorProducesFailureEnvelope(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteError(&buf, errors.New("mailbox not allowed")); err != nil {
		t.Fatalf("WriteError: %v", err)
	}
	m := decode(t, buf.Bytes())
	if m["ok"] != false {
		t.Errorf("ok = %v, want false", m["ok"])
	}
	errObj, _ := m["error"].(map[string]any)
	if errObj["message"] != "mailbox not allowed" {
		t.Errorf("error.message = %v", errObj["message"])
	}
	if _, hasData := m["data"]; hasData {
		t.Error("failure envelope must not contain data")
	}
}

func TestWriteJSONEndsWithNewline(t *testing.T) {
	var buf bytes.Buffer
	_ = WriteJSON(&buf, struct{}{})
	if b := buf.Bytes(); len(b) == 0 || b[len(b)-1] != '\n' {
		t.Error("WriteJSON output must end with a newline for line-based parsing")
	}
}
