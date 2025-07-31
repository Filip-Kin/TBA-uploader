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

type fmsScoreInfo2025 struct {
	auto   int
	teleop int
	fouls  int
	total  int
	// year-specific:
	baseRP int // win-loss-tie RP only
}

func makeFmsScoreInfo2025() fmsScoreInfo2025 {
	return fmsScoreInfo2025{}
}

type extraMatchAllianceInfo2025 struct {
	extraMatchAllianceInfoCommon
}

func makeExtraMatchAllianceInfo2025() extraMatchAllianceInfo2025 {
	return extraMatchAllianceInfo2025{
		extraMatchAllianceInfoCommon: makeExtraMatchAllianceInfoCommon(),
	}
}

func addManualFields2025(breakdown map[string]interface{}, info fmsScoreInfo2025, extra extraMatchAllianceInfo2025, playoff bool) {
	if _, ok := breakdown["adjustPoints"]; !ok {
		// adjust should be negative when total = 0
		breakdown["adjustPoints"] = info.total - info.auto - info.teleop - info.fouls
	}

	if !playoff {
		if _, ok := breakdown["rp"]; !ok {
			// assume this is a practice match
			// TODO: check for presence of the bonus fields (these are not present in practice matches anyway)
			breakdown["rp"] = info.baseRP
		}
	}
}

// map FMS names (lowercase) to API names of basic integer fields
var simpleIntFields2025 = map[string]string{
	// general
	"adjustments": "adjustPoints",
	// year-specific
	"net algae":       "netAlgaeCount",
	"processor algae": "wallAlgaeCount",
	// "leave points":                    "autoLeavePoints",
	// "speaker note amplified count":    "teleopSpeakerNoteAmplifiedCount",
	// "speaker note amplified points":   "teleopSpeakerNoteAmplifiedPoints",
	// "endgame harmony points":          "endGameHarmonyPoints",
	// "endgame note in trap points":     "endGameNoteInTrapPoints",
	// "endgame on stage points":         "endGameOnStagePoints",
	// "endgame park points":             "endGameParkPoints",
	// "endgame spot light bonus points": "endGameSpotLightBonusPoints",
}

// Map FMS names (lowercase) to API name suffixes of basic integer fields.
// The match phase ("auto" or "teleop") will be prepended to the API names as appropriate.
var simpleIntMatchPhaseFields2025 = map[string]string{
	// "amp note count":                   "AmpNoteCount",
	// "amp note points":                  "AmpNotePoints",
	// "speaker note count":               "SpeakerNoteCount",
	// "speaker note un-amplified count":  "SpeakerNoteCount",
	// "speaker note points":              "SpeakerNotePoints",
	// "speaker note un-amplified points": "SpeakerNotePoints",
}

var totalIntFields2025 = map[string][]string{
	// "autoTotalNotePoints":     {"autoAmpNotePoints", "autoSpeakerNotePoints"},
	// "teleopTotalNotePoints":   {"teleopAmpNotePoints", "teleopSpeakerNotePoints", "teleopSpeakerNoteAmplifiedPoints"},
	// "endGameTotalStagePoints": {"endGameParkPoints", "endGameOnStagePoints", "endGameSpotLightBonusPoints", "endGameHarmonyPoints", "endGameNoteInTrapPoints"},
}

var simpleStringFields2025 = map[string]string{}

var simpleIconFields2025 = map[string]string{
	// "coop button pressed": "coopNotePlayed",
	// "coopertition bonus":  "coopertitionBonusAchieved",
	// "ensemble":            "ensembleBonusAchieved",
	// "melody":              "melodyBonusAchieved",
}

var penaltyFields2025 = map[string]string{
	"G206": "g206Penalty",
	"G410": "g410Penalty",
	"G418": "g418Penalty",
	"G428": "g428Penalty",
}

var skipRows2025 = map[string]bool{
	"autonomous reef": true,
	"teleop reef":     true,
}

var DEFAULT_BREAKDOWN_VALUES_2025 = map[string]any{}

// year-specific:

var reefRowFields2025 = map[string]string{
	"low branch":    "botRow",
	"middle branch": "midRow",
	"high branch":   "topRow",
}

type scoreThresholds2025 struct {
	EnsembleBonusOnStageRobotsThreshold int `json:"ensembleBonusOnStageRobotsThreshold"`
	EnsembleBonusStagePointsThreshold   int `json:"ensembleBonusStagePointsThreshold"`
	MelodyBonusThresholdCoop            int `json:"melodyBonusThresholdCoop"`
	MelodyBonusThresholdNonCoop         int `json:"melodyBonusThresholdNonCoop"`
}

func makeDefaultScoreThresholds2025() scoreThresholds2025 {
	return scoreThresholds2025{
		EnsembleBonusOnStageRobotsThreshold: 2,
		EnsembleBonusStagePointsThreshold:   10,
		MelodyBonusThresholdCoop:            15,
		MelodyBonusThresholdNonCoop:         18,
	}
}

type reefDict2025 map[string]any

func assignReefRow(breakdown map[string]any, reef_field string, reef_row_field string, cell *goquery.Selection) {
	reef, reef_exists := breakdown[reef_field].(reefDict2025)
	if !reef_exists {
		reef = make(reefDict2025)
		breakdown[reef_field] = reef
	}
	reefRow := make(map[string]bool)
	reefValues := iconsToBools(cell, 12, "fa-check", "fa-circle-small")
	count := 0
	for i, val := range reefValues {
		reefRow["node"+string(rune('A'+i))] = val
		if val {
			count++
		}
	}
	reef[reef_row_field] = reefRow
	reef["tba_"+reef_row_field+"Count"] = count
}

func parseHTMLtoJSON2025(filename string, config FMSParseConfig) (map[string]interface{}, error) {
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

	extra_info := make(map[string]extraMatchAllianceInfo2025)
	extra_info["blue"] = makeExtraMatchAllianceInfo2025()
	extra_info["red"] = makeExtraMatchAllianceInfo2025()
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
		blue fmsScoreInfo2025
		red  fmsScoreInfo2025
	}{
		makeFmsScoreInfo2025(),
		makeFmsScoreInfo2025(),
	}

	thresholds := makeDefaultScoreThresholds2025()
	// TODO: read thresholds from request
	err = assignBreakdownFieldsFromJsonStruct[scoreThresholds2025](breakdown, thresholds)
	if err != nil {
		panic(fmt.Sprintf("could not assign thresholds: %v", err))
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

			// Handle each data row
			if api_field, ok := simpleStringFields2025[row_name]; ok {
				assignBreakdownAllianceFields(breakdown, api_field, identity_fn[string], breakdownAllianceFields[string]{
					blue: blue_text,
					red:  red_text,
				})
			} else if api_field, ok := simpleIntFields2025[row_name]; ok {
				assignBreakdownAllianceFields(breakdown, api_field, identity_fn[int], breakdownAllianceFields[int]{
					blue: checkParseInt(blue_text, "blue "+api_field),
					red:  checkParseInt(red_text, "red "+api_field),
				})
			} else if api_field_suffix, ok := simpleIntMatchPhaseFields2025[row_name]; ok {
				validateMatchPhase(match_phase)
				api_field := match_phase + api_field_suffix
				assignBreakdownAllianceFields(breakdown, api_field, identity_fn[int], breakdownAllianceFields[int]{
					blue: checkParseInt(blue_text, "blue "+api_field),
					red:  checkParseInt(red_text, "red "+api_field),
				})
			} else if api_field, ok := simpleIconFields2025[row_name]; ok {
				success_icon := "fa-check"
				if blue_cell.AddSelection(red_cell).Find("i.fa-handshake").Length() >= 1 {
					success_icon = "fa-handshake"
				}
				assignBreakdownAllianceFields[bool](breakdown, api_field, identity_fn[bool], breakdownAllianceFields[bool]{
					blue: iconToBool(blue_cell.Find("i"), success_icon, "fa-times"),
					red:  iconToBool(red_cell.Find("i"), success_icon, "fa-times"),
				})
			} else if _, ok := skipRows2025[row_name]; ok {
				// skip
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
				if blue_score == red_score {
					scoreInfo.blue.baseRP = 1
					scoreInfo.red.baseRP = 1
				} else if blue_score > red_score {
					scoreInfo.blue.baseRP = 2
					scoreInfo.red.baseRP = 0
				} else {
					scoreInfo.blue.baseRP = 0
					scoreInfo.red.baseRP = 2
				}
			} else if row_name == "ranking points" {
				for _, alliance := range []string{"blue", "red"} {
					cell := blue_cell
					if alliance == "red" {
						cell = red_cell
					}
					rp, err := countRankingPoints(cell)
					if err != nil {
						panic(fmt.Errorf("%s ranking points: %s", alliance, err))
					}

					breakdown[alliance]["rp"] = rp

					if cell.Find("div.col-md-3").Length() >= 4 {
						// RPs are icons
						breakdown[alliance]["melodyBonusAchieved"] = cell.Find("i.fa-music").Length() >= 1
						breakdown[alliance]["ensembleBonusAchieved"] = cell.Find("i.fa-users").Length() >= 1
					}
				}
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
				assignPenaltyFields(breakdown, penaltyFields2025, breakdownAllianceFields[*goquery.Selection]{
					blue: blue_cell,
					red:  red_cell,
				})

				// begin year-specific
				// } else if row_name == "leave" {
				// 	assignBreakdownRobotFields(breakdown, "autoLineRobot", boolToYesNo, breakdownRobotFields[bool]{
				// 		blue: iconsToBools(blue_cell, 3, "fa-check", "fa-times"),
				// 		red:  iconsToBools(red_cell, 3, "fa-check", "fa-times"),
				// 	})
			} else if reef_row_field, ok := reefRowFields2025[row_name]; ok {
				validateMatchPhase(match_phase)
				reef_field := match_phase + "Reef"
				assignReefRow(breakdown["red"], reef_field, reef_row_field, red_cell)
				assignReefRow(breakdown["blue"], reef_field, reef_row_field, blue_cell)
				// blue_values := iconsToBools(blue_cell, 3, "fa-check", "fa-times")
				// red_values := iconsToBools(red_cell, 3, "fa-check", "fa-times")
				// for i, api_field_suffix := range []string{"StageRight", "CenterStage", "StageLeft"} {
				// breakdown["blue"][api_field_prefix+api_field_suffix] = blue_values[i]
				// breakdown["red"][api_field_prefix+api_field_suffix] = red_values[i]
				// }
			} else {
				breakdown["blue"]["!"+row_name] = blue_text
				breakdown["red"]["!"+row_name] = red_text
			}
		}
	})

	for total_field, component_fields := range totalIntFields2025 {
		err := assignTotalField(breakdown, total_field, component_fields)
		if err != nil {
			fmt.Printf("Parse error in %s: assignTotalField: %v\n", filename, err)
			parse_errors = append(parse_errors, fmt.Sprintf("assignTotalField: %v", err))
		}
	}

	// fields determined by coopertition:
	for _, alliance := range []string{"blue", "red"} {
		if coop_button_field, ok := breakdown[alliance]["coopNotePlayed"]; ok {
			if coop_button, ok := coop_button_field.(bool); ok {
				breakdown[alliance]["coopertitionCriteriaMet"] = coop_button && !config.Playoff
			}
		}

		if coop_achieved_field, ok := breakdown[alliance]["coopertitionBonusAchieved"]; ok {
			if coop_achieved, ok := coop_achieved_field.(bool); ok {
				if coop_achieved {
					breakdown[alliance]["melodyBonusThreshold"] = thresholds.MelodyBonusThresholdCoop
				} else {
					breakdown[alliance]["melodyBonusThreshold"] = thresholds.MelodyBonusThresholdNonCoop
				}
			}
		}
	}

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

	addManualFields2025(breakdown["blue"], scoreInfo.blue, extra_info["blue"], config.Playoff)
	addManualFields2025(breakdown["red"], scoreInfo.red, extra_info["red"], config.Playoff)

	if len(parse_errors) > 0 {
		return nil, fmt.Errorf("Parse error (%d):\n%s", len(parse_errors), strings.Join(parse_errors, "\n"))
	}

	all_json["alliances"] = alliances
	all_json["score_breakdown"] = breakdown

	return all_json, nil
}
