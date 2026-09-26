// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package observe

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func hermesTrace(frames int) string {
	lines := []string{"Error: boom"}
	for i := 0; i < frames; i++ {
		lines = append(lines, fmt.Sprintf("    at f%d (address at /data/.expo-internal/cc6bcf26.bundle:1:%d)", i, 1000+i))
	}
	return strings.Join(lines, "\n")
}

func TestTrimStacktraceCutsTheMiddle(t *testing.T) {
	trace, ok := trimStacktrace(hermesTrace(300))
	require.True(t, ok)
	lines := strings.Split(trace, "\n")
	require.Len(t, lines, 1+recentFramesKept+1+oldestFramesKept)
	assert.Equal(t, "Error: boom", lines[0])
	assert.Contains(t, lines[recentFramesKept], "at f63 ")
	assert.Equal(t, "    ... skipping 220 frames", lines[recentFramesKept+1])
	assert.Contains(t, lines[recentFramesKept+2], "at f284 ")
	assert.Contains(t, lines[len(lines)-1], "at f299 ")
}

func TestTrimStacktraceCountsFramesHermesAlreadySkipped(t *testing.T) {
	lines := strings.Split(hermesTrace(100), "\n")
	// Line 71 is frame f70, inside the middle that is cut.
	lines = append(lines[:71:71], append([]string{"    ... skipping 1000 frames"}, lines[71:]...)...)
	trace, ok := trimStacktrace(strings.Join(lines, "\n"))
	require.True(t, ok)
	assert.Contains(t, trace, "    ... skipping 1020 frames")
}

func TestTrimStacktraceKeepsAShortTrace(t *testing.T) {
	short := hermesTrace(20)
	trace, ok := trimStacktrace(short)
	require.True(t, ok)
	assert.Equal(t, short, trace)
}

func TestTrimStacktraceIgnoresText(t *testing.T) {
	_, ok := trimStacktrace("line one\nline two\n    at only one frame (a.js:1:2)")
	assert.False(t, ok)
	_, ok = trimStacktrace(strings.Repeat("x", maxStacktraceScanBytes+10))
	assert.False(t, ok)
}

func TestMarshalAttributesKeepsAStacktracePastTheValueLimit(t *testing.T) {
	trace := hermesTrace(30)
	require.Greater(t, len(trace), maxAttributeValueRunes)
	note := strings.Repeat("n", 2000)

	var kept map[string]string
	require.NoError(t, json.Unmarshal([]byte(marshalAttributes(map[string]any{
		"exception.stacktrace": trace,
		"note":                 note,
	}, nil)), &kept))
	assert.Equal(t, trace, kept["exception.stacktrace"])
	assert.Len(t, kept["note"], maxAttributeValueRunes)
}

func TestBoundBodyGivesAStacktraceItsRoom(t *testing.T) {
	trace := hermesTrace(70)
	require.Greater(t, len(trace), maxBodyRunes)
	assert.Equal(t, trace, boundBody(trace))
	assert.Len(t, boundBody(strings.Repeat("b", 5000)), maxBodyRunes)
}
