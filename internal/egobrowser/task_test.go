package egobrowser

import "testing"

func TestDecodeCancelRequestPayloadIsStrict(t *testing.T) {
	payload, err := DecodeCancelRequestPayload(map[string]any{
		"binding_id": "binding-1",
		"generation": 2,
		"request_id": "request-1",
		"sequence":   3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if payload.BindingID != "binding-1" || payload.Generation != 2 ||
		payload.RequestID != "request-1" || payload.Sequence != 3 {
		t.Fatalf("unexpected cancellation payload: %#v", payload)
	}

	for name, invalid := range map[string]map[string]any{
		"unknown field": {
			"binding_id": "binding-1", "generation": 2, "request_id": "request-1",
			"sequence": 3, "script": "forbidden",
		},
		"zero sequence": {
			"binding_id": "binding-1", "generation": 2, "request_id": "request-1", "sequence": 0,
		},
		"missing request": {
			"binding_id": "binding-1", "generation": 2, "sequence": 3,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeCancelRequestPayload(invalid); err == nil {
				t.Fatal("accepted invalid cancellation payload")
			}
		})
	}
}
