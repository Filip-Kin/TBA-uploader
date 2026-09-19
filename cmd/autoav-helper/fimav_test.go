package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeManifest drops a fimav-matches.json holding the given records.
func writeManifest(t *testing.T, dir string, recs ...fimavRecord) {
	t.Helper()
	data, err := json.Marshal(fimavManifestFile{Version: 1, Matches: recs})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, fimavManifest), data, 0o644); err != nil {
		t.Fatal(err)
	}
	// The manifest cache is keyed on size+mtime; bump mtime so successive
	// writes in one test are always seen.
	now := time.Now().Add(time.Duration(len(recs)) * time.Second)
	_ = os.Chtimes(filepath.Join(dir, fimavManifest), now, now)
}

func recorded(name string, endedAgo time.Duration) fimavRecord {
	return fimavRecord{
		ID:       name,
		FileName: name,
		FilePath: filepath.Join("C:\\AV", name),
		EndedAt:  time.Now().Add(-endedAgo).UnixMilli(),
		Status:   "recorded",
	}
}

func TestCutHold(t *testing.T) {
	cfg := eventConfig{}
	name := "QM5_MIKET.mp4"

	t.Run("no manifest", func(t *testing.T) {
		if hold, _ := cutHold(t.TempDir(), name, cfg); hold {
			t.Error("held with no manifest present")
		}
	})

	t.Run("file not in manifest", func(t *testing.T) {
		dir := t.TempDir()
		writeManifest(t, dir, recorded("QM1_MIKET.mp4", time.Hour))
		if hold, _ := cutHold(dir, name, cfg); hold {
			t.Error("held a file the manifest doesn't know about")
		}
	})

	t.Run("still recording", func(t *testing.T) {
		dir := t.TempDir()
		rec := recorded(name, 0)
		rec.Status = "recording"
		writeManifest(t, dir, rec)
		if hold, reason := cutHold(dir, name, cfg); !hold || reason != "still recording" {
			t.Errorf("hold=%v reason=%q", hold, reason)
		}
	})

	t.Run("cut queued and running", func(t *testing.T) {
		for state, wantReason := range map[string]string{
			"queued":     "cut queued",
			"processing": "cut running",
		} {
			dir := t.TempDir()
			rec := recorded(name, time.Hour)
			rec.Processing = &fimavProcessing{State: state}
			writeManifest(t, dir, rec)
			if hold, reason := cutHold(dir, name, cfg); !hold || reason != wantReason {
				t.Errorf("%s: hold=%v reason=%q", state, hold, reason)
			}
		}
	})

	t.Run("cut done", func(t *testing.T) {
		dir := t.TempDir()
		rec := recorded(name, time.Hour)
		rec.Processing = &fimavProcessing{State: "done", OutputPath: rec.FilePath}
		writeManifest(t, dir, rec)
		if hold, _ := cutHold(dir, name, cfg); hold {
			t.Error("held a finished cut")
		}
	})

	t.Run("cut failed uploads the raw video", func(t *testing.T) {
		dir := t.TempDir()
		rec := recorded(name, time.Hour)
		rec.Processing = &fimavProcessing{State: "error", Error: "ffmpeg exited 1"}
		writeManifest(t, dir, rec)
		if hold, _ := cutHold(dir, name, cfg); hold {
			t.Error("held a failed cut instead of uploading the original")
		}
	})

	t.Run("carded match is never cut", func(t *testing.T) {
		dir := t.TempDir()
		rec := recorded(name, 0)
		rec.HasCard = true
		writeManifest(t, dir, rec)
		if hold, _ := cutHold(dir, name, cfg); hold {
			t.Error("held a carded match, which FIM-AV never cuts")
		}
	})

	t.Run("grace window", func(t *testing.T) {
		dir := t.TempDir()
		writeManifest(t, dir, recorded(name, 5*time.Second))
		if hold, reason := cutHold(dir, name, cfg); !hold || reason != "waiting for cut" {
			t.Errorf("inside grace: hold=%v reason=%q", hold, reason)
		}

		dir2 := t.TempDir()
		writeManifest(t, dir2, recorded(name, (defaultCutWaitSeconds+10)*time.Second))
		if hold, _ := cutHold(dir2, name, cfg); hold {
			t.Error("still holding after the grace window; no cut was coming")
		}
	})

	t.Run("gate switched off", func(t *testing.T) {
		dir := t.TempDir()
		rec := recorded(name, 0)
		rec.Processing = &fimavProcessing{State: "processing"}
		writeManifest(t, dir, rec)
		if hold, _ := cutHold(dir, name, eventConfig{CutWaitSeconds: -1}); hold {
			t.Error("held despite cut_wait_seconds = -1")
		}
	})
}

// The whole point: a stable raw recording must not be promoted to "stable"
// (and therefore uploaded) while FIM-AV Assistant is still cutting it.
func TestScanHoldsFileUntilCutFinishes(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("TBA_UPLOADER_DATA_DIR", dataDir)
	videoDir := t.TempDir()

	oldVideoDir := settings.VideoDir
	settings.VideoDir = videoDir
	defer func() { settings.VideoDir = oldVideoDir }()

	name := "QM5_MIKET.mp4"
	path := filepath.Join(videoDir, name)
	if err := os.WriteFile(path, []byte("raw recording"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := recorded(name, 2*time.Second)
	rec.Processing = &fimavProcessing{State: "queued"}
	writeManifest(t, videoDir, rec)

	store, err := openStateStore("2026miket")
	if err != nil {
		t.Fatal(err)
	}
	m := newUploadManager(store, nil)

	// First scan discovers the file; backdate the stability timer so the next
	// scan is past stableDelay without the test having to wait for it.
	m.scanNow()
	if err := store.update(func(s *eventState) {
		s.Videos[name].StableSince = nowUnix() - int64(stableDelay/time.Second) - 1
	}); err != nil {
		t.Fatal(err)
	}

	m.scanNow()
	if got := store.snapshot().Videos[name]; got.Status != statusCutting {
		t.Fatalf("status = %q (%s), want cutting", got.Status, got.HoldReason)
	}
	if _, ok := m.pickNext(); ok {
		t.Fatal("picked a file that is still being cut")
	}

	// The cut finishes: ffmpeg wrote a new file in place, so size and mtime
	// changed, and the manifest now says done.
	if err := os.WriteFile(path, []byte("trimmed cut, shorter"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec.Processing = &fimavProcessing{State: "done"}
	writeManifest(t, videoDir, rec, recorded("QM6_MIKET.mp4", time.Hour))

	m.scanNow() // notices the change, resets the timer
	if got := store.snapshot().Videos[name].Status; got != statusNew {
		t.Fatalf("status = %q, want new after the file changed", got)
	}
	if err := store.update(func(s *eventState) {
		s.Videos[name].StableSince = nowUnix() - int64(stableDelay/time.Second) - 1
	}); err != nil {
		t.Fatal(err)
	}
	m.scanNow()

	got := store.snapshot().Videos[name]
	if got.Status != statusStable {
		t.Fatalf("status = %q (%s), want stable", got.Status, got.HoldReason)
	}
	if got.HoldReason != "" {
		t.Errorf("hold reason = %q, want empty", got.HoldReason)
	}
	if got.Size != int64(len("trimmed cut, shorter")) {
		t.Errorf("size = %d, want the cut's size", got.Size)
	}
}

// A cut that lands after we already published leaves YouTube holding the raw
// video. We can't unpublish it, but the operator has to be able to see it.
func TestScanFlagsChangeAfterUpload(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("TBA_UPLOADER_DATA_DIR", dataDir)
	videoDir := t.TempDir()

	oldVideoDir := settings.VideoDir
	settings.VideoDir = videoDir
	defer func() { settings.VideoDir = oldVideoDir }()

	name := "QM5_MIKET.mp4"
	path := filepath.Join(videoDir, name)
	if err := os.WriteFile(path, []byte("raw recording"), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := openStateStore("2026miket")
	if err != nil {
		t.Fatal(err)
	}
	m := newUploadManager(store, nil)
	m.scanNow()
	if err := store.update(func(s *eventState) {
		v := s.Videos[name]
		v.Status = statusUploaded
		v.YTVideoID = "abc11char23"
	}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("trimmed cut"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.scanNow()

	got := store.snapshot().Videos[name]
	if got.Status != statusUploaded {
		t.Errorf("status = %q, want uploaded (entries stay immutable)", got.Status)
	}
	if !got.ChangedAfterUpload {
		t.Error("changed_after_upload not set; the raw video is on YouTube silently")
	}
}
