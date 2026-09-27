// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package observe

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// A Hermes trace as a device writes it: the bundle sits in a folder named
// after the device.
func deviceTrace(device string, recursion int, throwOffset string) string {
	bundle := "/Users/me/CoreSimulator/Devices/" + device + "/Application Support/.expo-internal/cc6bcf26.bundle"
	lines := []string{"Error: Deliberate deep crash", "    at descend (address at " + bundle + ":1:" + throwOffset + ")"}
	for i := 0; i < recursion; i++ {
		lines = append(lines, "    at descend (address at "+bundle+":1:1128589)")
	}
	if recursion > 50 {
		lines = append(lines[:40], append([]string{"    ... skipping 212 frames"}, lines[40:]...)...)
	}
	lines = append(lines, "    at onPress (address at "+bundle+":1:1472103)", "    at forEach (native)")
	return strings.Join(lines, "\n")
}

// exceptionRow is a js.exception as the SDK logs it, at error level.
var exceptionRow = LogRow{EventName: "js.exception", SeverityNumber: severityError}

func errorAttributes(errorType, message, stacktrace string) map[string]any {
	return map[string]any{
		exceptionTypeKey:       errorType,
		exceptionMessageKey:    message,
		exceptionStacktraceKey: stacktrace,
	}
}

func TestTheSameBugHasOneFingerprintOnEveryDevice(t *testing.T) {
	onIPhone := errorFingerprint(exceptionRow, errorAttributes("Error", "boom", deviceTrace("0CC7E3AE", 3, "1128613")))
	onPixel := errorFingerprint(exceptionRow, errorAttributes("Error", "boom", deviceTrace("9F21B04C", 3, "1128613")))
	assert.NotEqual(t, uuid.Nil, onIPhone)
	assert.Equal(t, onIPhone, onPixel)
}

func TestARecursionHasOneFingerprintWhateverItsDepth(t *testing.T) {
	shallow := errorFingerprint(exceptionRow, errorAttributes("Error", "boom", deviceTrace("0CC7E3AE", 3, "1128613")))
	deep := errorFingerprint(exceptionRow, errorAttributes("Error", "boom", deviceTrace("0CC7E3AE", 300, "1128613")))
	assert.Equal(t, shallow, deep)
}

func TestTwoThrowSitesAreTwoErrors(t *testing.T) {
	here := errorFingerprint(exceptionRow, errorAttributes("Error", "boom", deviceTrace("0CC7E3AE", 3, "1128613")))
	there := errorFingerprint(exceptionRow, errorAttributes("Error", "boom", deviceTrace("0CC7E3AE", 3, "1128700")))
	assert.NotEqual(t, here, there)
}

func TestTheFramesDecideAndNotTheMessage(t *testing.T) {
	trace := deviceTrace("0CC7E3AE", 3, "1128613")
	assert.Equal(t,
		errorFingerprint(exceptionRow, errorAttributes("TypeError", "Cannot read property 'name' of undefined", trace)),
		errorFingerprint(exceptionRow, errorAttributes("TypeError", "Cannot read property 'email' of undefined", trace)))
	assert.NotEqual(t,
		errorFingerprint(exceptionRow, errorAttributes("TypeError", "boom", trace)),
		errorFingerprint(exceptionRow, errorAttributes("RangeError", "boom", trace)))
}

func TestWithoutFramesTheMessageDecidesWithoutItsValues(t *testing.T) {
	assert.Equal(t,
		errorFingerprint(exceptionRow, errorAttributes("Error", "User 42 not found at https://api.example.com/users/42", "")),
		errorFingerprint(exceptionRow, errorAttributes("Error", "User 7 not found at https://api.example.com/users/7", "")))
	assert.NotEqual(t,
		errorFingerprint(exceptionRow, errorAttributes("Error", "User 42 not found", "")),
		errorFingerprint(exceptionRow, errorAttributes("Error", "Cart 42 is empty", "")))
}

// The level decides: what is logged at error or fatal is an error, whatever
// it carries; what is logged below is not, whatever it carries.
func TestTheLevelDecidesWhatIsAnError(t *testing.T) {
	info := LogRow{EventName: "lab_button_pressed", SeverityNumber: 9}
	assert.Equal(t, uuid.Nil, errorFingerprint(info, map[string]any{"message": "hello"}))
	assert.Equal(t, uuid.Nil, errorFingerprint(info, errorAttributes("Error", "boom", "")))

	failed := LogRow{EventName: "payment_failed", SeverityNumber: severityError, Body: "Card declined"}
	assert.NotEqual(t, uuid.Nil, errorFingerprint(failed, map[string]any{"source": "checkout"}))
	assert.Equal(t,
		errorFingerprint(failed, nil),
		errorFingerprint(LogRow{EventName: "payment_failed", SeverityNumber: 20, Body: "Card declined"}, nil),
		"the error levels are one level")

	fatal := LogRow{EventName: "app_died", IsFatal: true}
	assert.NotEqual(t, uuid.Nil, errorFingerprint(fatal, nil), "an event without any message is named after itself")
}

// The manual xprem_js_crash event spells the same things as name, message and stack.
func TestAManualCrashEventHasTheSameFingerprintAsTheSDKs(t *testing.T) {
	trace := deviceTrace("0CC7E3AE", 3, "1128613")
	manualRow := LogRow{EventName: JSCrashEventName, SeverityNumber: 21, IsFatal: true}
	manual := errorFingerprint(manualRow, map[string]any{"name": "Error", "message": "boom", "stack": trace})
	assert.NotEqual(t, uuid.Nil, manual)
	assert.Equal(t, errorFingerprint(exceptionRow, errorAttributes("Error", "boom", trace)), manual)
}
