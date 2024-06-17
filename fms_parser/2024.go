package fms_parser

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

type fmsScoreInfo2024 struct {
	auto   int
	teleop int
	fouls  int
	total  int
	// year-specific:
	// auto_charge_station   int
	// teleop_charge_station int
	// link                  int
}

func makeFmsScoreInfo2024() fmsScoreInfo2024 {
	return fmsScoreInfo2024{}
}

type extraMatchAllianceInfo2024 struct {
	extraMatchAllianceInfoCommon
}

func makeExtraMatchAllianceInfo2024() extraMatchAllianceInfo2024 {
	return extraMatchAllianceInfo2024{
		extraMatchAllianceInfoCommon: makeExtraMatchAllianceInfoCommon(),
	}
}

func addManualFields2024(breakdown map[string]interface{}, info fmsScoreInfo2024, extra extraMatchAllianceInfo2024, playoff bool) {
	// breakdown["totalChargeStationPoints"] = info.auto_charge_station + info.teleop_charge_station

	if _, ok := breakdown["adjustPoints"]; !ok {
		// adjust should be negative when total = 0
		breakdown["adjustPoints"] = info.total - info.auto - info.teleop - info.fouls
	}
}

// const (
// 	K2023_COMMUNITY_BOTTOM = "Bottom"
// 	K2023_COMMUNITY_MIDDLE = "Middle"
// 	K2023_COMMUNITY_TOP    = "Top"

// 	K2023_COMMUNITY_NONE = "None"
// 	K2023_COMMUNITY_CUBE = "Cube"
// 	K2023_COMMUNITY_CONE = "Cone"
// )

// map FMS names (lowercase) to API names of basic integer fields
var simpleIntFields2024 = map[string]string{
	// general
	"adjustments": "adjustPoints",
	// year-specific
	"leave points":                    "autoLeavePoints",
	"speaker note amplified count":    "teleopSpeakerNoteAmplifiedCount",
	"speaker note amplified points":   "teleopSpeakerNoteAmplifiedPoints",
	"endgame harmony points":          "endGameHarmonyPoints",
	"endgame note in trap points":     "endGameNoteInTrapPoints",
	"endgame on stage points":         "endGameOnStagePoints",
	"endgame park points":             "endGameParkPoints",
	"endgame spot light bonus points": "endGameSpotLightBonusPoints",
}

// Map FMS names (lowercase) to API name suffixes of basic integer fields.
// The match phase ("auto" or "teleop") will be prepended to the API names as appropriate.
var simpleIntMatchPhaseFields2024 = map[string]string{
	"amp note count":      "AmpNoteCount",
	"amp note points":     "AmpNotePoints",
	"speaker note count":  "SpeakerNoteCount",
	"speaker note points": "SpeakerNotePoints",
}

var simpleStringFields2024 = map[string]string{}

var simpleIconFields2024 = map[string]string{
	"coop button pressed": "coopNotePlayed", // TODO: verify
	"coopertition bonus":  "coopertitionBonusAchieved",
	"ensemble":            "ensembleBonusAchieved",
	"melody":              "melodyBonusAchieved",
}

var penaltyFields2024 = map[string]string{
	"G206": "g206Penalty",
	"G408": "g408Penalty",
	"G424": "g424Penalty",
}

var DEFAULT_BREAKDOWN_VALUES_2024 = map[string]any{}

// year-specific:

var stageFields2024 = map[string]string{
	"note in trap": "trap",
	"spotlit":      "mic",
}

func parseHTMLtoJSON2024(filename string, config FMSParseConfig) (map[string]interface{}, error) {
	//////////////////////////////////////////////////
	// Parse html from FMS into TBA-compatible JSON //
	//////////////////////////////////////////////////

	// Open file
	r, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("Error opening file: %s: %s", filename, err)
	}
	defer r.Close()

	// Read from file
	dom, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, fmt.Errorf("Error reading from file: %s: %s", filename, err)
	}

	all_json := make(map[string]interface{})

	extra_info := make(map[string]extraMatchAllianceInfo2024)
	extra_info["blue"] = makeExtraMatchAllianceInfo2024()
	extra_info["red"] = makeExtraMatchAllianceInfo2024()
	extra_filename := filename[0:len(filename)-len(path.Ext(filename))] + ".extrajson"
	extra_raw, err := ioutil.ReadFile(extra_filename)
	if err == nil {
		err = json.Unmarshal(extra_raw, &extra_info)
		if err != nil {
			return nil, fmt.Errorf("Error reading JSON from %s: %s", extra_filename, err)
		}
	}

	alliances := map[string]map[string]interface{}{
		"blue": {
			"teams":      make([]string, 3),
			"surrogates": extra_info["blue"].Surrogates,
			"dqs":        extra_info["blue"].Dqs,
			"score":      -1,
		},
		"red": {
			"teams":      make([]string, 3),
			"surrogates": extra_info["red"].Surrogates,
			"dqs":        extra_info["red"].Dqs,
			"score":      -1,
		},
	}

	breakdown := map[string]map[string]interface{}{
		"blue": make(map[string]interface{}),
		"red":  make(map[string]interface{}),
	}

	var scoreInfo = struct {
		blue fmsScoreInfo2024
		red  fmsScoreInfo2024
	}{
		makeFmsScoreInfo2024(),
		makeFmsScoreInfo2024(),
	}

	parse_errors := make([]string, 0)

	checkParseInt := func(s, desc string) int {
		n, err := strconv.ParseInt(s, 10, 0)
		if err != nil {
			panic(fmt.Sprintf("parse int %s failed: %s", desc, err))
		}
		return int(n)
	}

	match_phase := ""
	validateMatchPhase := func(desc string) {
		if match_phase == "" {
			panic(fmt.Sprintf("no active match phase: %s", desc))
		}
	}
	// matchPhaseWithEndGame := func() string {
	// 	validateMatchPhase(match_phase)
	// 	if match_phase == "teleop" {
	// 		return "endGame"
	// 	}
	// 	return match_phase
	// }

	// var cur_community struct {
	// 	blue *Community2024
	// 	red  *Community2024
	// }
	// communityRowToKey := func(row_name string) string {
	// 	if row_name == "bottom" {
	// 		return K2024_COMMUNITY_BOTTOM
	// 	} else if row_name == "middle" {
	// 		return K2024_COMMUNITY_MIDDLE
	// 	} else if row_name == "top" {
	// 		return K2024_COMMUNITY_TOP
	// 	}
	// 	panic("invalid community row name: " + row_name)
	// }

	dom.Find("tr").Each(func(i int, s *goquery.Selection) {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("Parse error in %s: %s\n%s", filename, r, debug.Stack())
				parse_errors = append(parse_errors, fmt.Sprint(r))
			}
		}()

		columns := s.Children()
		if columns.Length() < 1 {
			return // continue
		}

		row_name := strings.ToLower(strings.TrimSpace(columns.Eq(0).Text()))
		if row_name == "" || row_name == "match score item" {
			return // continue
		}

		// if row_name == "community" {
		// 	if cur_community.red != nil {
		// 		panic("found community before end ")
		// 	}
		// 	cur_community.blue = makeCommunity2023()
		// 	cur_community.red = makeCommunity2023()
		// 	return // continue
		// }

		if columns.Length() == 3 {
			if row_name == "leave" {
				match_phase = "auto"
			}

			blue_cell := columns.Eq(1)
			red_cell := columns.Eq(2)
			blue_text := strings.TrimSpace(blue_cell.Text())
			red_text := strings.TrimSpace(red_cell.Text())

			parseIntWrapper := func(s, alliance string) int {
				return checkParseInt(s, alliance+" "+row_name)
			}

			// if cur_community.red != nil {
			// 	cur_community.blue.parseCommunityRow(communityRowToKey(row_name), blue_cell)
			// 	cur_community.red.parseCommunityRow(communityRowToKey(row_name), red_cell)

			// 	if cur_community.red.isComplete() {
			// 		api_field := match_phase + "Community"
			// 		cur_community.blue.assignPiecesToBreakdown(breakdown["blue"], api_field)
			// 		cur_community.red.assignPiecesToBreakdown(breakdown["red"], api_field)
			// 		if match_phase == "teleop" {
			// 			cur_community.blue.assignLinksToBreakdown(breakdown["blue"], "links")
			// 			cur_community.red.assignLinksToBreakdown(breakdown["red"], "links")
			// 		}
			// 		cur_community.blue = nil
			// 		cur_community.red = nil
			// 	}
			// 	return // continue
			// }

			// Handle each data row
			if api_field, ok := simpleStringFields2024[row_name]; ok {
				assignBreakdownAllianceFields(breakdown, api_field, identity_fn[string], breakdownAllianceFields[string]{
					blue: blue_text,
					red:  red_text,
				})
			} else if api_field, ok := simpleIntFields2024[row_name]; ok {
				assignBreakdownAllianceFields(breakdown, api_field, identity_fn[int], breakdownAllianceFields[int]{
					blue: checkParseInt(blue_text, "blue "+api_field),
					red:  checkParseInt(red_text, "red "+api_field),
				})
			} else if api_field_suffix, ok := simpleIntMatchPhaseFields2024[row_name]; ok {
				validateMatchPhase(match_phase)
				api_field := match_phase + api_field_suffix
				assignBreakdownAllianceFields(breakdown, api_field, identity_fn[int], breakdownAllianceFields[int]{
					blue: checkParseInt(blue_text, "blue "+api_field),
					red:  checkParseInt(red_text, "red "+api_field),
				})
			} else if api_field, ok := simpleIconFields2024[row_name]; ok {
				assignBreakdownAllianceFields[bool](breakdown, api_field, identity_fn[bool], breakdownAllianceFields[bool]{
					blue: iconToBool(blue_cell.Find("i"), "fa-check", "fa-times"),
					red:  iconToBool(red_cell.Find("i"), "fa-check", "fa-times"),
				})
			} else if row_name == "teams" {
				assignTbaTeams(alliances, breakdownAllianceFields[*goquery.Selection]{
					blue: blue_cell,
					red:  red_cell,
				})
			} else if row_name == "final score" {
				blue_score := checkParseInt(blue_text, "blue final score")
				red_score := checkParseInt(red_text, "red final score")
				breakdown["blue"]["totalPoints"] = blue_score
				breakdown["red"]["totalPoints"] = red_score
				alliances["blue"]["score"] = blue_score
				alliances["red"]["score"] = red_score
				scoreInfo.blue.total = blue_score
				scoreInfo.red.total = red_score
			} else if row_name == "ranking points" {
				blue_rp := checkParseInt(blue_text, "blue ranking points")
				red_rp := checkParseInt(red_text, "red ranking points")
				breakdown["blue"]["rp"] = blue_rp
				breakdown["red"]["rp"] = red_rp
			} else if row_name == "autonomous points" {
				blue_points := checkParseInt(blue_text, "blue "+row_name)
				red_points := checkParseInt(red_text, "red "+row_name)
				assignBreakdownAllianceFields(breakdown, "autoPoints", identity_fn[int], breakdownAllianceFields[int]{
					blue: blue_points,
					red:  red_points,
				})
				scoreInfo.blue.auto = blue_points
				scoreInfo.red.auto = red_points
				match_phase = "teleop"
			} else if row_name == "teleop points" {
				blue_points := checkParseInt(blue_text, "blue "+row_name)
				red_points := checkParseInt(red_text, "red "+row_name)
				assignBreakdownAllianceFields(breakdown, "teleopPoints", identity_fn[int], breakdownAllianceFields[int]{
					blue: blue_points,
					red:  red_points,
				})
				scoreInfo.blue.teleop = blue_points
				scoreInfo.red.teleop = red_points
				match_phase = ""
			} else if row_name == "foul points" {
				blue_points := checkParseInt(blue_text, "blue "+row_name)
				red_points := checkParseInt(red_text, "red "+row_name)
				assignBreakdownAllianceFields(breakdown, "foulPoints", identity_fn[int], breakdownAllianceFields[int]{
					blue: blue_points,
					red:  red_points,
				})
				scoreInfo.blue.fouls = blue_points
				scoreInfo.red.fouls = red_points
			} else if row_name == "fouls/techs committed" {
				assignBreakdownAllianceMultipleFields(breakdown, []string{"foulCount", "techFoulCount"}, parseIntWrapper, breakdownAllianceMultipleFields[string]{
					blue: split_and_strip(blue_text, "•"),
					red:  split_and_strip(red_text, "•"),
				})
			} else if row_name == "penalties" {
				assignPenaltyFields(breakdown, penaltyFields2024, breakdownAllianceFields[*goquery.Selection]{
					blue: blue_cell,
					red:  red_cell,
				})

				// begin year-specific
			} else if row_name == "leave" {
				assignBreakdownRobotFields(breakdown, "autoLineRobot", boolToYesNo, breakdownRobotFields[bool]{
					blue: iconsToBools(blue_cell, 3, "fa-check", "fa-times"),
					red:  iconsToBools(red_cell, 3, "fa-check", "fa-times"),
				})
			} else if api_field_prefix, ok := stageFields2024[row_name]; ok {
				blue_values := iconsToBools(blue_cell, 3, "fa-check", "fa-times")
				red_values := iconsToBools(red_cell, 3, "fa-check", "fa-times")
				for i, api_field_suffix := range []string{"StageRight", "CenterStage", "StageLeft"} {
					breakdown["blue"][api_field_prefix+api_field_suffix] = blue_values[i]
					breakdown["red"][api_field_prefix+api_field_suffix] = red_values[i]
				}
			} else {
				breakdown["blue"]["!"+row_name] = blue_text
				breakdown["red"]["!"+row_name] = red_text
			}
		}
	})

	if config.EnabledExtraRps != nil {
		assignBreakdownExtraRps(breakdown, config.EnabledExtraRps, map[string][]bool{
			"red":  extra_info["red"].ExtraRps,
			"blue": extra_info["blue"].ExtraRps,
		}, "tba_extraRp")
	}

	if config.Playoff {
		// set "rp" to 0 since the row is absent
		assignBreakdownAllianceFieldsConst(breakdown, "rp", 0)
	}

	addManualFields2024(breakdown["blue"], scoreInfo.blue, extra_info["blue"], config.Playoff)
	addManualFields2024(breakdown["red"], scoreInfo.red, extra_info["red"], config.Playoff)

	if len(parse_errors) > 0 {
		return nil, fmt.Errorf("Parse error (%d):\n%s", len(parse_errors), strings.Join(parse_errors, "\n"))
	}

	all_json["alliances"] = alliances
	all_json["score_breakdown"] = breakdown

	return all_json, nil
}
