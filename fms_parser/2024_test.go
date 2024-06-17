package fms_parser

import (
	"testing"
)

func TestParse2024(t *testing.T) {
	testParseMatchDir(t, parseHTMLtoJSON2024, "../tests/data/2024/")
}
