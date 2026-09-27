// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package observe

import (
	"fmt"
	"path"
	"xprem/ee/symbolication"

	"github.com/google/uuid"
)

// The attributes an error record may describe itself with: the OpenTelemetry
// keys of the SDK's js.exception and native.exception, and the ones the
// manual xprem_js_crash event carried before the SDK reported crashes.
const (
	exceptionTypeKey       = "exception.type"
	exceptionMessageKey    = "exception.message"
	exceptionStacktraceKey = "exception.stacktrace"
	manualTypeKey          = "name"
	manualMessageKey       = "message"
	manualStacktraceKey    = "stack"
)

// severityError is OpenTelemetry's lowest ERROR level; FATAL sits above it.
const severityError = 17

// isErrorRecord: an error is whatever was logged at error level or above.
func isErrorRecord(row LogRow) bool {
	return row.IsFatal || row.SeverityNumber >= severityError
}

// exception is what an error record says of itself.
type exception struct {
	errorType  string
	message    string
	stacktrace string
}

// exceptionOf reads the exception under whichever keys the record uses; a
// record with no message at all is named after its event.
func exceptionOf(eventName, body string, attributes map[string]any) exception {
	text := func(keys ...string) string {
		for _, key := range keys {
			if value, _ := attributes[key].(string); value != "" {
				return value
			}
		}
		return ""
	}
	found := exception{
		errorType:  text(exceptionTypeKey, manualTypeKey),
		message:    text(exceptionMessageKey, manualMessageKey),
		stacktrace: text(exceptionStacktraceKey, manualStacktraceKey),
	}
	if found.message == "" {
		found.message = body
	}
	if found.message == "" {
		found.message = eventName
	}
	return found
}

// errorFingerprint names an error by its type and the frames it went through,
// so every occurrence of one bug in one update gets the same fingerprint. A
// trace without frames falls back on the message. uuid.Nil means the record
// is not an error. attributes are the record's as sent, before any cut.
func errorFingerprint(row LogRow, attributes map[string]any) uuid.UUID {
	if !isErrorRecord(row) {
		return uuid.Nil
	}
	found := exceptionOf(row.EventName, row.Body, attributes)
	if frames := frameKeys(found.stacktrace); len(frames) > 0 {
		return symbolication.Fingerprint(append([]string{found.errorType}, frames...)...)
	}
	return symbolication.Fingerprint(found.errorType, symbolication.NormalizeMessage(found.message))
}

// frameKeys lists the frames of a stack trace the same way on every device:
// the file keeps only its name, since the folder holding the bundle differs
// from one device to the next, and a recursion counts once whatever its depth.
func frameKeys(stacktrace string) []string {
	if len(stacktrace) > maxStacktraceScanBytes {
		stacktrace = stacktrace[:maxStacktraceScanBytes]
	}
	var keys []string
	for _, frame := range symbolication.ReadTrace(stacktrace).Frames {
		if frame.Skipped > 0 {
			continue
		}
		file := ""
		if frame.File != "" {
			file = path.Base(frame.File)
		}
		key := fmt.Sprintf("%s %s:%d:%d", frame.Function, file, frame.Line, frame.Column)
		// A "skipping N frames" line inside a recursion must not split it in two.
		if len(keys) > 0 && keys[len(keys)-1] == key {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}
