package waa

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// Challenge holds client interpreter input returned by Waa/Create
type Challenge struct {
	MessageID                  string
	InterpreterJavaScript      string
	InterpreterURL             string
	InterpreterHash            string
	Program                    string
	GlobalName                 string
	ClientExperimentsStateBlob string
}

// ParseChallenge parses Waa/Create response, reading plaintext challenge in field 1 when obfuscated challenge in field 2 is empty
func ParseChallenge(raw []byte) (Challenge, error) {
	var outer []json.RawMessage
	if err := json.Unmarshal(raw, &outer); err != nil {
		return Challenge{}, fmt.Errorf("parse Waa/Create response: %w", err)
	}
	var encoded string
	if len(outer) > 1 {
		_ = json.Unmarshal(outer[1], &encoded)
	}
	var fields []json.RawMessage
	if encoded != "" {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return Challenge{}, fmt.Errorf("decode Waa/Create challenge: %w", err)
		}
		for index := range decoded {
			decoded[index] += 97
		}
		if err := json.Unmarshal(decoded, &fields); err != nil {
			return Challenge{}, fmt.Errorf("parse Waa/Create challenge: %w", err)
		}
	} else if len(outer) == 0 || json.Unmarshal(outer[0], &fields) != nil || len(fields) == 0 {
		return Challenge{}, fmt.Errorf("Waa/Create response missing challenge")
	}
	if len(fields) < 8 {
		return Challenge{}, fmt.Errorf("Waa/Create challenge fields insufficient: %d", len(fields))
	}
	challenge := Challenge{
		MessageID:                  decodeString(fields[0]),
		InterpreterJavaScript:      decodeFirstString(fields[1]),
		InterpreterHash:            decodeString(fields[3]),
		Program:                    decodeString(fields[4]),
		GlobalName:                 decodeString(fields[5]),
		ClientExperimentsStateBlob: decodeString(fields[7]),
	}
	if interpreterPath := decodeFirstString(fields[2]); interpreterPath != "" {
		challenge.InterpreterURL = "https:" + strings.TrimPrefix(interpreterPath, "https:")
	}
	if challenge.InterpreterHash == "" || challenge.Program == "" || challenge.GlobalName == "" {
		return Challenge{}, fmt.Errorf("Waa/Create challenge missing interpreter identifier or program")
	}
	return challenge, nil
}

func decodeString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func decodeFirstString(raw json.RawMessage) string {
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return ""
	}
	for _, rawValue := range values {
		if value := decodeString(rawValue); value != "" {
			return value
		}
	}
	return ""
}

// InterpreterHash returns SHA-256 base64url digest of interpreter source
func InterpreterHash(source []byte) string {
	digest := sha256.Sum256(source)
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
