package main

import (
	"reflect"
	"testing"
)

func TestParseWorkers(t *testing.T) {
	workers, err := parseWorkers("2, 4,2,16")
	if err != nil {
		t.Fatalf("parseWorkers() error = %v", err)
	}
	if want := []int{2, 4, 16}; !reflect.DeepEqual(workers, want) {
		t.Fatalf("parseWorkers() = %v, want %v", workers, want)
	}
}

func TestParseWorkersRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "0", "2,nope", "-1"} {
		if _, err := parseWorkers(value); err == nil {
			t.Errorf("parseWorkers(%q) succeeded", value)
		}
	}
}