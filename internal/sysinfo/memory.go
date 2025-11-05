package sysinfo

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ReadMemoryKB reads the total system memory from the specified meminfo path and returns it in kilobytes.
func ReadMemoryKB(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return 0, fmt.Errorf("invalid MemTotal line: %s", line)
			}

			memKB, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse MemTotal value: %w", err)
			}

			if memKB == 0 {
				return 0, fmt.Errorf("MemTotal is zero")
			}

			return memKB, nil
		}
	}

	return 0, fmt.Errorf("MemTotal not found in %s", path)
}
