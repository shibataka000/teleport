/*
We don't want to publish semver pre-release versions or versions with build
metadata to package repositories, as these are typically internal builds for
test purposes. -- 2021-12 Walt
*/
package main

import (
	"flag"
	"log"
	"strings"

	"github.com/gravitational/trace"
)

func main() {
	tag, err := parseFlags()
	if err != nil {
		log.Fatalf("Failed to parse flags; %v.", err)
	}

	if err := check(tag); err != nil {
		log.Fatalf("Check failed: %v.", err)
	}
}

func parseFlags() (string, error) {
	tag := flag.String("tag", "", "tag to validate")
	flag.Parse()

	if *tag == "" {
		return "", trace.BadParameter("tag missing")
	}
	return *tag, nil
}

func check(tag string) error {
	if strings.Contains(tag, "-") { // https://semver.org/#spec-item-9
		return trace.BadParameter("version is pre-release: %v", tag)
	}
	if strings.Contains(tag, "+") { // https://semver.org/#spec-item-10
		return trace.BadParameter("version contains build metadata: %v", tag)
	}
	return nil
}
