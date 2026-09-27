package skillmanager

import (
	"encoding/json"
	"errors"
	"io"
)

const maxManifestBytes = 64 << 20
const maxBaselineBytes = 2*maxManifestBytes + 1024

// Each entry is bounded by the manifest contract; avoid allocating a second full directory tree
// merely to serialize a large permission baseline during privileged preparation.
func writePermissionBaseline(writer io.Writer, baseline PermissionBaseline) error {
	for index, manifest := range []Manifest{baseline.Source, baseline.Materialized} {
		prefix := `{"version":1,"source":`
		if index == 1 {
			prefix = `,"materialized":`
		}
		if err := writeComplete(writer, []byte(prefix)); err != nil {
			return err
		}
		if err := writeManifest(writer, manifest); err != nil {
			return err
		}
	}
	return writeComplete(writer, []byte(`}`))
}

func writeManifest(writer io.Writer, manifest Manifest) error {
	writer = &metadataLimitWriter{writer: writer, remaining: maxManifestBytes}
	if err := writeComplete(writer, []byte(`{"version":1,"entries":[`)); err != nil {
		return err
	}
	for index, entry := range manifest.Entries {
		if index > 0 {
			if err := writeComplete(writer, []byte(",")); err != nil {
				return err
			}
		}
		encoded, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if err := writeComplete(writer, encoded); err != nil {
			return err
		}
	}
	return writeComplete(writer, []byte(`]}`))
}

type metadataLimitWriter struct {
	writer    io.Writer
	remaining int
}

func (w *metadataLimitWriter) Write(data []byte) (int, error) {
	if len(data) > w.remaining {
		return 0, errors.New("quota_exceeded: skill manifest metadata limit")
	}
	count, err := w.writer.Write(data)
	w.remaining -= count
	return count, err
}

func writeComplete(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		count, err := writer.Write(data)
		if err != nil {
			return err
		}
		if count <= 0 || count > len(data) {
			return io.ErrShortWrite
		}
		data = data[count:]
	}
	return nil
}
