package skillexport

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"regexp"
	"time"
)

var grantPattern = regexp.MustCompile(`^[A-Za-z0-9_=-]+\.[0-9a-f]{64}$`)

type exportRequest struct {
	grant    string
	recovery bool
}

func readGrant(parent context.Context, connection io.ReadWriteCloser) (exportRequest, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	data, err := io.ReadAll(io.LimitReader(connection, 8193))
	if err != nil || len(data) > 8192 || ctx.Err() != nil {
		return exportRequest{}, ErrUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return exportRequest{}, ErrUnavailable
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || name != "version" && name != "grant" && name != "recovery_version" || fields[name] != nil {
			return exportRequest{}, ErrUnavailable
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return exportRequest{}, ErrUnavailable
		}
		fields[name] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return exportRequest{}, ErrUnavailable
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil || !bytes.Equal(bytes.TrimSpace(fields["version"]), []byte("1")) {
		return exportRequest{}, ErrUnavailable
	}
	recovery := fields["recovery_version"] != nil
	if recovery && !bytes.Equal(bytes.TrimSpace(fields["recovery_version"]), []byte("1")) {
		return exportRequest{}, ErrUnavailable
	}
	var grant string
	if json.Unmarshal(fields["grant"], &grant) != nil || len(grant) > 4096 || !grantPattern.MatchString(grant) {
		return exportRequest{}, ErrUnavailable
	}
	return exportRequest{grant: grant, recovery: recovery}, nil
}
