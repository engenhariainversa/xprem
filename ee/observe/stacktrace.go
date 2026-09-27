// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package observe

import (
	"strings"
	"xprem/ee/symbolication"
)

// A stack trace keeps its most recent and its oldest frames; the oldest show
// what started a runaway recursion.
const (
	recentFramesKept = 50
	oldestFramesKept = 50
)

const (
	// maxStacktraceScanBytes bounds how much of a value is read for frames.
	maxStacktraceScanBytes = 256 << 10
	maxStacktraceRunes     = 32 << 10
	// maxStacktraceBytesPerRecord bounds the stack traces of one record together.
	maxStacktraceBytesPerRecord = 64 << 10
)

// stacktrace is a value read as a stack trace: the text kept, and its frames.
type stacktrace struct {
	text   string
	frames []symbolication.Frame
}

// minFramesOf is how many frames a value needs to be read as a stack trace:
// two tell a trace from any other text, and a key known to hold a trace may
// hold a single one.
func minFramesOf(key string) int {
	if key == exceptionStacktraceKey || key == manualStacktraceKey {
		return 1
	}
	return 2
}

// readStacktraces reads the stack traces among the attributes named, within
// maxStacktraceBytesPerRecord together.
func readStacktraces(attrs map[string]any, names []string) map[string]stacktrace {
	traces := map[string]stacktrace{}
	budget := maxStacktraceBytesPerRecord
	for _, key := range names {
		text, isText := attrs[key].(string)
		if !isText {
			continue
		}
		if trace, isTrace := readStacktrace(text, minFramesOf(key)); isTrace && len(trace.text) <= budget {
			budget -= len(trace.text)
			traces[key] = trace
		}
	}
	return traces
}

// readStacktrace reads value as a stack trace, with the frames past the ones
// kept replaced by a single "... skipping N frames" line.
func readStacktrace(value string, minFrames int) (stacktrace, bool) {
	if minFrames > 1 && !strings.Contains(value, "\n") {
		return stacktrace{}, false
	}
	if len(value) > maxStacktraceScanBytes {
		// Cut on a line break so no frame is read half.
		lastBreak := strings.LastIndexByte(value[:maxStacktraceScanBytes], '\n')
		if lastBreak < 0 {
			return stacktrace{}, false
		}
		value = value[:lastBreak]
	}
	lines := symbolication.ReadLines(value)
	frames := countFrames(lines)
	if frames < minFrames {
		return stacktrace{}, false
	}
	if frames > recentFramesKept+oldestFramesKept {
		lines = skipMiddleFrames(lines)
	}
	var read stacktrace
	texts := make([]string, 0, len(lines))
	for _, line := range lines {
		texts = append(texts, line.Text)
		if line.Frame != nil {
			read.frames = append(read.frames, *line.Frame)
		}
	}
	read.text = truncateRunes(strings.Join(texts, "\n"), maxStacktraceRunes)
	return read, true
}

func countFrames(lines []symbolication.Line) int {
	count := 0
	for _, line := range lines {
		if line.Frame != nil {
			count++
		}
	}
	return count
}

// skipMiddleFrames keeps the lines up to the last recent frame kept and from
// the first oldest frame kept, and counts every frame in between, including
// the ones an earlier "skipping" line already stood for.
func skipMiddleFrames(lines []symbolication.Line) []symbolication.Line {
	var frameLines []int
	for i, line := range lines {
		if line.Frame != nil {
			frameLines = append(frameLines, i)
		}
	}
	lastRecent := frameLines[recentFramesKept-1]
	firstOldest := frameLines[len(frameLines)-oldestFramesKept]
	skipped := len(frameLines) - recentFramesKept - oldestFramesKept
	for _, line := range lines[lastRecent+1 : firstOldest] {
		skipped += line.Skipped
	}
	skipping := symbolication.Line{Text: symbolication.SkippedFramesLine(skipped), Skipped: skipped}
	kept := append(lines[:lastRecent+1:lastRecent+1], skipping)
	return append(kept, lines[firstOldest:]...)
}

// boundBody caps a log body, giving a stack trace the room of one.
func boundBody(body string) string {
	if trace, isTrace := readStacktrace(body, 2); isTrace {
		return trace.text
	}
	return truncateRunes(body, maxBodyRunes)
}
