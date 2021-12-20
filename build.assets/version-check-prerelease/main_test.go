package main

import (
	"testing"
)

func TestCheck(t *testing.T) {
	tests := []struct {
		desc     string
		tag      string
		releases []string
		err      bool
	}{
		{
			desc: "fail-rc",
			tag:  "v9.0.0-rc.1",
			err:  true,
		},
		{ // this build was published to the deb repos on 2021-10-06
			desc: "fail-debug",
			tag:  "v6.2.14-debug.4",
			err:  true,
		},
		{
			desc: "fail-metadata",
			tag:  "v8.0.7+1a2b3c4d",
			err:  true,
		},
		{
			desc: "pass",
			tag:  "v8.0.1",
			err:  false,
		},
	}
	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			err := check(test.tag)
			if test.err && err == nil {
				t.Fatalf("Expected an error, got nil.")
			}
			if !test.err && err != nil {
				t.Fatalf("Did not expect and error, got: %v", err)
			}
		})
	}

}
