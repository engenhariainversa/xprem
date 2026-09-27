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

func TestReadStacktraceCutsTheMiddle(t *testing.T) {
	trace, ok := readStacktrace(hermesTrace(300), 2)
	require.True(t, ok)
	lines := strings.Split(trace.text, "\n")
	require.Len(t, lines, 1+recentFramesKept+1+oldestFramesKept)
	assert.Equal(t, "Error: boom", lines[0])
	assert.Contains(t, lines[recentFramesKept], "at f49 ")
	assert.Equal(t, "    ... skipping 200 frames", lines[recentFramesKept+1])
	assert.Contains(t, lines[recentFramesKept+2], "at f250 ")
	assert.Contains(t, lines[len(lines)-1], "at f299 ")
	require.Len(t, trace.frames, recentFramesKept+oldestFramesKept)
	assert.Equal(t, "f49", trace.frames[recentFramesKept-1].Function)
	assert.Equal(t, "f250", trace.frames[recentFramesKept].Function)
}

func TestReadStacktraceCountsFramesHermesAlreadySkipped(t *testing.T) {
	lines := strings.Split(hermesTrace(120), "\n")
	// Line 61 is frame f60, inside the middle that is cut.
	lines = append(lines[:61:61], append([]string{"    ... skipping 1000 frames"}, lines[61:]...)...)
	trace, ok := readStacktrace(strings.Join(lines, "\n"), 2)
	require.True(t, ok)
	assert.Contains(t, trace.text, "    ... skipping 1020 frames")
}

func TestReadStacktraceKeepsAShortTrace(t *testing.T) {
	short := hermesTrace(20)
	trace, ok := readStacktrace(short, 2)
	require.True(t, ok)
	assert.Equal(t, short, trace.text)
	assert.Len(t, trace.frames, 20)
}

func TestReadStacktraceIgnoresText(t *testing.T) {
	_, ok := readStacktrace("line one\nline two\n    at only one frame (a.js:1:2)", 2)
	assert.False(t, ok)
	_, ok = readStacktrace("line one\nline two\n    at only one frame (a.js:1:2)", 1)
	assert.True(t, ok)
	_, ok = readStacktrace("onPress@main.jsbundle:1:120", 1)
	assert.True(t, ok)
	_, ok = readStacktrace(strings.Repeat("x", maxStacktraceScanBytes+10), 2)
	assert.False(t, ok)
}

func TestMarshalAttributesKeepsAStacktracePastTheValueLimit(t *testing.T) {
	trace := hermesTrace(30)
	require.Greater(t, len(trace), maxAttributeValueRunes)
	note := strings.Repeat("n", 2000)

	out, traces := marshalAttributes(map[string]any{
		"exception.stacktrace": trace,
		"note":                 note,
	}, nil)
	var kept map[string]string
	require.NoError(t, json.Unmarshal([]byte(out), &kept))
	assert.Equal(t, trace, kept["exception.stacktrace"])
	assert.Len(t, kept["note"], maxAttributeValueRunes)
	assert.Len(t, traces["exception.stacktrace"].frames, 30)
}

func TestBoundBodyGivesAStacktraceItsRoom(t *testing.T) {
	trace := hermesTrace(70)
	require.Greater(t, len(trace), maxBodyRunes)
	assert.Equal(t, trace, boundBody(trace))
	assert.Len(t, boundBody(strings.Repeat("b", 5000)), maxBodyRunes)
}
