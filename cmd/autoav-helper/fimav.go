package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// FIM-AV Assistant writes a per-event manifest into the recording folder
// (src/main/recordings/matchStore.ts). It holds one record per recorded match,
// including the dead-time cut state.
//
// This matters to us because the cut is done IN PLACE: the raw recording is
// moved to Originals/ and ffmpeg writes the trimmed video back to the original
// path under the original name. A file on disk therefore tells us nothing about
// whether it is the raw recording or the finished cut, and once we mark an entry
// uploaded we never touch it again. Without reading this manifest we would race
// the encoder and publish raw videos with all the dead time in them.
const fimavManifest = "fimav-matches.json"

// fimavProcessing mirrors MatchProcessing in src/models/MatchRecord.ts.
type fimavProcessing struct {
	State      string `json:"state"` // unprocessed | queued | processing | done | error
	OutputPath string `json:"outputPath,omitempty"`
	Error      string `json:"error,omitempty"`
}

// fimavRecord is the subset of MatchRecord we care about.
type fimavRecord struct {
	ID         string           `json:"id"`
	FileName   string           `json:"fileName"`
	FilePath   string           `json:"filePath"`
	EndedAt    int64            `json:"endedAt"` // epoch ms
	Status     string           `json:"status"`  // recording | recorded | error
	HasCard    bool             `json:"hasCard"`
	Processing *fimavProcessing `json:"processing,omitempty"`
}

type fimavManifestFile struct {
	Version int           `json:"version"`
	Matches []fimavRecord `json:"matches"`
}

// Cache one parsed manifest per folder, keyed on the file's size+mtime, so the
// 5-second scan loop isn't re-parsing JSON it has already seen.
var (
	manifestMu    sync.Mutex
	manifestCache = map[string]*cachedManifest{}
)

type cachedManifest struct {
	size    int64
	mtime    int64
	byName  map[string]fimavRecord
	present bool // a manifest exists in this folder
}

func loadFimavManifest(dir string) *cachedManifest {
	path := filepath.Join(dir, fimavManifest)
	info, err := os.Stat(path)

	manifestMu.Lock()
	defer manifestMu.Unlock()

	if err != nil {
		// No manifest: this folder isn't driven by FIM-AV Assistant (or it
		// hasn't recorded anything yet). Nothing to gate on.
		delete(manifestCache, dir)
		return &cachedManifest{byName: map[string]fimavRecord{}}
	}
	if c, ok := manifestCache[dir]; ok && c.size == info.Size() && c.mtime == info.ModTime().UnixNano() {
		return c
	}
	c := &cachedManifest{
		size:    info.Size(),
		mtime:   info.ModTime().UnixNano(),
		byName:  map[string]fimavRecord{},
		present: true,
	}
	data, err := os.ReadFile(path)
	if err == nil {
		var mf fimavManifestFile
		if json.Unmarshal(data, &mf) == nil {
			for _, rec := range mf.Matches {
				name := rec.FileName
				if name == "" && rec.FilePath != "" {
					name = filepath.Base(rec.FilePath)
				}
				if name == "" {
					continue
				}
				// Later records win: a re-recorded match replaces the old row.
				c.byName[name] = rec
			}
		}
	}
	manifestCache[dir] = c
	return c
}

// defaultCutWaitSeconds is how long we give FIM-AV Assistant to queue a cut
// after a recording stops, before deciding no cut is coming. Auto-cut is queued
// within a second or two of the recording stopping, so this only delays uploads
// when cutting is switched off entirely.
const defaultCutWaitSeconds = 120

// cutWaitSeconds resolves the configured grace period. 0 means "use the
// default"; a negative value switches the whole gate off.
func cutWaitSeconds(cfg eventConfig) int64 {
	if cfg.CutWaitSeconds == 0 {
		return defaultCutWaitSeconds
	}
	return int64(cfg.CutWaitSeconds)
}

// cutHold reports whether a file should be held back from upload because
// FIM-AV Assistant is going to replace it with a trimmed cut, plus a short
// reason for the log.
//
// It holds when the manifest says the cut is queued or running, and also during
// a grace window after the recording stops, since a queued cut isn't visible in
// the manifest until the metadata fetch that precedes it finishes.
func cutHold(dir, filename string, cfg eventConfig) (bool, string) {
	wait := cutWaitSeconds(cfg)
	if wait < 0 {
		return false, ""
	}
	mf := loadFimavManifest(dir)
	if !mf.present {
		return false, ""
	}
	rec, ok := mf.byName[filename]
	if !ok {
		// Not a FIM-AV recording (hand-placed file, or an older event's video
		// copied in). Leave it alone.
		return false, ""
	}
	if rec.Status == "recording" {
		return true, "still recording"
	}
	// A carded match is never cut: the card explanation lives in the dead time
	// the cut would remove. The raw file is final.
	if rec.HasCard {
		return false, ""
	}
	state := ""
	if rec.Processing != nil {
		state = rec.Processing.State
	}
	switch state {
	case "queued":
		return true, "cut queued"
	case "processing":
		return true, "cut running"
	case "done":
		return false, ""
	case "error":
		// The cut failed and the original was restored. Upload the raw video
		// rather than nothing.
		return false, ""
	}
	// No processing state yet: either a cut is about to be queued, or cutting
	// is switched off and this file is already final. Wait out the grace window
	// to tell the two apart.
	if rec.EndedAt > 0 && nowUnix()-rec.EndedAt/1000 >= wait {
		return false, ""
	}
	return true, "waiting for cut"
}
