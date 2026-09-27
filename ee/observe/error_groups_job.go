// Copyright (c) 2026 Axel Marciano (Mercure Technologies). All rights reserved.
// This file is governed by the Mercure Technologies Enterprise Edition License
// (see ee/LICENSE); it is NOT covered by the MIT license of this repository.

package observe

import (
	"context"
	"errors"
	"log"
	"time"
	"xprem/ee/symbolication"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// IndexOpener opens the index of the source map of an update.
type IndexOpener interface {
	OpenUpdateIndex(ctx context.Context, appID, updateUUID string) (*symbolication.Index, error)
}

// The sweep's pace: how often it runs, how many errors one run takes, how far
// back it looks for them, how long it may take, and how many groups it writes
// at a time.
const (
	errorGroupsSweepInterval = time.Minute
	errorGroupsPerSweep      = 200
	errorGroupsLookback      = 24 * time.Hour
	errorGroupsSweepTimeout  = 50 * time.Second
	errorGroupsWriteEvery    = 20
)

// ErrorGroupsSweep gives a group to every error counted without one, from one
// trace symbolicated through the update's index, and marks the errors of an
// update that has no source map.
type ErrorGroupsSweep struct {
	explorer *Explorer
	indexes  IndexOpener
}

func NewErrorGroupsSweep(explorer *Explorer, indexes IndexOpener) *ErrorGroupsSweep {
	return &ErrorGroupsSweep{explorer: explorer, indexes: indexes}
}

// updateIndex is what a pass knows of an update: its index, or the error
// OpenUpdateIndex gave instead.
type updateIndex struct {
	index *symbolication.Index
	err   error
}

// Run is one pass.
func (s *ErrorGroupsSweep) Run(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, errorGroupsSweepTimeout)
	defer cancel()
	pending, err := s.explorer.pendingErrorGroups(ctx, time.Now().Add(-errorGroupsLookback), errorGroupsPerSweep)
	if err != nil {
		return err
	}
	known := map[string]updateIndex{}
	var groups []groupedError
	for _, key := range pending {
		update := s.indexOf(ctx, known, key)
		var group ErrorGroup
		switch {
		case update.err == nil:
			group, err = s.symbolicate(ctx, update.index, key)
			if err != nil {
				log.Printf("observe: error %s of update %s stays without a group: %v", key.fingerprint, key.updateID, err)
				continue
			}
		case errors.Is(update.err, symbolication.ErrNoSourcemap),
			errors.Is(update.err, symbolication.ErrUpdateNotFound):
			group = noGroup(key)
		case errors.Is(update.err, symbolication.ErrIndexNotReady),
			errors.Is(update.err, symbolication.ErrIndexFailed),
			errors.Is(update.err, symbolication.ErrUnavailable):
			// A failed index can be reindexed.
			continue
		default:
			log.Printf("observe: the index of update %s cannot be opened: %v", key.updateID, update.err)
			continue
		}
		groups = append(groups, groupedError{errorKey: key, ErrorGroup: group})
		if len(groups) == errorGroupsWriteEvery {
			if err := s.explorer.writeErrorGroups(ctx, groups); err != nil {
				return err
			}
			groups = nil
		}
	}
	return s.explorer.writeErrorGroups(ctx, groups)
}

// indexOf opens the update's index once per pass.
func (s *ErrorGroupsSweep) indexOf(ctx context.Context, known map[string]updateIndex, key errorKey) updateIndex {
	cacheKey := key.appID + "/" + key.updateID
	if update, seen := known[cacheKey]; seen {
		return update
	}
	index, err := s.indexes.OpenUpdateIndex(ctx, key.appID, key.updateID)
	known[cacheKey] = updateIndex{index: index, err: err}
	return known[cacheKey]
}

// noGroup marks an error whose update has no source map.
func noGroup(key errorKey) ErrorGroup {
	return ErrorGroup{Fingerprint: key.fingerprint, GroupFingerprint: noGroupFingerprint, SymbolicatedAt: time.Now().UTC()}
}

func (s *ErrorGroupsSweep) symbolicate(ctx context.Context, index *symbolication.Index, key errorKey) (ErrorGroup, error) {
	found, err := s.explorer.oneTraceOf(ctx, key)
	if err != nil {
		return ErrorGroup{}, err
	}
	trace := symbolication.Symbolicate(index, found.stacktrace)
	return ErrorGroup{
		Fingerprint:      key.fingerprint,
		GroupFingerprint: symbolication.GroupFingerprint(found.errorType, found.message, trace).String(),
		ErrorType:        found.errorType,
		Message:          found.message,
		Culprit:          symbolication.Culprit(trace),
		Trace:            trace,
		SymbolicatedAt:   time.Now().UTC(),
	}, nil
}

type errorGroupsSweepArgs struct{}

func (errorGroupsSweepArgs) Kind() string { return "error-groups-sweep" }

// A run still going when the next is due is not doubled.
func (errorGroupsSweepArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 1,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable,
				rivertype.JobStatePending,
				rivertype.JobStateScheduled,
				rivertype.JobStateRunning,
			},
		},
	}
}

type errorGroupsWorker struct {
	river.WorkerDefaults[errorGroupsSweepArgs]
	sweep *ErrorGroupsSweep
}

func (w *errorGroupsWorker) Work(ctx context.Context, _ *river.Job[errorGroupsSweepArgs]) error {
	return w.sweep.Run(ctx)
}

func RegisterErrorGroupsWorker(workers *river.Workers, sweep *ErrorGroupsSweep) {
	river.AddWorker(workers, &errorGroupsWorker{sweep: sweep})
}

// PeriodicJob schedules the sweep; River runs it on one replica only.
func (s *ErrorGroupsSweep) PeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(errorGroupsSweepInterval),
		func() (river.JobArgs, *river.InsertOpts) { return errorGroupsSweepArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true},
	)
}
