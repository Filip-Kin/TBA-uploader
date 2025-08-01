package fms_parser

import (
	"testing"
)

func TestParse2025(t *testing.T) {
	testParseMatchDir(t, parseHTMLtoJSON2025, "../tests/data/2025/", filterTestMatchDeleteFields())
}
