package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Status values used for entries in state.videos.
const (
	statusNew       = "new"     // discovered, still being written to disk
	statusCutting   = "cutting" // FIM-AV Assistant is trimming it; hold the upload
	statusStable    = "stable"  // size+mtime stable, ready to upload
	statusUploading = "uploading"
	statusUploaded  = "uploaded"
	statusFailed    = "failed"
	statusSkipped   = "skipped"
)

// eventConfig is the user-editable per-event configuration.
//
// Field tags follow the JSON shape from tba-uploader-yt-upload-plan.md so the
// file on disk stays stable and human-editable.
type eventConfig struct {
	EventKey            string `json:"event_key"`
	EventName           string `json:"event_name"`
	ProfileName         string `json:"profile_name"`
	PlaylistName        string `json:"playlist_name"`
	TitleTemplate       string `json:"title_template"`
	DescriptionTemplate string `json:"description_template"`
	ThumbnailPath       string `json:"thumbnail_path"`
	// Visibility is PUBLIC, UNLISTED or PRIVATE. Empty means unlisted.
	Visibility      string `json:"visibility,omitempty"`
	IncludePractice bool   `json:"include_practice"`
	IncludeTest     bool   `json:"include_test"`
	// Browser* point the driver at an installed browser's own profile instead
	// of a profile this tool owns, so there is no second YouTube sign-in.
	// BrowserUserDataDir is the browser's "User Data" folder,
	// BrowserProfileDirectory the profile inside it ("Default", "Profile 2"),
	// BrowserDebugPort the CDP port to attach to when the browser is already
	// running, and BrowserExe an explicit browser binary.
	BrowserUserDataDir      string `json:"browser_user_data_dir,omitempty"`
	BrowserProfileDirectory string `json:"browser_profile_directory,omitempty"`
	BrowserDebugPort        int    `json:"browser_debug_port,omitempty"`
	BrowserExe              string `json:"browser_exe,omitempty"`
	// CutWaitSeconds is the grace period given to FIM-AV Assistant to queue a
	// dead-time cut before a recording is treated as final. 0 uses the default
	// (see defaultCutWaitSeconds); a negative value uploads recordings as soon
	// as they are stable, cut or not.
	CutWaitSeconds int `json:"cut_wait_seconds,omitempty"`
}

// allianceTeam is one team's data inside a match's red or blue alliance.
type allianceTeam struct {
	Number int    `json:"number"`
	Name   string `json:"name"`
}

// videoMeta is everything the upload pipeline needs that isn't derivable
// from the filename. Sent in by the Vue layer via /api/rename.
type videoMeta struct {
	TBAMatchKey string                    `json:"tba_match_key"`
	MatchLevel  string                    `json:"match_level"`
	MatchNumber int                       `json:"match_number"`
	MatchLabel  string                    `json:"match_label"`
	Play        int                       `json:"play"`
	Alliances   map[string][]allianceTeam `json:"alliances,omitempty"`
}

// videoEntry is one row in state.videos.
type videoEntry struct {
	Size        int64  `json:"size"`
	Mtime       int64  `json:"mtime"`
	Status      string `json:"status"`
	StableSince int64  `json:"stable_since,omitempty"`
	YTVideoID   string `json:"yt_video_id,omitempty"`
	TitleUsed   string `json:"title_used,omitempty"`
	UploadedAt  string `json:"uploaded_at,omitempty"`
	Attempts    int    `json:"attempts"`
	NextAttempt int64  `json:"next_attempt,omitempty"`
	LastError   string `json:"last_error,omitempty"`
	// HoldReason explains a "cutting" status, e.g. "cut running".
	HoldReason string `json:"hold_reason,omitempty"`
	// ChangedAfterUpload is set when the file on disk changed after we
	// published it, which means YouTube has the raw recording and the cut only
	// exists locally. The entry stays uploaded; this is here so the operator
	// can see it and re-upload by hand.
	ChangedAfterUpload bool       `json:"changed_after_upload,omitempty"`
	Meta               *videoMeta `json:"meta,omitempty"`
}

// eventState is the on-disk shape of state.json.
type eventState struct {
	Config          eventConfig            `json:"config"`
	Videos          map[string]*videoEntry `json:"videos"`
	ManualVideoIDs  map[string]string      `json:"manual_video_ids"`
	NeedsReauth     bool                   `json:"needs_reauth"`
	LastChannelName string                 `json:"last_channel_name,omitempty"`
}

// stateStore owns one event's state.json. All mutations go through a single
// mutex; saves are atomic via temp-file rename.
type stateStore struct {
	path  string
	mu    sync.Mutex
	state eventState
}

// dataRoot returns %LOCALAPPDATA%\TBA-uploader on Windows, otherwise
// $XDG_DATA_HOME/TBA-uploader (or ~/.local/share/TBA-uploader). On any OS it
// honours TBA_UPLOADER_DATA_DIR for tests.
func dataRoot() string {
	if v := os.Getenv("TBA_UPLOADER_DATA_DIR"); v != "" {
		return v
	}
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		return filepath.Join(v, "TBA-uploader")
	}
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "TBA-uploader")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "TBA-uploader")
}

func eventStatePath(eventKey string) string {
	return filepath.Join(dataRoot(), "events", eventKey, "state.json")
}

func profileDir(profileName string) string {
	return filepath.Join(dataRoot(), "profiles", profileName)
}

// listProfiles returns the names of all existing profile directories.
func listProfiles() ([]string, error) {
	root := filepath.Join(dataRoot(), "profiles")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// openStateStore loads state.json for the given event, creating an empty
// scaffold on disk if it doesn't exist yet.
func openStateStore(eventKey string) (*stateStore, error) {
	path := eventStatePath(eventKey)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	s := &stateStore{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.state = eventState{
			Config: eventConfig{
				EventKey:            eventKey,
				ProfileName:         defaultProfileName,
				Visibility:          defaultVisibility,
				TitleTemplate:       defaultTitleTemplate,
				DescriptionTemplate: defaultDescriptionTemplate,
			},
			Videos:         map[string]*videoEntry{},
			ManualVideoIDs: map[string]string{},
		}
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.state); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if s.state.Videos == nil {
		s.state.Videos = map[string]*videoEntry{}
	}
	if s.state.ManualVideoIDs == nil {
		s.state.ManualVideoIDs = map[string]string{}
	}
	if s.state.Config.EventKey == "" {
		s.state.Config.EventKey = eventKey
	}
	return s, nil
}

func (s *stateStore) snapshot() eventState {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Deep copy via JSON round-trip. State is small (one event); this keeps
	// API responses isolated from concurrent mutations.
	data, _ := json.Marshal(s.state)
	var out eventState
	_ = json.Unmarshal(data, &out)
	return out
}

func (s *stateStore) update(fn func(*eventState)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.state)
	return s.saveLocked()
}

// saveLocked writes state.json atomically. Caller must hold s.mu.
func (s *stateStore) saveLocked() error {
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// nowUnix returns the current time as a unix timestamp. Wrapped so tests can
// override it via the clock package's manipulations if needed.
func nowUnix() int64 {
	return time.Now().Unix()
}
