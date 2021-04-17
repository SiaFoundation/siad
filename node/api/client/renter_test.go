package client

import (
	"testing"

	"go.sia.tech/siad/modules"
)

// TestEscapeSiaPath probes the escapeSiaPath function
func TestEscapeSiaPath(t *testing.T) {
	var tests = []struct {
		in  string
		out string
	}{
		{"dollar$sign", "dollar$sign"},
		{"and&sign", "and&sign"},
		{"single`quote", "single%60quote"},
		{"full:colon", "full:colon"},
		{"semi;colon", "semi%3Bcolon"},
		{"hash#tag", "hash%23tag"},
		{"percent%sign", "percent%25sign"},
		{"at@sign", "at@sign"},
		{"less<than", "less%3Cthan"},
		{"greater>than", "greater%3Ethan"},
		{"equal=to", "equal=to"},
		{"question?mark", "question%3Fmark"},
		{"open[bracket", "open%5Bbracket"},
		{"close]bracket", "close%5Dbracket"},
		{"open{bracket", "open%7Bbracket"},
		{"close}bracket", "close%7Dbracket"},
		{"carrot^top", "carrot%5Etop"},
		{"pipe|pipe", "pipe%7Cpipe"},
		{"tilda~tilda", "tilda~tilda"},
		{"plus+sign", "plus+sign"},
		{"minus-sign", "minus-sign"},
		{"under_score", "under_score"},
		{"comma,comma", "comma%2Ccomma"},
		{"apostrophy's", "apostrophy%27s"},
		{`quotation"marks`, `quotation%22marks`},
		{"", ""},
	}
	for _, test := range tests {
		var siaPath modules.SiaPath
		if test.in == "" {
			siaPath = modules.RootSiaPath()
		} else {
			err := siaPath.LoadString(test.in)
			if err != nil {
				t.Fatal(err)
			}
		}
		s := escapeSiaPath(siaPath)
		if s != test.out {
			t.Errorf("test %v; result %v, expected %v", test.in, s, test.out)
		}
	}
}
