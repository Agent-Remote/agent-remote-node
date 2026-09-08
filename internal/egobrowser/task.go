package egobrowser

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// CancelRequestPayload identifies one accepted browser request without
// carrying script, output, relay, or key material.
type CancelRequestPayload struct {
	BindingID  string `json:"binding_id"`
	Generation uint64 `json:"generation"`
	RequestID  string `json:"request_id"`
	Sequence   uint64 `json:"sequence"`
}

// DecodeCancelRequestPayload strictly validates a control-plane cancellation task.
func DecodeCancelRequestPayload(payload map[string]any) (CancelRequestPayload, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return CancelRequestPayload{}, err
	}
	var decoded CancelRequestPayload
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return CancelRequestPayload{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return CancelRequestPayload{}, errors.New("cancellation task payload must contain one JSON object")
	}
	if !validOpaqueText(decoded.BindingID, 128) || decoded.Generation == 0 ||
		!validOpaqueText(decoded.RequestID, 128) || decoded.Sequence == 0 {
		return CancelRequestPayload{}, fmt.Errorf("%w: cancellation task identity", ErrProtocol)
	}
	return decoded, nil
}
