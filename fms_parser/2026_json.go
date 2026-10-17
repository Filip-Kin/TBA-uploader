package fms_parser

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path"
	"strconv"
)

// FMS 2026 no longer renders a score-detail HTML page for TBA-uploader to
// scrape. The match list (/FieldMonitor/MatchesPartialByLevel) is JSON, and
// each match's breakdown comes from
// /GameSpecific/api/v1.0/fieldmonitor_gs/GetScoreDetails?matchId=<fmsMatchId>,
// which returns ScoreDetailModel_2026. downloadMatches saves both into one
// .fmsjson file: {"match": <list item>, "score": <score details>}.
//
// The alliance objects in ScoreDetailModel_2026 already use the field names TBA
// publishes for 2026, so they are copied across rather than recomputed.

type FMSMatchJSON2026 struct {
	Match FMSMatchListItem2026 `json:"match"`
	Score struct {
		MatchLevel string                   `json:"matchLevel"`
		Alliances  []map[string]interface{} `json:"alliances"`
	} `json:"score"`
}

type FMSMatchListItem2026 struct {
	FmsMatchId      string `json:"fmsMatchId"`
	Description     string `json:"description"`
	MatchNumber     int    `json:"matchNumber"`
	PlayNumber      int    `json:"playNumber"`
	TeamNumberBlue1 int    `json:"teamNumberBlue1"`
	TeamNumberBlue2 int    `json:"teamNumberBlue2"`
	TeamNumberBlue3 int    `json:"teamNumberBlue3"`
	TeamNumberRed1  int    `json:"teamNumberRed1"`
	TeamNumberRed2  int    `json:"teamNumberRed2"`
	TeamNumberRed3  int    `json:"teamNumberRed3"`
}

// breakdown fields copied from each FMS alliance object
var jsonBreakdownFields2026 = []string{
	"adjustPoints",
	"autoTowerPoints",
	"endGameTowerPoints",
	"foulPoints",
	"g206Penalty",
	"minorFoulCount",
	"majorFoulCount",
	"rp",
	"totalAutoPoints",
	"totalPoints",
	"totalTeleopPoints",
	"totalTowerPoints",
	"autoTowerRobot1",
	"autoTowerRobot2",
	"autoTowerRobot3",
	"endGameTowerRobot1",
	"endGameTowerRobot2",
	"endGameTowerRobot3",
	"energizedAchieved",
	"superchargedAchieved",
	"traversalAchieved",
}

// bonus RP flags only mean something in qualification
var jsonQualOnlyFields2026 = map[string]bool{
	"energizedAchieved":    true,
	"superchargedAchieved": true,
	"traversalAchieved":    true,
}

func parseJSONtoJSON2026(filename string, config FMSParseConfig) (map[string]interface{}, error) {
	raw, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("Error opening file: %s: %s", filename, err)
	}
	var fms FMSMatchJSON2026
	if err := json.Unmarshal(raw, &fms); err != nil {
		return nil, fmt.Errorf("Error reading JSON from %s: %s", filename, err)
	}

	extra_info := make(map[string]extraMatchAllianceInfo2026)
	extra_info["blue"] = makeExtraMatchAllianceInfo2026()
	extra_info["red"] = makeExtraMatchAllianceInfo2026()
	extra_filename := filename[0:len(filename)-len(path.Ext(filename))] + ".extrajson"
	extra_raw, err := ioutil.ReadFile(extra_filename)
	if err == nil {
		err = json.Unmarshal(extra_raw, &extra_info)
		if err != nil {
			return nil, fmt.Errorf("Error reading JSON from %s: %s", extra_filename, err)
		}
	}

	teams := breakdownRobotFields[string]{
		blue: []string{
			strconv.Itoa(fms.Match.TeamNumberBlue1),
			strconv.Itoa(fms.Match.TeamNumberBlue2),
			strconv.Itoa(fms.Match.TeamNumberBlue3),
		},
		red: []string{
			strconv.Itoa(fms.Match.TeamNumberRed1),
			strconv.Itoa(fms.Match.TeamNumberRed2),
			strconv.Itoa(fms.Match.TeamNumberRed3),
		},
	}

	alliances := map[string]map[string]interface{}{
		"blue": {
			"surrogates": extra_info["blue"].Surrogates,
			"dqs":        extra_info["blue"].Dqs,
			"score":      -1,
		},
		"red": {
			"surrogates": extra_info["red"].Surrogates,
			"dqs":        extra_info["red"].Dqs,
			"score":      -1,
		},
	}
	assignTbaTeamsRaw(alliances, teams)

	breakdown := map[string]map[string]interface{}{
		"blue": make(map[string]interface{}),
		"red":  make(map[string]interface{}),
	}

	for _, fms_alliance := range fms.Score.Alliances {
		var color string
		switch fms_alliance["alliance"] {
		case "Blue":
			color = "blue"
		case "Red":
			color = "red"
		default:
			return nil, fmt.Errorf("%s: unknown alliance %v", filename, fms_alliance["alliance"])
		}

		for _, field := range jsonBreakdownFields2026 {
			if config.Playoff && jsonQualOnlyFields2026[field] {
				continue
			}
			value, ok := fms_alliance[field]
			if !ok {
				return nil, fmt.Errorf("%s: %s alliance is missing %s", filename, color, field)
			}
			// encoding/json decodes numbers as float64; every number here is an int
			if f, is_float := value.(float64); is_float {
				value = int(f)
			}
			breakdown[color][field] = value
		}

		hub_raw, err := json.Marshal(fms_alliance["hubScore"])
		if err != nil {
			return nil, fmt.Errorf("%s: %s hubScore: %s", filename, color, err)
		}
		hub := &hubScore2026{}
		if err := json.Unmarshal(hub_raw, hub); err != nil {
			return nil, fmt.Errorf("%s: %s hubScore: %s", filename, color, err)
		}
		breakdown[color]["hubScore"] = hub

		alliances[color]["score"] = breakdown[color]["totalPoints"]
	}

	if len(fms.Score.Alliances) != 2 {
		return nil, fmt.Errorf("%s: expected 2 alliances in score details, got %d", filename, len(fms.Score.Alliances))
	}

	if config.Playoff {
		assignBreakdownAllianceFieldsConst(breakdown, "rp", 0)
	}

	if config.EnabledExtraRps != nil {
		assignBreakdownExtraRps(breakdown, config.EnabledExtraRps, map[string][]bool{
			"red":  extra_info["red"].ExtraRps,
			"blue": extra_info["blue"].ExtraRps,
		}, "tba_extraRp")
	}

	return map[string]interface{}{
		"alliances":       alliances,
		"score_breakdown": breakdown,
	}, nil
}
