package main

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var FMSConfig struct {
	FmsUrl     string `json:"fms_url"`
	DataFolder string `json:"data_folder"`
	TbaUrl     string `json:"tba_url"`
}

func checkFMSConnection() {
	// make sure the FMS server is running
	logger.Printf("Looking for FMS at %s...\n", FMSConfig.FmsUrl)
	client := http.Client{Timeout: 5 * time.Second}
	_, err := client.Get(FMSConfig.FmsUrl)
	if err != nil {
		logger.Println("Failed to connect to FMS!", err)
	} else {
		logger.Println("Found FMS")
	}
}

func downloadFile(folder string, filename string, url string, overwrite bool) (filepath string, ok bool, err error) {
	// return conditions:
	//      filepath: always
	//      ok: if the file exists now
	//      err: if the file was not downloaded
	err = os.MkdirAll(folder, os.ModePerm)
	if err != nil {
		logger.Printf("Failed to create folder: %s: %s\n", folder, err)
	}
	filepath = path.Join(folder, filename)
	ok = false
	if !overwrite {
		if fileExists(filepath) {
			// exists, don't overwrite
			return filepath, true, errors.New("already downloaded")
		}
	}

	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		return
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return
	}

	return filepath, true, nil
}

func getLevelDataPath(folder string, level int, subfolder string) string {
	return path.Join(FMSConfig.DataFolder, folder, fmt.Sprintf("level%d", level), subfolder)
}

func getMatchDownloadPath(level int, folder string) string {
	return getLevelDataPath(folder, level, "matches")
}

func getRankingDownloadPath(level int, folder string) string {
	return getLevelDataPath(folder, level, "rankings")
}

func downloadMatches(level int, folder string, new_only bool) ([]string, error) {
	url := fmt.Sprintf("%s/FieldMonitor/MatchesPartialByLevel?levelParam=%d", FMSConfig.FmsUrl, level)
	folder = path.Join(FMSConfig.DataFolder, folder, fmt.Sprintf("level%d", level))
	// ensure that the matches folder exists even if no matches are fetched
	matches_dir := path.Join(folder, "matches")
	backups_dir := path.Join(folder, "backups")
	var files []string
	os.MkdirAll(matches_dir, os.ModePerm)
	os.MkdirAll(backups_dir, os.ModePerm)

	if level == MATCH_LEVEL_MANUAL {
		html_files, err := listFilesWithExtension(matches_dir, "html")
		if err != nil {
			return nil, err
		}
		for _, html_file := range html_files {
			if !fileExists(path.Join(matches_dir, replaceExtension(html_file.Name(), "json"))) {
				files = append(files, path.Join(matches_dir, html_file.Name()))
			}
		}
		return files, nil
	}

	filename, ok, err := downloadFile(folder, "match_list.html", url, true)
	if !ok {
		return nil, err
	}
	list_raw, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(list_raw)) > 0 && bytes.TrimSpace(list_raw)[0] == '{' {
		// FMS 2026+: the match list is JSON
		return downloadMatchesJSON(list_raw, matches_dir, backups_dir, new_only)
	}
	reader, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	dom, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return nil, err
	}

	dom.Find("tbody tr").Each(func(i int, row *goquery.Selection) {
		match_url, ok := row.Find("a").First().Attr("href")
		if !ok {
			logger.Printf("Couldn't find link in row %d\n", i)
			return
		}
		match_url = FMSConfig.FmsUrl + match_url
		button := row.Find("a.btn-success, button").First() // link as of 2024
		button_text := strings.Replace(button.Text(), " ", "", -1)
		button_text = strings.Replace(button_text, "/", "-", -1)
		filename, ok, err := downloadFile(matches_dir, button_text+".html", match_url, !new_only)
		if !ok {
			logger.Printf("Failed to download %s: %s\n", button_text, err)
		} else if err == nil {
			files = append(files, filename)

			var err error
			file_content, err := ioutil.ReadFile(filename)
			if err != nil {
				logger.Printf("Failed to hash %s: %s\n", filename, err)
			} else {
				hash := md5.Sum(file_content)
				dest := path.Join(backups_dir, fmt.Sprintf("%s-%x.html", button_text, hash))
				if !fileExists(dest) {
					err = copyFile(filename, dest, false)
					if err != nil {
						logger.Printf("Failed to back up %s to %s: %s\n", filename, dest, err)
					}
				}
			}
		}
	})
	return files, nil
}

func fmsGetJSON(url string) ([]byte, error) {
	client := http.Client{Timeout: 10 * time.Second}
	response, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, response.StatusCode)
	}
	return body, nil
}

// downloadMatchesJSON handles FMS 2026+, where the match list is JSON and each
// match's score breakdown comes from the GameSpecific API. The list contains
// every match whose teleop has ended, which includes a match still waiting for
// its scores to be posted. So every match not yet uploaded (no .receipt) is
// fetched again on each call, and a file is returned whenever its content
// changed, so posted or edited scores replace what was fetched before.
func downloadMatchesJSON(list_raw []byte, matches_dir, backups_dir string, new_only bool) ([]string, error) {
	var list struct {
		Matches []map[string]interface{} `json:"matches"`
	}
	if err := json.Unmarshal(list_raw, &list); err != nil {
		return nil, fmt.Errorf("match list: %s", err)
	}

	var files []string
	for _, match := range list.Matches {
		match_id, _ := match["fmsMatchId"].(string)
		match_number, _ := match["matchNumber"].(float64)
		play_number, _ := match["playNumber"].(float64)
		if match_id == "" || match_number < 1 {
			logger.Printf("Skipping match list entry with no ID or number: %v\n", match)
			continue
		}
		name := fmt.Sprintf("%d-%d", int(match_number), int(play_number))
		match_path := path.Join(matches_dir, name+".fmsjson")
		if new_only && fileExists(path.Join(matches_dir, name+".receipt")) {
			continue
		}

		score_raw, err := fmsGetJSON(fmt.Sprintf("%s/GameSpecific/api/v1.0/fieldmonitor_gs/GetScoreDetails?matchId=%s", FMSConfig.FmsUrl, match_id))
		if err != nil {
			logger.Printf("Failed to download scores for %s: %s\n", name, err)
			continue
		}
		var score interface{}
		if err := json.Unmarshal(score_raw, &score); err != nil {
			logger.Printf("Failed to parse scores for %s: %s\n", name, err)
			continue
		}
		content, err := json.MarshalIndent(map[string]interface{}{
			"match": match,
			"score": score,
		}, "", "  ")
		if err != nil {
			return nil, err
		}

		old_content, read_err := ioutil.ReadFile(match_path)
		if new_only && read_err == nil && bytes.Equal(old_content, content) && fileExists(replaceExtension(match_path, "json")) {
			continue
		}
		if err := ioutil.WriteFile(match_path, content, os.ModePerm); err != nil {
			return nil, err
		}
		files = append(files, match_path)

		dest := path.Join(backups_dir, fmt.Sprintf("%s-%x.fmsjson", name, md5.Sum(content)))
		if !fileExists(dest) {
			if err := ioutil.WriteFile(dest, content, os.ModePerm); err != nil {
				logger.Printf("Failed to back up %s to %s: %s\n", match_path, dest, err)
			}
		}
	}
	return files, nil
}

func copyFile(src, dest string, overwrite bool) error {
	if !overwrite && fileExists(dest) {
		return errors.New(fmt.Sprintf("destination already exists: %s", dest))
	}
	content, err := ioutil.ReadFile(src)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(dest, content, os.ModePerm)
}

func downloadNewMatches(level int, folder string) ([]string, error) {
	return downloadMatches(level, folder, true)
}

func downloadAllMatches(level int, folder string) ([]string, error) {
	return downloadMatches(level, folder, false)
}

func downloadRankings(level int, folder string) ([]byte, error) {
	ranking_path := getRankingDownloadPath(level, folder)
	os.MkdirAll(ranking_path, os.ModePerm)

	out, api_err := downloadRankingsAPI()
	if api_err != nil {
		logger.Printf("Ranking API fetch failed, trying the pit display route: %s\n", api_err)
		var pit_err error
		out, pit_err = downloadRankingsPit()
		if pit_err != nil {
			return nil, fmt.Errorf("%s; %s", api_err, pit_err)
		}
	}

	ranking_files, _ := listFilesWithExtension(ranking_path, "json")
	i := len(ranking_files) + 1
	var dest_filename string
	for {
		dest_filename = path.Join(ranking_path, fmt.Sprintf("%05d.json", i))
		if !fileExists(dest_filename) {
			break
		}
		i += 1
	}
	err := ioutil.WriteFile(dest_filename, out, os.ModePerm)
	return out, err
}

// fmsRankingRow is the row shape the web UI expects (it matches what the
// pre-2026 pit display route returned).
type fmsRankingRow struct {
	Rank   int     `json:"rank"`
	Team   int     `json:"team"`
	Wins   int     `json:"wins"`
	Losses int     `json:"losses"`
	Ties   int     `json:"ties"`
	Played int     `json:"played"`
	DQ     float64 `json:"dq"`
	Sort1  float64 `json:"sort1"`
	Sort2  float64 `json:"sort2"`
	Sort3  float64 `json:"sort3"`
	Sort4  float64 `json:"sort4"`
	Sort5  float64 `json:"sort5"`
	Sort6  float64 `json:"sort6"`
}

// downloadRankingsAPI reads rankings from FMS 2026+. The pit display route is
// gone. GetQualificationRankData has team numbers but no played or DQ counts;
// GetTeamRankings has those but only FMS team IDs. Both read the same
// TeamRankings rows, so they are joined on rank and checked against each
// other.
func downloadRankingsAPI() ([]byte, error) {
	rank_data_raw, err := fmsGetJSON(FMSConfig.FmsUrl + "/api/v1.0/audience/get/GetQualificationRankData")
	if err != nil {
		return nil, err
	}
	var rank_data struct {
		TeamRanks []struct {
			Rank       int     `json:"rank"`
			TeamNumber int     `json:"teamNumber"`
			Sort1      float64 `json:"sort1"`
			Sort2      float64 `json:"sort2"`
			Sort3      float64 `json:"sort3"`
			Sort4      float64 `json:"sort4"`
			Sort5      float64 `json:"sort5"`
			Sort6      float64 `json:"sort6"`
			Wins       int     `json:"wins"`
			Losses     int     `json:"losses"`
			Ties       int     `json:"ties"`
		} `json:"teamRanks"`
	}
	if err := json.Unmarshal(rank_data_raw, &rank_data); err != nil {
		return nil, fmt.Errorf("GetQualificationRankData: %s", err)
	}

	team_rankings_raw, err := fmsGetJSON(FMSConfig.FmsUrl + "/api/v1.0/rankings/get/GetTeamRankings")
	if err != nil {
		return nil, err
	}
	var team_rankings []struct {
		Ranking       int     `json:"ranking"`
		Wins          int     `json:"wins"`
		Losses        int     `json:"losses"`
		Ties          int     `json:"ties"`
		MatchesPlayed int     `json:"matchesPlayed"`
		Disqualified  float64 `json:"disqualified"`
		SortOrder1    float64 `json:"sortOrder1"`
	}
	if err := json.Unmarshal(team_rankings_raw, &team_rankings); err != nil {
		return nil, fmt.Errorf("GetTeamRankings: %s", err)
	}

	by_rank := make(map[int]int)
	for i, r := range team_rankings {
		if _, dup := by_rank[r.Ranking]; dup {
			return nil, fmt.Errorf("GetTeamRankings: rank %d appears twice", r.Ranking)
		}
		by_rank[r.Ranking] = i
	}

	rows := make([]fmsRankingRow, 0, len(rank_data.TeamRanks))
	for _, r := range rank_data.TeamRanks {
		i, ok := by_rank[r.Rank]
		if !ok {
			return nil, fmt.Errorf("rank %d (team %d) missing from GetTeamRankings; rankings may have changed mid-fetch, try again", r.Rank, r.TeamNumber)
		}
		t := team_rankings[i]
		if t.Wins != r.Wins || t.Losses != r.Losses || t.Ties != r.Ties || t.SortOrder1 != r.Sort1 {
			return nil, fmt.Errorf("rank %d (team %d) differs between FMS ranking routes; rankings may have changed mid-fetch, try again", r.Rank, r.TeamNumber)
		}
		rows = append(rows, fmsRankingRow{
			Rank:   r.Rank,
			Team:   r.TeamNumber,
			Wins:   r.Wins,
			Losses: r.Losses,
			Ties:   r.Ties,
			Played: t.MatchesPlayed,
			DQ:     t.Disqualified,
			Sort1:  r.Sort1,
			Sort2:  r.Sort2,
			Sort3:  r.Sort3,
			Sort4:  r.Sort4,
			Sort5:  r.Sort5,
			Sort6:  r.Sort6,
		})
	}
	if len(rows) != len(team_rankings) {
		return nil, fmt.Errorf("FMS ranking routes disagree on team count (%d vs %d); try again", len(rows), len(team_rankings))
	}

	return json.Marshal(map[string]interface{}{"rankings": rows})
}

// downloadRankingsPit reads rankings from the pit display route FMS served
// before 2026.
func downloadRankingsPit() ([]byte, error) {
	// event ID, then cache ID - both can be arbitrary. Appears that only changing the event ID bypasses the cache.
	// for sanity, still allow caching for 30sec.
	cache_key := time.Now().Unix() / 30
	cache_key_uuid := fmt.Sprintf("00000000-0000-0000-%04x-%012x", (cache_key>>(12*4))&0xffff, cache_key&0xffffffffffff)

	request, err := http.NewRequest("GET", FMSConfig.FmsUrl+"/"+cache_key_uuid+"/refreshpiteventqualificationmodel/00000000-0000-0000-0000-000000000000", nil)
	if err != nil {
		return nil, err
	}
	client := http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	out, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pit display route: HTTP %d", response.StatusCode)
	}
	return out, nil
}

type reportPage map[string]interface{}

func makeReportRequest(report_action, report_type string, headers map[string]string, body_fields map[string]interface{}) (map[string]interface{}, error) {
	custom_data_raw, _ := json.Marshal([]map[string]string{{
		"reportType": report_type,
	}})
	request_body := map[string]interface{}{
		"reportAction":    report_action,
		"controlId":       "sfreportviewer",
		"reportPath":      "",
		"reportServerUrl": "",
		"processingMode":  "local",
		"locale":          "en-US",
		"customData":      string(custom_data_raw),
	}
	if body_fields != nil {
		for field, value := range body_fields {
			request_body[field] = value
		}
	}
	body_encoded, err := json.Marshal(request_body)
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequest("POST", FMSConfig.FmsUrl+"/Reports/PostReportAction", bytes.NewReader(body_encoded))
	if err != nil {
		return nil, err
	}

	request.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	request.Header.Set("Accept-Language", "en-US,en;q=0.9")
	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	request.Header.Set("DNT", "1")
	request.Header.Set("Origin", "http://10.0.100.5")
	request.Header.Set("Referer", "http://10.0.100.5/Reports/"+report_type)
	request.Header.Set("User-Agent", "Mozilla/5.0")
	if headers != nil {
		for header_name, header_value := range headers {
			request.Header.Set(header_name, header_value)
		}
	}

	client := http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}

	response_raw, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}

	out := make(map[string]interface{})
	err = json.Unmarshal(response_raw, &out)
	if err != nil {
		return nil, err
	}

	return out, nil
}

func getReportToken(report_type string) (string, error) {
	response, err := makeReportRequest("ReportLoad", report_type, nil, nil)
	if err != nil {
		return "", errors.New(fmt.Sprintf("makeReportRequest: %s", err))
	}

	return readFromStringGenericMap[string](response, "reportViewerID")
}

func downloadReportPage(report_type string, page int, token string) (reportPage, error) {
	return makeReportRequest("GetPageModel", report_type, nil, map[string]interface{}{
		"dataRefresh":          true,
		"dataSources":          nil,
		"isPrint":              true,
		"pageindex":            page,
		"pageInit":             true,
		"parameters":           nil,
		"refresh":              false,
		"reportViewerClientId": token,
		"reportViewerToken":    token,
	})

}

func downloadReport(report_type string) ([]reportPage, error) {
	token, err := getReportToken(report_type)
	if err != nil {
		return nil, err
	}

	page1, err := downloadReportPage(report_type, 1, token)
	if err != nil {
		return nil, err
	}

	page_count, err := readFromStringGenericMap[float64](page1, "reportPageModel", "TotalPages")
	if err != nil {
		return nil, err
	}

	pages := []reportPage{page1}
	for i := 2; i <= int(page_count); i++ {
		next_page, err := downloadReportPage(report_type, i, token)
		if err != nil {
			return nil, err
		}
		pages = append(pages, next_page)
	}
	return pages, nil
}
