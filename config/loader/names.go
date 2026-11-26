// Copyright 2026 The kpt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package loader

import (
	"strings"
	"unicode"
)

// splitWords splits a Go field name into words. A run of capitals is one word,
// so MaxGRPCMessageSize is Max, GRPC, Message, Size, and DB is one word.
func splitWords(name string) []string {
	rs := []rune(name)
	if len(rs) == 0 {
		return nil
	}
	words := make([]string, 0, len(rs)/2)
	start := 0
	for i := 1; i < len(rs); i++ {
		if wordBoundary(rs, i) {
			words = append(words, string(rs[start:i]))
			start = i
		}
	}
	return append(words, string(rs[start:]))
}

func wordBoundary(rs []rune, i int) bool {
	prev, curr := rs[i-1], rs[i]
	if unicode.IsLower(prev) && unicode.IsUpper(curr) {
		return true
	}
	if unicode.IsLetter(prev) && unicode.IsDigit(curr) {
		return true
	}
	if unicode.IsDigit(prev) && unicode.IsLetter(curr) {
		return true
	}
	return i+1 < len(rs) && unicode.IsUpper(prev) && unicode.IsUpper(curr) && unicode.IsLower(rs[i+1])
}

func camelCase(words []string) string {
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(strings.ToLower(words[0]))
	for _, word := range words[1:] {
		b.WriteString(camelWord(word))
	}
	return b.String()
}

func camelWord(word string) string {
	if len(word) > 1 && isUpper(word) {
		return strings.ToUpper(word)
	}
	rs := []rune(strings.ToLower(word))
	rs[0] = unicode.ToUpper(rs[0])
	return string(rs)
}

func isUpper(word string) bool {
	for _, r := range word {
		if unicode.IsLetter(r) && !unicode.IsUpper(r) {
			return false
		}
	}
	return true
}

func kebabCase(words []string) string {
	return joinCase(words, "-", strings.ToLower)
}

func envCase(words []string) string {
	return joinCase(words, "_", strings.ToUpper)
}

func joinCase(words []string, sep string, conv func(string) string) string {
	parts := make([]string, len(words))
	for i, word := range words {
		parts[i] = conv(word)
	}
	return strings.Join(parts, sep)
}

// tagName returns the name from a struct tag and whether the field is skipped.
// An empty tag value is not a name. "-" skips the field.
func tagName(value string) (name string, skip bool) {
	name, _, _ = strings.Cut(value, ",")
	if name == "-" {
		return "", true
	}
	return name, false
}
