package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"testing"

	"github.com/lethosor/TBA-uploader/fms_parser"
)

// Serves real FMS 2026 responses from tests/data/2026/fms-api: the match list
// and scores are from 2026mibg (BGRC) qualification 1, the rankings from
// 2026mibat.
func startFakeFMS2026(t *testing.T) *httptest.Server {
	routes := map[string]string{
		"/FieldMonitor/MatchesPartialByLevel":                    "MatchesPartialByLevel.json",
		"/GameSpecific/api/v1.0/fieldmonitor_gs/GetScoreDetails": "GetScoreDetails.json",
		"/api/v1.0/audience/get/GetQualificationRankData":        "GetQualificationRankData.json",
		"/api/v1.0/rankings/get/GetTeamRankings":                 "GetTeamRankings.json",
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		http.ServeFile(w, r, path.Join("tests/data/2026/fms-api", file))
	}))
}

func withFakeFMS2026(t *testing.T) {
	server := startFakeFMS2026(t)
	old := FMSConfig
	FMSConfig.FmsUrl = server.URL
	FMSConfig.DataFolder = t.TempDir()
	t.Cleanup(func() {
		server.Close()
		FMSConfig = old
	})
}

func TestDownloadMatchesJSON2026(t *testing.T) {
	withFakeFMS2026(t)

	files, err := downloadNewMatches(MATCH_LEVEL_QUAL, "2026mibg")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || path.Base(files[0]) != "1-1.fmsjson" {
		t.Fatalf("expected [1-1.fmsjson], got %v", files)
	}

	match, err := fms_parser.ParseHTMLtoJSON(2026, files[0], fms_parser.FMSParseConfig{})
	if err != nil {
		t.Fatal(err)
	}
	alliances := match["alliances"].(map[string]map[string]interface{})
	if alliances["blue"]["score"] != 216 || alliances["red"]["score"] != 251 {
		t.Errorf("scores: blue %v red %v", alliances["blue"]["score"], alliances["red"]["score"])
	}

	// Unchanged and not yet parsed to .json: still returned, so the caller parses it.
	files, err = downloadNewMatches(MATCH_LEVEL_QUAL, "2026mibg")
	if err != nil || len(files) != 1 {
		t.Fatalf("second fetch before .json exists: %v %v", files, err)
	}

	// Unchanged and parsed: nothing new.
	os.WriteFile(replaceExtension(files[0], "json"), []byte("{}"), 0644)
	files, err = downloadNewMatches(MATCH_LEVEL_QUAL, "2026mibg")
	if err != nil || len(files) != 0 {
		t.Fatalf("unchanged fetch: %v %v", files, err)
	}

	// Uploaded: skipped, even if FMS scores changed.
	matches_dir := getMatchDownloadPath(MATCH_LEVEL_QUAL, "2026mibg")
	os.WriteFile(path.Join(matches_dir, "1-1.fmsjson"), []byte("stale"), 0644)
	os.WriteFile(path.Join(matches_dir, "1-1.receipt"), []byte("1-1"), 0644)
	files, err = downloadNewMatches(MATCH_LEVEL_QUAL, "2026mibg")
	if err != nil || len(files) != 0 {
		t.Fatalf("uploaded match fetched again: %v %v", files, err)
	}
}

func TestDownloadMatchesJSON2026Changed(t *testing.T) {
	withFakeFMS2026(t)

	files, _ := downloadNewMatches(MATCH_LEVEL_QUAL, "2026mibg")
	os.WriteFile(replaceExtension(files[0], "json"), []byte("{}"), 0644)
	// Scores fetched before posting differ from what FMS has now.
	os.WriteFile(files[0], []byte("pre-post"), 0644)
	files, err := downloadNewMatches(MATCH_LEVEL_QUAL, "2026mibg")
	if err != nil || len(files) != 1 {
		t.Fatalf("changed scores not fetched again: %v %v", files, err)
	}
}

func TestDownloadRankings2026(t *testing.T) {
	withFakeFMS2026(t)

	out, err := downloadRankings(MATCH_LEVEL_QUAL, "2026mibat")
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Rankings []fmsRankingRow `json:"rankings"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Rankings) != 6 {
		t.Fatalf("expected 6 rankings, got %d", len(parsed.Rankings))
	}
	first := parsed.Rankings[0]
	if first.Rank != 1 || first.Team != 4 || first.Wins != 1 || first.Played != 1 || first.Sort1 != 3 || first.Sort2 != 10 {
		t.Errorf("rank 1: %+v", first)
	}
}
