package godotenv

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

const (
	charComment       = '#'
	prefixSingleQuote = '\''
	prefixDoubleQuote = '"'

	exportPrefix = "export"
)

func parseBytes(src []byte, out map[string]string) error {
	return parseBytesWithComments(src, out, nil)
}

func parseBytesWithComments(src []byte, out map[string]string, comments map[string]string) error {
	src = bytes.Replace(src, []byte("\r\n"), []byte("\n"), -1)
	cutset := src
	for {
		cutset = getStatementStart(cutset)
		if cutset == nil {
			// reached end of file
			break
		}

		key, left, err := locateKeyName(cutset)
		if err != nil {
			return err
		}

		if key == "" {
			return nil
		}

		value, comment, left, err := extractVarValue(left, out)
		if err != nil {
			return err
		}

		out[key] = value
		cutset = left

		if comments != nil && len(comment) > 0 {
			comments[key] = comment
		}
	}

	return nil
}

// getStatementPosition returns position of statement begin.
//
// It skips any comment line or non-whitespace character.
func getStatementStart(src []byte) []byte {
	pos := indexOfNonSpaceChar(src)
	if pos == -1 {
		return nil
	}

	src = src[pos:]
	if src[0] != charComment {
		return src
	}

	// skip comment section
	pos = bytes.IndexFunc(src, isCharFunc('\n'))
	if pos == -1 {
		return nil
	}

	return getStatementStart(src[pos:])
}

// locateKeyName locates and parses key name and returns rest of slice
func locateKeyName(src []byte) (key string, cutset []byte, err error) {
	// trim "export" and space at beginning
	src = bytes.TrimLeftFunc(src, isSpace)
	if bytes.HasPrefix(src, []byte(exportPrefix)) {
		trimmed := bytes.TrimPrefix(src, []byte(exportPrefix))
		if bytes.IndexFunc(trimmed, isSpace) == 0 {
			src = bytes.TrimLeftFunc(trimmed, isSpace)
		}
	}

	// locate key name end and validate it in single loop
	offset := 0
loop:
	for i, char := range src {
		rchar := rune(char)
		if isSpace(rchar) {
			continue
		}

		switch char {
		case '=', ':':
			// library also supports yaml-style value declaration
			key = string(src[0:i])
			offset = i + 1
			break loop
		case '_':
		default:
			// variable name should match [A-Za-z0-9_.]
			if unicode.IsLetter(rchar) || unicode.IsNumber(rchar) || rchar == '.' {
				continue
			}

			return "", nil, fmt.Errorf(
				`unexpected character %q in variable name near %q`,
				string(char), string(src))
		}
	}

	if len(src) == 0 {
		return "", nil, errors.New("zero length string")
	}

	// trim whitespace
	key = strings.TrimRightFunc(key, unicode.IsSpace)
	cutset = bytes.TrimLeftFunc(src[offset:], isSpace)
	return key, cutset, nil
}

// extractVarValue extracts variable value and returns the rest of the slice
func extractVarValue(src []byte, vars map[string]string) (value string, comment string, rest []byte, err error) {
	quote, hasPrefix := hasQuotePrefix(src)
	if !hasPrefix {
		return extractUnquotedValue(src, vars)
	}

	return extractQuotedValue(src, vars, quote)
}

// extractUnquotedValue extracts unquoted variable value and returns the rest of the slice
func extractUnquotedValue(src []byte, vars map[string]string) (value string, comment string, rest []byte, err error) {
	endOfLine := findEndOfLine(src)

	if endOfLine == -1 {
		endOfLine = len(src)

		if endOfLine == 0 {
			return "", "", nil, nil
		}
	}

	line := []rune(string(src[0:endOfLine]))
	endOfVar := findEndOfVar(line)
	trimmed := ""

	if len(line) > 0 && endOfVar == endOfLine && strings.HasPrefix(string(line), "#") {
		comment = strings.TrimSpace(string(line[1:endOfLine]))
	} else {
		trimmed = strings.TrimFunc(string(line[0:endOfVar]), isSpace)
		if endOfLine > endOfVar+1 {
			comment = strings.TrimSpace(string(src[endOfVar+1 : endOfLine]))
		}
	}

	return expandVariables(trimmed, vars), comment, src[endOfLine:], nil
}

// extractQuotedValue extracts quoted variable value and returns the rest of the slice
func extractQuotedValue(src []byte, vars map[string]string, quote byte) (value string, comment string, rest []byte, err error) {
	for i := 1; i < len(src); i++ {
		if char := src[i]; char != quote {
			continue
		}

		if prevChar := src[i-1]; prevChar == '\\' {
			continue
		}

		trimFunc := isCharFunc(rune(quote))
		value = string(bytes.TrimLeftFunc(bytes.TrimRightFunc(src[0:i], trimFunc), trimFunc))
		endOfLine := findEndOfLine(src)

		if endOfLine == -1 {
			endOfLine = len(src)

			if endOfLine == 0 {
				return "", "", nil, nil
			}
		}

		line := []rune(string(src[0:endOfLine]))
		endOfVar := findEndOfVar(line)

		if endOfLine > endOfVar+1 {
			comment = strings.TrimSpace(string(src[endOfVar+1 : endOfLine]))
		}

		if quote == prefixDoubleQuote {
			value = expandVariables(expandEscapes(value), vars)
		}

		return value, comment, src[i+1:], nil
	}

	valEndIndex := findEndOfLine(src)
	if valEndIndex == -1 {
		valEndIndex = len(src)
	}

	return "", "", nil, fmt.Errorf("unterminated quoted value %s", src[:valEndIndex])
}

// findEndOfLine finds the index of the end of the line
func findEndOfLine(src []byte) int {
	endOfLine := bytes.IndexFunc(src, isLineEnd)

	if endOfLine == -1 {
		endOfLine = len(src)
	}

	return endOfLine
}

// findEndOfVar finds the index of the end of the variable
func findEndOfVar(line []rune) int {
	for i := len(line) - 1; i >= 0; i-- {
		if line[i] == charComment && i > 0 {
			if isSpace(line[i-1]) {
				return i
			}
		}
	}
	return len(line)
}

func expandEscapes(str string) string {
	out := escapeRegex.ReplaceAllStringFunc(str, func(match string) string {
		c := strings.TrimPrefix(match, `\`)
		switch c {
		case "n":
			return "\n"
		case "r":
			return "\r"
		default:
			return match
		}
	})
	return unescapeCharsRegex.ReplaceAllString(out, "$1")
}

func indexOfNonSpaceChar(src []byte) int {
	return bytes.IndexFunc(src, func(r rune) bool {
		return !unicode.IsSpace(r)
	})
}

// hasQuotePrefix reports whether charset starts with single or double quote and returns quote character
func hasQuotePrefix(src []byte) (prefix byte, isQuored bool) {
	if len(src) == 0 {
		return 0, false
	}

	switch prefix := src[0]; prefix {
	case prefixDoubleQuote, prefixSingleQuote:
		return prefix, true
	default:
		return 0, false
	}
}

func isCharFunc(char rune) func(rune) bool {
	return func(v rune) bool {
		return v == char
	}
}

// isSpace reports whether the rune is a space character but not line break character
//
// this differs from unicode.IsSpace, which also applies line break as space
func isSpace(r rune) bool {
	switch r {
	case '\t', '\v', '\f', '\r', ' ', 0x85, 0xA0:
		return true
	}
	return false
}

func isLineEnd(r rune) bool {
	if r == '\n' || r == '\r' {
		return true
	}
	return false
}

var (
	escapeRegex        = regexp.MustCompile(`\\.`)
	expandVarRegex     = regexp.MustCompile(`(\\)?(\$)(\()?\{?([A-Z0-9_]+)?\}?`)
	unescapeCharsRegex = regexp.MustCompile(`\\([^$])`)
)

func expandVariables(v string, m map[string]string) string {
	return expandVarRegex.ReplaceAllStringFunc(v, func(s string) string {
		submatch := expandVarRegex.FindStringSubmatch(s)

		if submatch == nil {
			return s
		}
		if submatch[1] == "\\" || submatch[2] == "(" {
			return submatch[0][1:]
		} else if submatch[4] != "" {
			return m[submatch[4]]
		}
		return s
	})
}
