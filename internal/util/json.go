package util

import (
	"bytes"
	"encoding/json"
)

// Marshal encodes v to JSON with the unified conventions. It preserves field
// names as declared in struct tags (camelCase to match the Python contract) and
// omits empty fields only when explicitly tagged `omitempty`.
func Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// Unmarshal decodes JSON with strict unknown-field rejection, mirroring
// Pydantic's `extra="forbid"` behavior in the Python backend. Use it for our own
// request/response contracts whose field set is fully known.
func Unmarshal(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	return nil
}

// UnmarshalLenient decodes JSON tolerating unknown fields. Use it for external
// third-party responses whose field set is not under our control (e.g. ReMail /
// MailCom Hub). Unlike Unmarshal, it does not reject extra fields.
func UnmarshalLenient(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
