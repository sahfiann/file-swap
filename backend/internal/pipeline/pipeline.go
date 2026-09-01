// Package pipeline implements the internal quality gate used by every job.
// It deliberately exposes no UI concepts: callers receive only the selected,
// validated output and its file-size measurements.
package pipeline

import (
	"fmt"
	"os"
)

type Measurement struct {
	InputBytes  int64
	OutputBytes int64
}

// Execute runs the smart processing lifecycle internally:
// analyze inputs, form a valid plan, process, validate the output, measure it,
// then select that validated output as the job result.
func Execute(inputs []string, profile string, process func() (string, error)) (string, Measurement, error) {
	if len(inputs) == 0 || profile == "" {
		return "", Measurement{}, fmt.Errorf("could not create a processing plan")
	}

	var inputBytes int64
	for _, input := range inputs {
		info, err := os.Stat(input)
		if err != nil {
			return "", Measurement{}, fmt.Errorf("analyze input: %w", err)
		}
		if info.Size() == 0 {
			return "", Measurement{}, fmt.Errorf("input file is empty")
		}
		inputBytes += info.Size()
	}

	output, err := process()
	if err != nil {
		return "", Measurement{}, err
	}
	info, err := os.Stat(output)
	if err != nil {
		return "", Measurement{}, fmt.Errorf("validate result: %w", err)
	}
	if info.Size() == 0 {
		return "", Measurement{}, fmt.Errorf("validate result: processing produced an empty file")
	}

	return output, Measurement{InputBytes: inputBytes, OutputBytes: info.Size()}, nil
}
