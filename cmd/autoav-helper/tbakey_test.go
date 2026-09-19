package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTbaMatchKey(t *testing.T) {
	cases := []struct {
		filename string
		want     string
	}{
		// FIM-AV in-season names
		{"QM5_MIKET.mp4", "2026fsu_qm5"},
		{"QM12_P2_MIKET.mp4", "2026fsu_qm12"}, // a replay is still the same match
		{"SF3M1_MIKET.mp4", "2026fsu_sf3m1"},
		{"SF13M1_MIKET.mp4", "2026fsu_sf13m1"},
		{"F1M2_MIKET.mp4", "2026fsu_f1m2"},
		// TBA-uploader's own names
		{"2026 FSU Roboday Qualification Match 7.mp4", "2026fsu_qm7"},
		{"2026 FSU Roboday Playoff Match 4.mp4", "2026fsu_sf4m1"},
		{"2026 FSU Roboday Final Match 1.mp4", "2026fsu_f1m1"},
		// Not matches TBA tracks
		{"zz_PR1_MIKET.mp4", ""},
		{"zz_TM1_MIKET.mp4", ""},
		{"2026 FSU Roboday Practice Match 2.mp4", ""},
		{"random recording.mp4", ""},
	}
	for _, c := range cases {
		p, ok := parseFilename(c.filename)
		if !ok {
			if c.want != "" {
				t.Errorf("%q did not parse", c.filename)
			}
			continue
		}
		if got := tbaMatchKey("2026fsu", p); got != c.want {
			t.Errorf("tbaMatchKey(%q) = %q, want %q", c.filename, got, c.want)
		}
	}

	// Playoff numbering past the bracket's elimination matches becomes finals,
	// which is how FMS numbers them.
	p, _ := parseFilename("2026 FSU Roboday Playoff Match 14.mp4")
	if got := tbaMatchKey("2026fsu", p); got != "2026fsu_f1m1" {
		t.Errorf("playoff 14 = %q, want 2026fsu_f1m1", got)
	}

	// No event key, no link.
	p, _ = parseFilename("QM5_MIKET.mp4")
	if got := tbaMatchKey("", p); got != "" {
		t.Errorf("empty event key produced %q", got)
	}
}

func TestFillMetaFromFilename(t *testing.T) {
	entry := &videoEntry{}
	fillMetaFromFilename(entry, "QM5_MIKET.mp4", "2026fsu")
	if entry.Meta == nil || entry.Meta.TBAMatchKey != "2026fsu_qm5" {
		t.Fatalf("meta = %+v", entry.Meta)
	}
	if entry.Meta.MatchLabel != "Qualification 5" || entry.Meta.MatchNumber != 5 || entry.Meta.Play != 1 {
		t.Errorf("meta = %+v", entry.Meta)
	}

	// Richer meta from /api/rename must not be overwritten.
	existing := &videoEntry{Meta: &videoMeta{
		TBAMatchKey: "2026fsu_qm9",
		MatchLabel:  "Qualification 9",
		Alliances:   map[string][]allianceTeam{"red": {{Number: 2767, Name: "Stryke Force"}}},
	}}
	fillMetaFromFilename(existing, "QM5_MIKET.mp4", "2026fsu")
	if existing.Meta.TBAMatchKey != "2026fsu_qm9" || len(existing.Meta.Alliances) != 1 {
		t.Errorf("existing meta was disturbed: %+v", existing.Meta)
	}

	// A practice recording gets no key, and no empty meta object either.
	practice := &videoEntry{}
	fillMetaFromFilename(practice, "zz_PR1_MIKET.mp4", "2026fsu")
	if practice.Meta != nil {
		t.Errorf("practice meta = %+v, want nil", practice.Meta)
	}
}

// A scan must give every match video its TBA key, since that is what the UI
// links and submits on.
func TestScanFillsTbaMatchKey(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("TBA_UPLOADER_DATA_DIR", dataDir)
	videoDir := t.TempDir()

	oldVideoDir := settings.VideoDir
	settings.VideoDir = videoDir
	defer func() { settings.VideoDir = oldVideoDir }()

	for _, name := range []string{"QM5_MIKET.mp4", "zz_PR1_MIKET.mp4"} {
		if err := os.WriteFile(filepath.Join(videoDir, name), []byte("video"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	store, err := openStateStore("2026fsu")
	if err != nil {
		t.Fatal(err)
	}
	m := newUploadManager(store, nil)
	m.scanNow()
	time.Sleep(10 * time.Millisecond)

	videos := store.snapshot().Videos
	qm := videos["QM5_MIKET.mp4"]
	if qm == nil || qm.Meta == nil || qm.Meta.TBAMatchKey != "2026fsu_qm5" {
		t.Errorf("qualification entry meta = %+v", qm)
	}
	if pr := videos["zz_PR1_MIKET.mp4"]; pr != nil && pr.Meta != nil {
		t.Errorf("practice entry got meta %+v", pr.Meta)
	}
}
