package selector

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testPRName     = "repo.package.v1"
	testKptFile    = "Kptfile"
	testReadmeFile = "README.md"
)

func TestParseGetPackageRevisionResourcesUrl(t *testing.T) {
	testCases := map[string]struct {
		nameWithQuery               string
		expectedPackageRevisionName string
		expectedSelector            PRRGet
		expectedErr                 string
	}{
		"no name": {
			nameWithQuery:               "",
			expectedPackageRevisionName: "",
			expectedSelector: PRRGet{
				FilePaths: nil,
			},
			expectedErr: "",
		},
		"no selector": {
			nameWithQuery:               testPRName,
			expectedPackageRevisionName: testPRName,
			expectedSelector: PRRGet{
				FilePaths: nil,
			},
			expectedErr: "",
		},
		"empty selector": {
			nameWithQuery:               fmt.Sprintf("%s?", testPRName),
			expectedPackageRevisionName: testPRName,
			expectedSelector: PRRGet{
				FilePaths: nil,
			},
			expectedErr: "",
		},
		"file selector": {
			nameWithQuery:               fmt.Sprintf("%s?file=%s", testPRName, testKptFile),
			expectedPackageRevisionName: testPRName,
			expectedSelector: PRRGet{
				FilePaths: []string{testKptFile},
			},
			expectedErr: "",
		},
		"partial selector": {
			nameWithQuery:               fmt.Sprintf("%s?partial=false", testPRName),
			expectedPackageRevisionName: testPRName,
			expectedSelector: PRRGet{
				FilePaths: nil,
			},
			expectedErr: "",
		},
	}

	for tn, tc := range testCases {
		t.Run(tn, func(t *testing.T) {
			// when
			packageRevisionName, selector, err := ParsePRRGet(tc.nameWithQuery)

			// then
			assert.Equal(t, tc.expectedPackageRevisionName, packageRevisionName, "expected package revision name does not match")
			assert.Equal(t, tc.expectedSelector, selector, "expected selector does not match")
			if tc.expectedErr == "" {
				require.NoError(t, err, "expected no error")
			} else {
				require.Equal(t, tc.expectedErr, err.Error(), "expected error does not match")
			}
		})
	}
}

func TestParseUpdatePackageRevisionResourcesUrl(t *testing.T) {
	testCases := map[string]struct {
		nameWithQuery               string
		expectedPackageRevisionName string
		expectedSelector            PRRUpdate
		expectedErr                 string
	}{
		"multiple selector": {
			nameWithQuery:               fmt.Sprintf("%s?file=%s&file=%s&partial=true", testPRName, testKptFile, testReadmeFile),
			expectedPackageRevisionName: testPRName,
			expectedSelector: PRRUpdate{
				Partial: true,
			},
			expectedErr: "",
		},
		"separator not exist in item": {
			nameWithQuery:               fmt.Sprintf("%s?partial", testPRName),
			expectedPackageRevisionName: testPRName,
			expectedSelector: PRRUpdate{
				Partial: true,
			},
			expectedErr: "",
		},
		"duplicate key in selector error": {
			nameWithQuery:               fmt.Sprintf("%s?partial=true&partial=false", testPRName),
			expectedPackageRevisionName: "",
			expectedSelector:            PRRUpdate{},
			expectedErr:                 "multiple partial values found",
		},
		"parse query string error": {
			nameWithQuery:               "repo.package.v1?file=Kptfile%partial=false",
			expectedPackageRevisionName: "",
			expectedSelector:            PRRUpdate{},
			expectedErr:                 "failed to parse query string: invalid URL escape \"%pa\"",
		},
		"parse url error": {
			nameWithQuery:               "repo.package.v1%partial",
			expectedPackageRevisionName: "",
			expectedSelector:            PRRUpdate{},
			expectedErr:                 "failed to parse raw name: parse \"repo.package.v1%partial\": invalid URL escape \"%pa\"",
		},
	}

	for tn, tc := range testCases {
		t.Run(tn, func(t *testing.T) {
			// when
			packageRevisionName, selector, err := ParsePRRUpdate(tc.nameWithQuery)

			// then
			assert.Equal(t, tc.expectedPackageRevisionName, packageRevisionName, "expected package revision name does not match")
			assert.Equal(t, tc.expectedSelector, selector, "expected selector does not match")
			if tc.expectedErr == "" {
				require.NoError(t, err, "expected no error")
			} else {
				require.Equal(t, tc.expectedErr, err.Error(), "expected error does not match")
			}
		})
	}
}

func TestPRRGetIsMatchFilePath(t *testing.T) {
	testCases := map[string]struct {
		selector      PRRGet
		match         string
		expectedMatch bool
		expectedIsAll bool
	}{
		"all": {
			selector:      PRRGet{FilePaths: nil},
			match:         testKptFile,
			expectedMatch: true,
			expectedIsAll: true,
		},
		"match": {
			selector:      PRRGet{FilePaths: []string{testKptFile, testReadmeFile}},
			match:         testKptFile,
			expectedMatch: true,
			expectedIsAll: false,
		},
		"mismatch": {
			selector:      PRRGet{FilePaths: []string{testReadmeFile}},
			match:         testKptFile,
			expectedMatch: false,
			expectedIsAll: false,
		},
	}

	for tn, tc := range testCases {
		t.Run(tn, func(t *testing.T) {
			// when
			match := tc.selector.MatchesFilePath(tc.match)
			isAll := tc.selector.IsAllFiles()

			// then
			assert.Equal(t, tc.expectedMatch, match, "expected match")
			assert.Equal(t, tc.expectedIsAll, isAll, "expected is all")
		})
	}
}
