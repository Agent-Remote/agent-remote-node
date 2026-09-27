package skillmanager

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"unicode/utf8"
)

func validateJSONObject(data []byte, allowed ...string) error {
	if !utf8.Valid(data) || !json.Valid(data) || !validSurrogateEscapes(data) {
		return errors.New("skill manifest requires valid UTF-8 JSON")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return errors.New("skill manifest requires an object")
	}
	for field, value := range fields {
		known := false
		for _, candidate := range allowed {
			if field == candidate {
				known = true
				break
			}
		}
		if !known || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("skill manifest has an unknown or null field")
		}
	}
	return nil
}

// Go replaces unpaired UTF-16 surrogates; reject them before identity validation instead.
func validSurrogateEscapes(data []byte) bool {
	inString := false
	for index := 0; index < len(data); index++ {
		if data[index] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[index] != '\\' {
			continue
		}
		index++
		if index >= len(data) {
			return false
		}
		if data[index] != 'u' {
			continue
		}
		if index+4 >= len(data) {
			return false
		}
		unit, err := strconv.ParseUint(string(data[index+1:index+5]), 16, 16)
		if err != nil || unit >= 0xdc00 && unit <= 0xdfff {
			return false
		}
		index += 4
		if unit < 0xd800 || unit > 0xdbff {
			continue
		}
		if index+6 >= len(data) || data[index+1] != '\\' || data[index+2] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(string(data[index+3:index+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		index += 6
	}
	return true
}
