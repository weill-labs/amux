package keys

import (
	"encoding/hex"
	"fmt"
)

type Chunk struct {
	Data       []byte
	PaceBefore bool
}

func ParseKeyArgs(args []string) (hexMode bool, keys []string) {
	for _, arg := range args {
		if arg == "--hex" {
			hexMode = true
		} else {
			keys = append(keys, arg)
		}
	}
	return hexMode, keys
}

func EncodeChunks(hexMode bool, keys []string) ([]Chunk, error) {
	var chunks []Chunk
	if hexMode {
		for _, hexStr := range keys {
			b, err := hex.DecodeString(hexStr)
			if err != nil {
				return nil, fmt.Errorf("invalid hex: %s", hexStr)
			}
			chunks = append(chunks, Chunk{Data: b})
		}
		return chunks, nil
	}

	for _, key := range keys {
		chunks = append(chunks, Chunk{
			Data:       ParseKey(key),
			PaceBefore: PacedKeyToken(key),
		})
	}
	return chunks, nil
}

func PacedKeyToken(key string) bool {
	if key == "Enter" {
		return true
	}
	if len(key) == 3 && (key[0] == 'C' || key[0] == 'c') && key[1] == '-' {
		return true
	}
	return false
}

func ParseKey(key string) []byte {
	if b, ok := specialKeys[key]; ok {
		return b
	}

	if len(key) == 3 && (key[0] == 'C' || key[0] == 'c') && key[1] == '-' {
		ch := key[2]
		if ch >= 'a' && ch <= 'z' {
			return []byte{ch - 'a' + 1}
		}
		if ch >= 'A' && ch <= 'Z' {
			return []byte{ch - 'A' + 1}
		}
	}

	if len(key) == 3 && (key[0] == 'M' || key[0] == 'm') && key[1] == '-' {
		return []byte{0x1b, key[2]}
	}

	return []byte(key)
}

var specialKeys = map[string][]byte{
	"Enter":    {'\r'},
	"Tab":      {'\t'},
	"Escape":   {0x1b},
	"Space":    {' '},
	"BSpace":   {0x7f},
	"Up":       {0x1b, '[', 'A'},
	"Down":     {0x1b, '[', 'B'},
	"Right":    {0x1b, '[', 'C'},
	"Left":     {0x1b, '[', 'D'},
	"Home":     {0x1b, '[', 'H'},
	"End":      {0x1b, '[', 'F'},
	"PageUp":   {0x1b, '[', '5', '~'},
	"PageDown": {0x1b, '[', '6', '~'},
	"Delete":   {0x1b, '[', '3', '~'},
	"Insert":   {0x1b, '[', '2', '~'},
}
