package storage

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Trysac/u409-columnar-sql-engine/interfaces"
)

const maxErrorMessageInputLen = 200

func truncateForError(s string) string {
	if len(s) > maxErrorMessageInputLen {
		return s[:maxErrorMessageInputLen] + "..."
	}
	return s
}

// isNullValue checks if a CSV field represents a NULL value.
func isNullValue(raw string, dt interfaces.DataType) bool {
	trimmed := strings.TrimSpace(raw)
	if strings.EqualFold(trimmed, "null") || trimmed == "\\N" {
		return true
	}
	if trimmed == "" {
		return true
	}
	return false
}

// parseFieldValue parses a non-null string into the target physical type.
func parseFieldValue(raw string, colName string, dt interfaces.DataType) (int64Val int64, float64Val float64, strVal string, boolVal bool, err error) {
	trimmed := strings.TrimSpace(raw)
	switch dt {
	case interfaces.TypeInt64:
		v, parseErr := strconv.ParseInt(trimmed, 10, 64)
		if parseErr != nil {
			return 0, 0, "", false, &interfaces.EngineError{
				Code:    interfaces.ErrTypeMismatch,
				Message: fmt.Sprintf("cannot parse %q as INT64 in column %q: %s", truncateForError(raw), truncateForError(colName), parseErr.Error()),
			}
		}
		return v, 0, "", false, nil

	case interfaces.TypeFloat64:
		v, parseErr := strconv.ParseFloat(trimmed, 64)
		if parseErr != nil {
			return 0, 0, "", false, &interfaces.EngineError{
				Code:    interfaces.ErrTypeMismatch,
				Message: fmt.Sprintf("cannot parse %q as FLOAT64 in column %q: %s", truncateForError(raw), truncateForError(colName), parseErr.Error()),
			}
		}
		return 0, v, "", false, nil

	case interfaces.TypeDecimal:
		v, parseErr := strconv.ParseFloat(trimmed, 64)
		if parseErr != nil {
			return 0, 0, "", false, &interfaces.EngineError{
				Code:    interfaces.ErrTypeMismatch,
				Message: fmt.Sprintf("cannot parse %q as DECIMAL in column %q: %s", truncateForError(raw), truncateForError(colName), parseErr.Error()),
			}
		}
		return 0, v, trimmed, false, nil

	case interfaces.TypeVarChar:
		return 0, 0, raw, false, nil

	case interfaces.TypeDate:
		formats := []string{
			"2006-01-02",
			"2006-01-02 15:04:05",
			time.RFC3339,
			"2006/01/02",
		}
		var parsedTime time.Time
		parsed := false
		for _, f := range formats {
			if t, err := time.Parse(f, trimmed); err == nil {
				parsedTime = t
				parsed = true
				break
			}
		}
		if !parsed {
			return 0, 0, "", false, &interfaces.EngineError{
				Code:    interfaces.ErrTypeMismatch,
				Message: fmt.Sprintf("cannot parse %q as DATE in column %q", truncateForError(raw), truncateForError(colName)),
			}
		}
		return parsedTime.Unix(), 0, trimmed, false, nil

	case interfaces.TypeBool:
		lower := strings.ToLower(trimmed)
		switch lower {
		case "1", "t", "true", "yes", "y":
			return 0, 0, "", true, nil
		case "0", "f", "false", "no", "n":
			return 0, 0, "", false, nil
		default:
			return 0, 0, "", false, &interfaces.EngineError{
				Code:    interfaces.ErrTypeMismatch,
				Message: fmt.Sprintf("cannot parse %q as BOOL in column %q", truncateForError(raw), truncateForError(colName)),
			}
		}

	default:
		return 0, 0, "", false, &interfaces.EngineError{
			Code:    interfaces.ErrTypeMismatch,
			Message: fmt.Sprintf("unsupported data type %d in column %q", dt, truncateForError(colName)),
		}
	}
}
