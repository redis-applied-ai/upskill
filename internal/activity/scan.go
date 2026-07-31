package activity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

type scanResult struct {
	Events   []Event
	Warnings []string
}

func scanEvents(path string) (scanResult, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return scanResult{}, nil
	}
	if err != nil {
		return scanResult{}, fmt.Errorf("read activity file %s: %w", path, err)
	}
	defer file.Close()

	result := scanResult{}
	reader := bufio.NewReader(file)
	for lineNumber := 1; ; lineNumber++ {
		line, readErr := reader.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return scanResult{}, fmt.Errorf("scan activity file %s: %w", path, readErr)
		}
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			break
		}

		unterminatedFinal := errors.Is(readErr, io.EOF) && !bytes.HasSuffix(line, []byte{'\n'})
		line = bytes.TrimSpace(line)
		if len(line) != 0 {
			if len(line)+1 > MaxEventBytes {
				result.Warnings = append(result.Warnings, fmt.Sprintf("activity file %s line %d exceeds %d bytes and was skipped", path, lineNumber, MaxEventBytes))
			} else {
				var event Event
				if err := json.Unmarshal(line, &event); err != nil {
					if unterminatedFinal {
						result.Warnings = append(result.Warnings, fmt.Sprintf("activity file %s has an interrupted final line; it was skipped", path))
					} else {
						result.Warnings = append(result.Warnings, fmt.Sprintf("activity file %s line %d is malformed and was skipped", path, lineNumber))
					}
				} else if !validEventType(event.EventType) {
					result.Warnings = append(result.Warnings, fmt.Sprintf("activity file %s line %d has unknown event_type %q and was skipped", path, lineNumber, event.EventType))
				} else if err := ValidateEvent(event); err != nil {
					result.Warnings = append(result.Warnings, fmt.Sprintf("activity file %s line %d is invalid and was skipped: %s", path, lineNumber, err))
				} else {
					result.Events = append(result.Events, event)
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	return result, nil
}
