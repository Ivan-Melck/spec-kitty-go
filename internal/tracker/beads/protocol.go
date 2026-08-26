package beads

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

const (
	defaultJSONLRecordLimit    = int64(2 << 20)
	defaultJSONLAggregateLimit = int64(256 << 20)
)

type envelope struct {
	SchemaVersion *uint16         `json:"schema_version"`
	Data          json.RawMessage `json:"data"`
}

type protocolMetadata struct {
	schemaVersion   uint16
	warningsPresent bool
	warningCount    int
}

type protocolCause string

const (
	protocolMalformed protocolCause = "malformed"
	protocolSchema    protocolCause = "schema"
	protocolShape     protocolCause = "shape"
	protocolOversized protocolCause = "oversized"
	protocolCanceled  protocolCause = "canceled"
	protocolConsumer  protocolCause = "consumer"
)

type protocolError struct {
	cause      protocolCause
	schema     uint16
	record     uint64
	bytes      int64
	underlying error
}

func (failure *protocolError) Error() string {
	if failure.schema != 0 {
		return fmt.Sprintf("beads protocol %s (schema=%d)", failure.cause, failure.schema)
	}
	if failure.record != 0 {
		return fmt.Sprintf("beads protocol %s (record=%d bytes=%d)", failure.cause, failure.record, failure.bytes)
	}
	return "beads protocol " + string(failure.cause)
}

func (failure *protocolError) Unwrap() error { return failure.underlying }

func decodeEnvelope(payload []byte, shape expectedShape) (json.RawMessage, protocolMetadata, error) {
	if shape != expectedObject && shape != expectedArray {
		return nil, protocolMetadata{}, &protocolError{cause: protocolShape}
	}
	if !utf8.Valid(payload) {
		return nil, protocolMetadata{}, &protocolError{cause: protocolMalformed}
	}
	var value envelope
	if err := decodeOne(payload, &value); err != nil {
		return nil, protocolMetadata{}, &protocolError{cause: protocolMalformed, underlying: err}
	}
	if value.SchemaVersion == nil {
		return nil, protocolMetadata{}, &protocolError{cause: protocolSchema}
	}
	metadata := protocolMetadata{schemaVersion: *value.SchemaVersion}
	if *value.SchemaVersion != SupportedProtocolSchema {
		return nil, metadata, &protocolError{cause: protocolSchema, schema: *value.SchemaVersion}
	}
	trimmed := bytes.TrimSpace(value.Data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, metadata, &protocolError{cause: protocolMalformed}
	}
	want := byte('{')
	if shape == expectedArray {
		want = '['
	}
	if trimmed[0] != want {
		return nil, metadata, &protocolError{cause: protocolShape}
	}
	return append(json.RawMessage(nil), trimmed...), metadata, nil
}

func decodeOne(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

type documentedError struct {
	SchemaVersion *uint16 `json:"schema_version"`
	Message       string  `json:"error"`
	Code          string  `json:"code"`
}

type documentedErrorShape uint8

const (
	documentedErrorObject documentedErrorShape = iota + 1
	documentedDataErrorEnvelope
)

type errorDecode struct {
	providerCode  string
	schemaVersion uint16
	shape         documentedErrorShape
}

func decodeDocumentedError(stdout []byte, truncated bool) (errorDecode, error) {
	if truncated {
		return errorDecode{}, &protocolError{cause: protocolOversized}
	}
	if !utf8.Valid(stdout) {
		return errorDecode{}, &protocolError{cause: protocolMalformed}
	}
	var fields map[string]json.RawMessage
	if err := decodeOne(stdout, &fields); err != nil || fields == nil {
		return errorDecode{}, &protocolError{cause: protocolMalformed, underlying: err}
	}
	schemaPayload, hasSchema := fields["schema_version"]
	if !hasSchema {
		return errorDecode{}, &protocolError{cause: protocolMalformed}
	}
	var schema uint16
	if err := json.Unmarshal(schemaPayload, &schema); err != nil {
		return errorDecode{}, &protocolError{cause: protocolMalformed, underlying: err}
	}
	result := errorDecode{schemaVersion: schema}
	if schema != SupportedProtocolSchema {
		return result, &protocolError{cause: protocolSchema, schema: schema}
	}

	dataPayload, hasData := fields["data"]
	if hasData {
		if _, hasMessage := fields["error"]; hasMessage {
			return errorDecode{}, &protocolError{cause: protocolMalformed}
		}
		if _, hasCode := fields["code"]; hasCode {
			return errorDecode{}, &protocolError{cause: protocolMalformed}
		}
		var dataFields map[string]json.RawMessage
		if err := decodeOne(dataPayload, &dataFields); err != nil || dataFields == nil {
			return errorDecode{}, &protocolError{cause: protocolMalformed, underlying: err}
		}
		messagePayload, hasMessage := dataFields["error"]
		if !hasMessage {
			return errorDecode{}, &protocolError{cause: protocolMalformed}
		}
		var message string
		if err := json.Unmarshal(messagePayload, &message); err != nil || message == "" {
			return errorDecode{}, &protocolError{cause: protocolMalformed, underlying: err}
		}
		result.shape = documentedDataErrorEnvelope
		return result, nil
	}

	if len(fields) != 3 {
		return errorDecode{}, &protocolError{cause: protocolMalformed}
	}
	var candidate documentedError
	if err := decodeOne(stdout, &candidate); err != nil || candidate.SchemaVersion == nil || candidate.Message == "" || candidate.Code == "" {
		return errorDecode{}, &protocolError{cause: protocolMalformed, underlying: err}
	}
	result.providerCode = candidate.Code
	result.shape = documentedErrorObject
	return result, nil
}

func countNonEmptyLines(payload []byte) int {
	count := 0
	for _, line := range bytes.Split(payload, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) > 0 {
			count++
		}
	}
	return count
}

type jsonlLimits struct{ record, aggregate int64 }

func defaultJSONLLimits() jsonlLimits {
	return jsonlLimits{record: defaultJSONLRecordLimit, aggregate: defaultJSONLAggregateLimit}
}

func decodeJSONL(ctx context.Context, request command, source io.Reader, limits jsonlLimits, consume func(json.RawMessage) error) (uint64, error) {
	if request.shape != expectedJSONL {
		return 0, &protocolError{cause: protocolShape}
	}
	return decodeJSONLRecords(ctx, source, limits, consume)
}

func decodeJSONLRecords(ctx context.Context, source io.Reader, limits jsonlLimits, consume func(json.RawMessage) error) (uint64, error) {
	maxInt := int64(^uint(0) >> 1)
	if source == nil || consume == nil || limits.record <= 0 || limits.aggregate <= 0 || limits.record > limits.aggregate || limits.record >= maxInt {
		return 0, &protocolError{cause: protocolMalformed}
	}
	reader := bufio.NewReaderSize(source, int(limits.record)+1)
	var records uint64
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return records, &protocolError{cause: protocolCanceled, record: records + 1, bytes: total, underlying: err}
		}
		line, err := reader.ReadSlice('\n')
		total += int64(len(line))
		if total > limits.aggregate {
			return records, &protocolError{cause: protocolOversized, record: records + 1, bytes: total}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			return records, &protocolError{cause: protocolOversized, record: records + 1, bytes: int64(len(line))}
		}
		if errors.Is(err, io.EOF) {
			if len(line) != 0 {
				return records, &protocolError{cause: protocolMalformed, record: records + 1, bytes: int64(len(line))}
			}
			return records, nil
		}
		if err != nil {
			return records, &protocolError{cause: protocolMalformed, record: records + 1, bytes: int64(len(line)), underlying: err}
		}
		record := bytes.TrimSuffix(line, []byte{'\n'})
		record = bytes.TrimSuffix(record, []byte{'\r'})
		if len(bytes.TrimSpace(record)) == 0 || !utf8.Valid(record) || !json.Valid(record) {
			return records, &protocolError{cause: protocolMalformed, record: records + 1, bytes: int64(len(record))}
		}
		if bytes.TrimSpace(record)[0] != '{' {
			return records, &protocolError{cause: protocolShape, record: records + 1, bytes: int64(len(record))}
		}
		if isEnvelopeRecord(record) {
			return records, &protocolError{cause: protocolShape, record: records + 1, bytes: int64(len(record))}
		}
		if err := ctx.Err(); err != nil {
			return records, &protocolError{cause: protocolCanceled, record: records + 1, bytes: total, underlying: err}
		}
		if err := consume(append(json.RawMessage(nil), record...)); err != nil {
			return records, &protocolError{cause: protocolConsumer, record: records + 1, bytes: int64(len(record)), underlying: err}
		}
		records++
	}
}

func isEnvelopeRecord(record []byte) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(record, &fields) != nil {
		return false
	}
	_, schema := fields["schema_version"]
	_, data := fields["data"]
	return schema && data
}
