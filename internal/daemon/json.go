package daemon

import (
	"encoding/json"

	"github.com/yourok/cdx/internal/ipc"
)

func jsonBytes(v any) ([]byte, error) {
	return json.Marshal(v)
}

func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func decodePayload(f *ipc.Frame, v any) error {
	raw, err := json.Marshal(f.Payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}
