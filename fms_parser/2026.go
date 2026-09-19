package fms_parser

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// 2026: REBUILT. Fuel goes into the hub in five teleop windows (a transition
// shift plus four shifts, then endgame), robots climb the tower in auto and
// endgame, and the three bonus RPs are Energized, Supercharged and Traversal.
//
// Field names and the score-detail table layout come from FMS itself
// (FMS.GameSpecific.S2026: ScoreDetailModelAlliance_2026, ScoreDetailModelGoal_2026
// and the DefaultS2026.cshtml score-detail view), and match the 2026 breakdown
// TBA publishes.

type fmsScoreInfo2026 struct {
	auto   int
	teleop int
	fouls  int
	total  int
	// year-specific:
	baseRP int // win-loss-tie RP only
}

func makeFmsScoreInfo2026() fmsScoreInfo2026 {
	return fmsScoreInfo2026{}
}

type extraMatchAllianceInfo2026 struct {
	extraMatchAllianceInfoCommon
}

func makeExtraMatchAllianceInfo2026() extraMatchAllianceInfo2026 {
	return extraMatchAllianceInfo2026{
		extraMatchAllianceInfoCommon: makeExtraMatchAllianceInfoCommon(),
	}
}

// hubScore2026 is the nested "hubScore" object in the breakdown. Counts come
// from the report; points are derived (see deriveHubPoints2026).
type hubScore2026 struct {
	AutoCount        int `json:"autoCount"`
	AutoPoints       int `json:"autoPoints"`
	TransitionCount  int `json:"transitionCount"`
	TransitionPoints int `json:"transitionPoints"`
	Shift1Count      int `json:"shift1Count"`
	Shift1Points     int `json:"shift1Points"`
	Shift2Count      int `json:"shift2Count"`
	Shift2Points     int `json:"shift2Points"`
	Shift3Count      int `json:"shift3Count"`
	Shift3Points     int `json:"shift3Points"`
	Shift4Count      int `json:"shift4Count"`
	Shift4Points     int `json:"shift4Points"`
	EndgameCount     int `json:"endgameCount"`
	EndgamePoints    int `json:"endgamePoints"`
	TeleopCount      int `json:"teleopCount"`
	TeleopPoints     int `json:"teleopPoints"`
	TotalCount       int `json:"totalCount"`
	TotalPoints      int `json:"totalPoints"`
}

func addManualFields2026(breakdown map[string]interface{}, info fmsScoreInfo2026, extra extraMatchAllianceInfo2026, playoff bool) {
	if _, ok := breakdown["adjustPoints"]; !ok {
		// adjust should be negative when total = 0
		breakdown["adjustPoints"] = info.total - info.auto - info.teleop - info.fouls
	}

	if !playoff {
		if _, ok := breakdown["rp"]; !ok {
			// assume this is a practice match
			breakdown["rp"] = info.baseRP
		}
	}
}

// map FMS row names (lowercase) to API names of basic integer fields
var simpleIntFields2026 = map[string]string{
	// general
	"adjustments": "adjustPoints",
	// year-specific:
	"auto tower points":    "autoTowerPoints",
	"endgame tower points": "endGameTowerPoints",
}

// hub fuel counts, one row each
var hubCountFields2026 = map[string]string{
	"auto fuel count":             "autoCount",
	"transition shift fuel count": "transitionCount",
	"shift 1 fuel count":          "shift1Count",
	"shift 2 fuel count":          "shift2Count",
	"shift 3 fuel count":          "shift3Count",
	"shift 4 fuel count":          "shift4Count",
	"endgame fuel count":          "endgameCount",
	"total teleop fuel count":     "teleopCount",
}

var penaltyFields2026 = map[string]string{
	"G206": "g206Penalty",
}

var skipRows2026 = map[string]bool{}

var DEFAULT_BREAKDOWN_VALUES_2026 = map[string]any{}

// Tower levels, and what each is worth. Auto climb only ever reaches Level1
// (RobotAutoClimbType), endgame goes to Level3 (RobotEndGameType). Values are
// only used to cross-check the points rows, never to replace them.
var TOWER_POINTS_2026 = map[string]map[string]int{
	"auto": {
		"None":   0,
		"Level1": 15,
	},
	"endgame": {
		"None":   0,
		"Level1": 10,
		"Level2": 20,
		"Level3": 30,
	},
}

// Fuel is one point per piece, confirmed against a real FMS capture where
// TotalFuelCount always equalled TotalFuelPoints.
const FUEL_POINTS_PER_PIECE_2026 = 1

var leadingIntRe2026 = regexp.MustCompile(`^\s*(-?\d+)`)

// leadingInt2026 reads the number at the start of a string, so "3 Minor" and
// "12 / 240" both give up their first value.
func leadingInt2026(text, desc string) int {
	m := leadingIntRe2026.FindStringSubmatch(text)
	if m == nil {
		panic(fmt.Sprintf("parse int %s failed: no leading number in %q", desc, text))
	}
	n, err := strconv.ParseInt(m[1], 10, 0)
	if err != nil {
		panic(fmt.Sprintf("parse int %s failed: %s", desc, err))
	}
	return int(n)
}

// countLitIcons2026 counts only the icons FMS actually lit up. The 2026 view
// always emits an icon for every bonus and dims the ones that were not earned
// with an inline opacity, so counting every <i> would report a full house
// every time.
func countLitIcons2026(cell *goquery.Selection, selector string) int {
	count := 0
	cell.Find(selector).Each(func(_ int, icon *goquery.Selection) {
		if style, _ := icon.Attr("style"); !strings.Contains(style, "opacity") {
			count++
		}
	})
	return count
}

func getHub2026(breakdown map[string]interface{}) *hubScore2026 {
	return breakdown["hubScore"].(*hubScore2026)
}

// assignHubCount2026 writes one count field on the hub object by its JSON name.
func assignHubCount2026(breakdown map[string]interface{}, field string, value int) error {
	vals := map[string]any{field: value}
	encoded, err := json.Marshal(vals)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, getHub2026(breakdown))
}

// deriveHubPoints2026 fills in the fuel points, which the report never prints.
//
// Auto and teleop fuel totals are exact: the report gives both phase totals and
// both tower totals, and a phase is only ever fuel plus tower. The per-window
// points then follow from the counts at one point per piece.
func deriveHubPoints2026(breakdown map[string]interface{}) {
	hub := getHub2026(breakdown)

	hub.TransitionPoints = hub.TransitionCount * FUEL_POINTS_PER_PIECE_2026
	hub.Shift1Points = hub.Shift1Count * FUEL_POINTS_PER_PIECE_2026
	hub.Shift2Points = hub.Shift2Count * FUEL_POINTS_PER_PIECE_2026
	hub.Shift3Points = hub.Shift3Count * FUEL_POINTS_PER_PIECE_2026
	hub.Shift4Points = hub.Shift4Count * FUEL_POINTS_PER_PIECE_2026
	hub.EndgamePoints = hub.EndgameCount * FUEL_POINTS_PER_PIECE_2026

	auto_total, has_auto := breakdown["totalAutoPoints"].(int)
	auto_tower, has_auto_tower := breakdown["autoTowerPoints"].(int)
	if has_auto && has_auto_tower {
		hub.AutoPoints = auto_total - auto_tower
	} else {
		hub.AutoPoints = hub.AutoCount * FUEL_POINTS_PER_PIECE_2026
	}

	teleop_total, has_teleop := breakdown["totalTeleopPoints"].(int)
	endgame_tower, has_endgame_tower := breakdown["endGameTowerPoints"].(int)
	if has_teleop && has_endgame_tower {
		hub.TeleopPoints = teleop_total - endgame_tower
	} else {
		hub.TeleopPoints = hub.TransitionPoints + hub.Shift1Points + hub.Shift2Points +
			hub.Shift3Points + hub.Shift4Points + hub.EndgamePoints
	}

	hub.TotalPoints = hub.AutoPoints + hub.TeleopPoints
	if hub.TotalCount == 0 {
		// The progress rows are qualification-only; outside quals the total
		// count is the phases added up.
		hub.TotalCount = hub.AutoCount + hub.TeleopCount
	}
}

func parseHTMLtoJSON2026(filename string, config FMSParseConfig) (map[string]interface{}, error) {
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
		blue fmsScoreInfo2026
		red  fmsScoreInfo2026
	}{
		makeFmsScoreInfo2026(),
		makeFmsScoreInfo2026(),
	}

	parse_errors := make([]string, 0)

	checkParseInt := func(s, desc string) int {
		n, err := strconv.ParseInt(s, 10, 0)
		if err != nil {
			panic(fmt.Sprintf("parse int %s failed: %s", desc, err))
		}
		return int(n)
	}

	// year-specific:
	breakdown["blue"]["hubScore"] = &hubScore2026{}
	breakdown["red"]["hubScore"] = &hubScore2026{}

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
			blue_cell := columns.Eq(1)
			red_cell := columns.Eq(2)
			blue_text := strings.TrimSpace(blue_cell.Text())
			red_text := strings.TrimSpace(red_cell.Text())

			// Handle each data row
			if api_field, ok := simpleIntFields2026[row_name]; ok {
				assignBreakdownAllianceFields(breakdown, api_field, identity_fn[int], breakdownAllianceFields[int]{
					blue: checkParseInt(blue_text, "blue "+api_field),
					red:  checkParseInt(red_text, "red "+api_field),
				})
			} else if hub_field, ok := hubCountFields2026[row_name]; ok {
				for alliance, text := range map[string]string{"blue": blue_text, "red": red_text} {
					if err := assignHubCount2026(breakdown[alliance], hub_field, checkParseInt(text, alliance+" "+row_name)); err != nil {
						panic(fmt.Errorf("%s %s: %v", alliance, row_name, err))
					}
				}
			} else if _, ok := skipRows2026[row_name]; ok {
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
				// year-specific: a win is 3 RP in 2026, a tie 1.
				if blue_score == red_score {
					scoreInfo.blue.baseRP = 1
					scoreInfo.red.baseRP = 1
				} else if blue_score > red_score {
					scoreInfo.blue.baseRP = 3
					scoreInfo.red.baseRP = 0
				} else {
					scoreInfo.blue.baseRP = 0
					scoreInfo.red.baseRP = 3
				}
			} else if row_name == "ranking points" {
				for _, alliance := range []string{"blue", "red"} {
					cell := blue_cell
					if alliance == "red" {
						cell = red_cell
					}

					// year-specific: every bonus and every win/tie trophy is
					// drawn, earned or not, so count the lit ones.
					breakdown[alliance]["rp"] = countLitIcons2026(cell, "i.fas, i.fak")
					breakdown[alliance]["energizedAchieved"] = countLitIcons2026(cell, "i.fa-circle") >= 1
					breakdown[alliance]["superchargedAchieved"] = countLitIcons2026(cell, "i.fa-ball-pile") >= 1
					breakdown[alliance]["traversalAchieved"] = countLitIcons2026(cell, "i.fa-chess-rook") >= 1
				}
			} else if row_name == "autonomous points" {
				blue_points := checkParseInt(blue_text, "blue "+row_name)
				red_points := checkParseInt(red_text, "red "+row_name)
				assignBreakdownAllianceFields(breakdown, "totalAutoPoints", identity_fn[int], breakdownAllianceFields[int]{
					blue: blue_points,
					red:  red_points,
				})
				scoreInfo.blue.auto = blue_points
				scoreInfo.red.auto = red_points
			} else if row_name == "teleop points" {
				blue_points := checkParseInt(blue_text, "blue "+row_name)
				red_points := checkParseInt(red_text, "red "+row_name)
				assignBreakdownAllianceFields(breakdown, "totalTeleopPoints", identity_fn[int], breakdownAllianceFields[int]{
					blue: blue_points,
					red:  red_points,
				})
				scoreInfo.blue.teleop = blue_points
				scoreInfo.red.teleop = red_points
			} else if row_name == "foul points" {
				blue_points := checkParseInt(blue_text, "blue "+row_name)
				red_points := checkParseInt(red_text, "red "+row_name)
				assignBreakdownAllianceFields(breakdown, "foulPoints", identity_fn[int], breakdownAllianceFields[int]{
					blue: blue_points,
					red:  red_points,
				})
				scoreInfo.blue.fouls = blue_points
				scoreInfo.red.fouls = red_points
			} else if row_name == "fouls committed" {
				// "3 Minor • 1 Major"
				assignBreakdownAllianceMultipleFields(breakdown, []string{"minorFoulCount", "majorFoulCount"}, func(value, alliance string) int {
					return leadingInt2026(value, alliance+" "+row_name)
				}, breakdownAllianceMultipleFields[string]{
					blue: split_and_strip(blue_text, "•"),
					red:  split_and_strip(red_text, "•"),
				})
			} else if row_name == "penalties" {
				assignPenaltyFields(breakdown, penaltyFields2026, breakdownAllianceFields[*goquery.Selection]{
					blue: blue_cell,
					red:  red_cell,
				})

				// begin year-specific
			} else if row_name == "auto tower" {
				// Auto climb is all-or-nothing per robot (Level1), drawn as a
				// check or a cross.
				values := breakdownRobotFields[bool]{
					blue: iconsToBools(blue_cell, 3, "fa-check", "fa-times"),
					red:  iconsToBools(red_cell, 3, "fa-check", "fa-times"),
				}
				assignBreakdownRobotFields(breakdown, "autoTowerRobot", func(climbed bool) string {
					if climbed {
						return "Level1"
					}
					return "None"
				}, values)
			} else if row_name == "endgame tower" {
				values := breakdownRobotFields[string]{
					blue: split_and_strip(blue_text, "\n"),
					red:  split_and_strip(red_text, "\n"),
				}
				assignBreakdownRobotFields(breakdown, "endGameTowerRobot", identity_fn[string], values)
			} else if row_name == "energized progress" || row_name == "supercharged progress" {
				// "<total fuel> / <threshold>"
				for alliance, text := range map[string]string{"blue": blue_text, "red": red_text} {
					getHub2026(breakdown[alliance]).TotalCount = leadingInt2026(text, alliance+" "+row_name)
				}
			} else if row_name == "traversal progress" {
				// "<tower points> / <threshold>"
				assignBreakdownAllianceFields(breakdown, "totalTowerPoints", identity_fn[int], breakdownAllianceFields[int]{
					blue: leadingInt2026(blue_text, "blue "+row_name),
					red:  leadingInt2026(red_text, "red "+row_name),
				})
			} else {
				breakdown["blue"]["!"+row_name] = blue_text
				breakdown["red"]["!"+row_name] = red_text
			}
		}
	})

	for _, alliance := range []string{"blue", "red"} {
		if _, ok := breakdown[alliance]["totalTowerPoints"]; !ok {
			// Outside qualification the traversal row is absent, but both
			// tower rows are there.
			auto_tower, has_auto := breakdown[alliance]["autoTowerPoints"].(int)
			endgame_tower, has_endgame := breakdown[alliance]["endGameTowerPoints"].(int)
			if has_auto && has_endgame {
				breakdown[alliance]["totalTowerPoints"] = auto_tower + endgame_tower
			}
		}
		deriveHubPoints2026(breakdown[alliance])
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
		// The bonus rows are qualification-only, so outside quals we have
		// nothing to report for them and leave them out rather than claiming
		// they were not achieved.
	}

	addManualFields2026(breakdown["blue"], scoreInfo.blue, extra_info["blue"], config.Playoff)
	addManualFields2026(breakdown["red"], scoreInfo.red, extra_info["red"], config.Playoff)

	if len(parse_errors) > 0 {
		return nil, fmt.Errorf("Parse error (%d):\n%s", len(parse_errors), strings.Join(parse_errors, "\n"))
	}

	all_json["alliances"] = alliances
	all_json["score_breakdown"] = breakdown

	return all_json, nil
}
