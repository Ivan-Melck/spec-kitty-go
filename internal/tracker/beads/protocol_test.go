package beads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestDecodeEnvelopeContract(t *testing.T) {
	tests := []struct {
		name, input string
		shape       expectedShape
		want        string
		cause       protocolCause
	}{
		{"object", `{"schema_version":1,"data":{"id":"x","unknown":true},"future":"ok"}`, expectedObject, `{"id":"x","unknown":true}`, ""},
		{"array", `{"schema_version":1,"data":[{"id":"x"}]}`, expectedArray, `[{"id":"x"}]`, ""},
		{"wrong requested shape", `{"schema_version":1,"data":{}}`, expectedJSONL, "", protocolShape},
		{"shape mismatch", `{"schema_version":1,"data":[]}`, expectedObject, "", protocolShape},
		{"missing schema", `{"data":{}}`, expectedObject, "", protocolSchema},
		{"zero schema", `{"schema_version":0,"data":{}}`, expectedObject, "", protocolSchema},
		{"future schema", `{"schema_version":2,"data":{}}`, expectedObject, "", protocolSchema},
		{"missing data", `{"schema_version":1}`, expectedObject, "", protocolMalformed},
		{"null data", `{"schema_version":1,"data":null}`, expectedObject, "", protocolMalformed},
		{"malformed", `{"schema_version":1,"data":`, expectedObject, "", protocolMalformed},
		{"trailing prose", `{"schema_version":1,"data":{}} warning`, expectedObject, "", protocolMalformed},
		{"two values", `{"schema_version":1,"data":{}} {}`, expectedObject, "", protocolMalformed},
		{"invalid utf8", string([]byte{0xff}), expectedObject, "", protocolMalformed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, metadata, err := decodeEnvelope([]byte(test.input), test.shape)
			if test.cause == "" {
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != test.want {
					t.Fatalf("got=%s", got)
				}
				if metadata.schemaVersion != 1 {
					t.Fatalf("metadata=%+v", metadata)
				}
				return
			}
			var failure *protocolError
			if !errors.As(err, &failure) || failure.cause != test.cause {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestDecodeDocumentedErrorAmongWarnings(t *testing.T) {
	input := []byte("first warning\n{\"schema_version\":1,\"error\":\"secret body\",\"code\":\"not_found\"}\nsecond warning\n")
	got, err := decodeDocumentedError(input, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.providerCode != "not_found" || !got.warningsPresent || got.warningCount != 2 {
		t.Fatalf("got=%+v", got)
	}
	if strings.Contains(got.providerCode, "secret") {
		t.Fatal("message leaked")
	}
	tests := []struct {
		name      string
		input     []byte
		truncated bool
		cause     protocolCause
	}{
		{"truncated", input, true, protocolOversized},
		{"invalid utf8", []byte{0xff}, false, protocolMalformed},
		{"prose", []byte("not found"), false, protocolMalformed},
		{"partial", []byte(`{"schema_version":1,"error":`), false, protocolMalformed},
		{"missing code", []byte(`{"schema_version":1,"error":"x"}`), false, protocolMalformed},
		{"wrong field type", []byte(`{"schema_version":{},"error":"x","code":"bad"}`), false, protocolMalformed},
		{"future schema", []byte(`{"schema_version":2,"error":"x","code":"bad"}`), false, protocolSchema},
		{"multiple", []byte("{\"schema_version\":1,\"error\":\"x\",\"code\":\"a\"}\n{\"schema_version\":1,\"error\":\"y\",\"code\":\"b\"}"), false, protocolMalformed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeDocumentedError(test.input, test.truncated)
			var failure *protocolError
			if !errors.As(err, &failure) || failure.cause != test.cause {
				t.Fatalf("err=%v", err)
			}
		})
	}
	clean, err := decodeDocumentedError([]byte(`{"schema_version":1,"error":"x","code":"invalid_request"}`), false)
	if err != nil || clean.warningsPresent || clean.warningCount != 0 {
		t.Fatalf("clean=%+v err=%v", clean, err)
	}
}

func TestDecodeJSONLStreamingOrderAndLimits(t *testing.T) {
	input := "{\"id\":1}\n{\"id\":2}\n"
	var got []string
	count, err := decodeExportJSONL(context.Background(), strings.NewReader(input), jsonlLimits{record: 16, aggregate: int64(len(input))}, func(record json.RawMessage) error { got = append(got, string(record)); return nil })
	if err != nil || count != 2 || !reflect.DeepEqual(got, []string{`{"id":1}`, `{"id":2}`}) {
		t.Fatalf("count=%d got=%v err=%v", count, got, err)
	}
	exact := `{"123":456789}`
	count, err = decodeExportJSONL(context.Background(), strings.NewReader(exact+"\n"), jsonlLimits{record: int64(len(exact)), aggregate: int64(len(exact) + 1)}, func(json.RawMessage) error { return nil })
	if err != nil || count != 1 {
		t.Fatalf("boundary count=%d err=%v", count, err)
	}
	_, err = decodeExportJSONL(context.Background(), strings.NewReader(exact+"xxxxx\n"), jsonlLimits{record: int64(len(exact)), aggregate: 100}, func(json.RawMessage) error { return nil })
	assertProtocolCause(t, err, protocolOversized)
	_, err = decodeExportJSONL(context.Background(), strings.NewReader("{}\n{}\n{}\n{}\n{}\n{}\n"), jsonlLimits{record: 10, aggregate: 15}, func(json.RawMessage) error { return nil })
	assertProtocolCause(t, err, protocolOversized)
}

func decodeExportJSONL(ctx context.Context, source io.Reader, limits jsonlLimits, consume func(json.RawMessage) error) (uint64, error) {
	return decodeJSONL(ctx, command{shape: expectedJSONL}, source, limits, consume)
}

func TestDecodeJSONLRequiresExportShape(t *testing.T) {
	_, err := decodeJSONL(context.Background(), command{shape: expectedObject}, strings.NewReader("{}\n"), jsonlLimits{record: 8, aggregate: 8}, func(json.RawMessage) error { return nil })
	assertProtocolCause(t, err, protocolShape)
}

func TestDecodeJSONLRejectsMalformedPartialAndEnvelope(t *testing.T) {
	tests := []struct {
		name, input string
		cause       protocolCause
	}{
		{"blank", "\n", protocolMalformed},
		{"malformed", "{bad}\n", protocolMalformed},
		{"array", "[]\n", protocolShape},
		{"envelope", `{"schema_version":1,"data":{}}` + "\n", protocolShape},
		{"partial final", `{"id":1}`, protocolMalformed},
		{"invalid utf8", string([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}', '\n'}), protocolMalformed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeExportJSONL(context.Background(), strings.NewReader(test.input), jsonlLimits{record: 64, aggregate: 128}, func(json.RawMessage) error { return nil })
			assertProtocolCause(t, err, test.cause)
		})
	}
	_, err := decodeExportJSONL(context.Background(), nil, jsonlLimits{record: 1, aggregate: 1}, func(json.RawMessage) error { return nil })
	assertProtocolCause(t, err, protocolMalformed)
	_, err = decodeExportJSONL(context.Background(), strings.NewReader("{}\n"), jsonlLimits{record: 0, aggregate: 1}, func(json.RawMessage) error { return nil })
	assertProtocolCause(t, err, protocolMalformed)
	_, err = decodeExportJSONL(context.Background(), strings.NewReader("{}\n"), jsonlLimits{record: 1, aggregate: 1}, nil)
	assertProtocolCause(t, err, protocolMalformed)
}

func TestDecodeJSONLCancellationConsumerAndReadFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := decodeExportJSONL(ctx, strings.NewReader("{}\n"), jsonlLimits{record: 8, aggregate: 8}, func(json.RawMessage) error { return nil })
	assertProtocolCause(t, err, protocolCanceled)
	ctx, cancel = context.WithCancel(context.Background())
	deliveries := 0
	_, err = decodeExportJSONL(ctx, strings.NewReader("{}\n{}\n"), jsonlLimits{record: 8, aggregate: 8}, func(json.RawMessage) error { deliveries++; cancel(); return nil })
	assertProtocolCause(t, err, protocolCanceled)
	if deliveries != 1 {
		t.Fatalf("deliveries=%d", deliveries)
	}
	sentinel := errors.New("consumer secret")
	_, err = decodeExportJSONL(context.Background(), strings.NewReader("{}\n"), jsonlLimits{record: 8, aggregate: 8}, func(json.RawMessage) error { return sentinel })
	assertProtocolCause(t, err, protocolConsumer)
	if !errors.Is(err, sentinel) {
		t.Fatal("consumer cause not wrapped")
	}
	_, err = decodeExportJSONL(context.Background(), failingReader{}, jsonlLimits{record: 8, aggregate: 8}, func(json.RawMessage) error { return nil })
	assertProtocolCause(t, err, protocolMalformed)
	ctx, cancel = context.WithCancel(context.Background())
	_, err = decodeExportJSONL(ctx, cancelingReader{cancel: cancel}, jsonlLimits{record: 8, aggregate: 8}, func(json.RawMessage) error { return nil })
	assertProtocolCause(t, err, protocolCanceled)
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func assertProtocolCause(t *testing.T, err error, want protocolCause) {
	t.Helper()
	var failure *protocolError
	if !errors.As(err, &failure) || failure.cause != want {
		t.Fatalf("err=%v want=%s", err, want)
	}
}

type cancelingReader struct{ cancel context.CancelFunc }

func (reader cancelingReader) Read(payload []byte) (int, error) {
	reader.cancel()
	return copy(payload, []byte("{}\n")), nil
}

func TestDecodeOneRejectsSecondDecodeFailure(t *testing.T) {
	var value any
	if err := decodeOne([]byte(`{} []`), &value); err == nil {
		t.Fatal("expected multiple values")
	}
	if err := decodeOne([]byte(`{} {`), &value); err == nil {
		t.Fatal("expected trailing parse failure")
	}
}

func TestJSONLDefaultLimits(t *testing.T) {
	got := defaultJSONLLimits()
	if got.record != defaultJSONLRecordLimit || got.aggregate != defaultJSONLAggregateLimit {
		t.Fatalf("got=%+v", got)
	}
	if isEnvelopeRecord([]byte(`{"id":1}`)) {
		t.Fatal("ordinary record classified envelope")
	}
	if isEnvelopeRecord([]byte(`not-json`)) {
		t.Fatal("malformed classified envelope")
	}
}

func TestProtocolErrorsNeverEchoBodies(t *testing.T) {
	secret := "PROTOCOL_SECRET_SENTINEL"
	_, _, err := decodeEnvelope([]byte(secret), expectedObject)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("err=%v", err)
	}
	_, err = decodeExportJSONL(context.Background(), bytes.NewBufferString(`{"secret":"`+secret+`"}`), jsonlLimits{record: 128, aggregate: 128}, func(json.RawMessage) error { return nil })
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("err=%v", err)
	}
}

func TestProtocolErrorSafeSchemaDiagnostic(t *testing.T) {
	failure := &protocolError{cause: protocolSchema, schema: 2}
	if !strings.Contains(failure.Error(), "schema=2") || failure.Unwrap() != nil {
		t.Fatalf("failure=%v", failure)
	}
}

var _ io.Reader = failingReader{}
