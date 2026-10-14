// Package jsonnum preserves the server's number API while both Connector and
// server use the same bounded, lexical JSON Schema integer parser.
package jsonnum

import shared "github.com/ktcloud4-SL/labbit-app/internal/jsonnum"

func PositiveInteger(raw []byte) bool      { return shared.PositiveInteger(raw) }
func Int64(raw []byte) (int64, bool, bool) { return shared.Int64(raw) }
