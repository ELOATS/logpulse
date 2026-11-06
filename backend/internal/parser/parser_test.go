package parser

import (
	"testing"
	"time"
)

func newTestParser() *Parser {
	p := New("default")
	p.Now = func() time.Time { return time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC) }
	return p
}

func TestParseJSON(t *testing.T) {
	e, ok := newTestParser().ParseLine(`{"time":"2024-05-01T10:00:00Z","level":"error","msg":"db timeout","service":"api","user_id":42}`)
	if !ok {
		t.Fatal("not parsed")
	}
	if e.Level != "ERROR" || e.Service != "api" || e.Message != "db timeout" || e.Format != "json" {
		t.Fatalf("unexpected entry: %+v", e)
	}
	if !e.Timestamp.Equal(time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("bad timestamp %v", e.Timestamp)
	}
	if e.Attributes["user_id"] == nil {
		t.Errorf("attributes missing user_id: %+v", e.Attributes)
	}
}

func TestParseJSONEpochMillis(t *testing.T) {
	e, _ := newTestParser().ParseLine(`{"ts":1714557600000,"message":"hi"}`)
	if !e.Timestamp.Equal(time.Unix(1714557600, 0)) {
		t.Errorf("bad epoch ts %v", e.Timestamp)
	}
}

func TestParseCombined(t *testing.T) {
	e, ok := newTestParser().ParseLine(`127.0.0.1 - frank [10/Oct/2023:13:55:36 -0700] "GET /index.html HTTP/1.1" 503 2326 "-" "curl/8.0"`)
	if !ok || e.Format != "access" || e.Level != "ERROR" {
		t.Fatalf("unexpected entry: %+v", e)
	}
	if e.Attributes["status"] != 503 {
		t.Errorf("status attr: %+v", e.Attributes)
	}
}

func TestParseSyslog(t *testing.T) {
	e, ok := newTestParser().ParseLine(`May  1 10:00:00 web-01 sshd[1234]: Failed password for root from 1.2.3.4`)
	if !ok || e.Format != "syslog" || e.Host != "web-01" || e.Service != "sshd" {
		t.Fatalf("unexpected entry: %+v", e)
	}
	if e.Level != "ERROR" {
		t.Errorf("expected keyword-guessed ERROR, got %s", e.Level)
	}
}

func TestParseGenericText(t *testing.T) {
	e, _ := newTestParser().ParseLine(`2024-05-01 10:00:00,123 WARN [order-service] stock low for sku 991`)
	if e.Level != "WARN" || e.Service != "order-service" || e.Message != "stock low for sku 991" {
		t.Fatalf("unexpected entry: %+v", e)
	}
	if e.Timestamp.Nanosecond() != 123000000 {
		t.Errorf("millis lost: %v", e.Timestamp)
	}
}

func TestParsePlainFallback(t *testing.T) {
	e, ok := newTestParser().ParseLine("something went wrong: exception in thread main")
	if !ok || e.Level != "ERROR" || e.Service != "default" {
		t.Fatalf("unexpected entry: %+v", e)
	}
}

func TestEmptyLine(t *testing.T) {
	if _, ok := newTestParser().ParseLine("   "); ok {
		t.Fatal("empty line should be skipped")
	}
}
