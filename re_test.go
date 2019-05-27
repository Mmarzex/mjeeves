package main

import (
	"regexp"
	"testing"
)


func TestMessageMatch(t *testing.T) {
	remindMessageRe, err := regexp.Compile(`\/remind \d* (day|hour|minute)`)
	if err != nil {
		t.Errorf("REgex is invalid")
	}

	res := remindMessageRe.MatchString("/remind 10 minutes")
	if !res {
		t.Errorf("Did not match")
	}
}
