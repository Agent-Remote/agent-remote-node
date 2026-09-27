package skillmanager

// CaptureOptions contains snapshot-authorized exclusions and adapter-verified runtime links.
type CaptureOptions struct {
	DirectoryBytes      int64             `json:"directory_bytes"`
	Entries             int               `json:"entries"`
	SystemPaths         []string          `json:"system_paths"`
	RuntimeDependencies map[string]string `json:"runtime_dependencies"`
}

// DefaultCaptureOptions returns the independent runtime-state limits, without a file-size cap.
func DefaultCaptureOptions() CaptureOptions {
	return CaptureOptions{DirectoryBytes: 10 << 30, Entries: 100_000}
}
