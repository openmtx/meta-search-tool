package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/openmtx/meta-search-tool/internal/cgoapi"
)

// emitDryRun writes the dry-run JSON to w and returns any marshal error. The
// plan itself is resolved by cgoapi.ResolveDryRun; this is the I/O shell.
func emitDryRun(w io.Writer, dry *cgoapi.DryRunOutput) error {
	data, err := json.MarshalIndent(dry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal dry-run: %w", err)
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}
