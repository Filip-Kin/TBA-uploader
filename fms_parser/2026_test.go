package fms_parser

import (
	"testing"
)

// The fixtures are generated to match FMS's own 2026 score-detail view
// (FMS.GameSpecific.S2026/AspNetCoreGeneratedDocument/Views_Shared_Components_ScoreDetail_DefaultS2026),
// and the expected JSON uses the field names TBA publishes for 2026.
//
// Note on the playoff fixture: FMS only renders the bonus progress rows and the
// ranking-point row in qualification, so a playoff report carries no evidence of
// energized/supercharged/traversal and the parser leaves those keys out.
func TestParse2026(t *testing.T) {
	testParseMatchDir(t, parseHTMLtoJSON2026, "../tests/data/2026/", filterTestMatchDeleteFields())
}

// FMS 2026 serves scores as JSON. The fixture is a real capture from 2026mibg
// (BGRC) qualification 1: the match list entry plus GetScoreDetails.
func TestParse2026JSON(t *testing.T) {
	testParseSingleMatch(t, parseJSONtoJSON2026, "../tests/data/2026/bgrc-qm1.fmsjson", "../tests/data/2026/bgrc-qm1.json", filterTestMatchDeleteFields())
}
