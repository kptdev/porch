package selector

import (
	neturl "net/url"
	"slices"
	"strings"

	kptfilev1 "github.com/kptdev/kpt/api/kptfile/v1"
	pkgerrors "github.com/pkg/errors"
)

const (
	fileQueryKey    = "file"
	partialQueryKey = "partial"
)

var AllFiles = PRRGet{}
var NoFiles = PRRGet{FilePaths: []string{}}
var KptFile = PRRGet{FilePaths: []string{kptfilev1.KptFileName}}
var Partial = PRRUpdate{Partial: true}
var Complete = PRRUpdate{Partial: false}

type PRRGet struct {
	FilePaths []string
}

type PRRUpdate struct {
	Partial bool
}

func (s *PRRGet) IsAllFiles() bool {
	return s.FilePaths == nil
}

func (s *PRRGet) MatchesFilePath(filePath string) bool {
	return s.IsAllFiles() || slices.Contains(s.FilePaths, filePath)
}

func ParsePRRGet(rawName string) (packageRevisionName string, selector PRRGet, err error) {
	packageRevisionName, queryValues, err := parseRawName(rawName)
	if err != nil {
		return "", PRRGet{}, err
	}
	files := queryValues[fileQueryKey]
	return packageRevisionName, PRRGet{
		FilePaths: files,
	}, nil
}

func ParsePRRUpdate(rawName string) (packageRevisionName string, selector PRRUpdate, err error) {
	packageRevisionName, queryValues, err := parseRawName(rawName)
	if err != nil {
		return "", PRRUpdate{}, err
	}
	partial := false
	if partialValues, existPartialKey := queryValues[partialQueryKey]; existPartialKey {
		if len(partialValues) > 1 {
			return "", PRRUpdate{}, pkgerrors.New("multiple partial values found")
		}
		if slices.Contains([]string{"YES", "TRUE", "1", ""}, strings.ToUpper(partialValues[0])) {
			partial = true
		}
	}
	return packageRevisionName, PRRUpdate{
		Partial: partial,
	}, nil
}

func parseRawName(rawName string) (resourceName string, queryValues neturl.Values, err error) {
	url, err := neturl.Parse(rawName)
	if err != nil {
		return rawName, nil, pkgerrors.Wrap(err, "failed to parse raw name")
	}
	resourceName = url.Path
	queryValues, err = neturl.ParseQuery(url.RawQuery)
	if err != nil {
		return resourceName, nil, pkgerrors.Wrap(err, "failed to parse query string")
	}
	return
}
