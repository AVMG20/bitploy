package dbview

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// SQLite values go through JSON like this, in both directions:
//
//	NULL                  null
//	INTEGER, |n| <= 2^53  a number
//	INTEGER, bigger       {"int": "9007199254740993"}
//	REAL                  a number; NaN and the infinities {"real": "NaN"}
//	TEXT                  a string; over 64 KiB in a result:
//	                      {"text": "<first 64 KiB>", "size": n, "truncated": true}
//	BLOB                  {"blob": "<base64 of the first 4 KiB>", "size": n, "truncated": bool}
//
// A value that was cut short must never be sent back as a new value.

const maxSafeInt = 1 << 53

// encodeValue turns what the driver read into its JSON form. The second result
// is about how many bytes the JSON takes, to keep a response within bounds.
func encodeValue(v any) (any, int) {
	switch x := v.(type) {
	case nil:
		return nil, 4
	case int64:
		if x > maxSafeInt || x < -maxSafeInt {
			return map[string]any{"int": strconv.FormatInt(x, 10)}, 40
		}
		return x, 12
	case bool: // a driver that reads BOOLEAN columns as bool
		if x {
			return int64(1), 1
		}
		return int64(0), 1
	case float64:
		switch {
		case math.IsNaN(x):
			return map[string]any{"real": "NaN"}, 20
		case math.IsInf(x, 1):
			return map[string]any{"real": "Inf"}, 20
		case math.IsInf(x, -1):
			return map[string]any{"real": "-Inf"}, 20
		}
		return x, 24
	case string:
		if len(x) > MaxTextBytes {
			// Do not end in the middle of a character.
			end := MaxTextBytes
			for end > MaxTextBytes-utf8.UTFMax && !utf8.RuneStart(x[end]) {
				end--
			}
			cut := x[:end]
			return map[string]any{"text": cut, "size": len(x), "truncated": true}, jsonSize(cut) + 50
		}
		return x, jsonSize(x) + 2
	case []byte:
		head := x
		if len(head) > MaxBlobPreview {
			head = head[:MaxBlobPreview]
		}
		enc := base64.StdEncoding.EncodeToString(head)
		return map[string]any{"blob": enc, "size": len(x), "truncated": len(x) > MaxBlobPreview}, len(enc) + 60
	case time.Time:
		// Only for a value the driver converted behind our back (a RETURNING
		// column that is declared as a date). Reads of tables avoid this.
		return x.Format("2006-01-02 15:04:05.999999999"), 32
	}
	return fmt.Sprint(v), 32
}

// valueShape is the object form of a value.
type valueShape struct {
	Int       *string `json:"int"`
	Real      *string `json:"real"`
	Text      *string `json:"text"`
	Blob      *string `json:"blob"`
	Size      *int64  `json:"size"`
	Truncated bool    `json:"truncated"`
}

func valueInvalid(what, why string) *Error {
	return errf("value_invalid", what+" is not a value SQLite can store: "+why+".",
		`Send null, a number, a string, {"int":"..."} for an integer beyond 9007199254740991, {"real":"..."}, or {"blob":"<base64>"}.`)
}

// decodeValue reads a value the client sent into what the driver binds:
// nil, int64, float64, string or []byte. what names it in error messages.
func decodeValue(what string, raw json.RawMessage) (any, *Error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, valueInvalid(what, "the text is broken")
		}
		return s, nil
	case 't', 'f':
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, valueInvalid(what, "it is not true or false")
		}
		if b {
			return int64(1), nil
		}
		return int64(0), nil
	case '{':
		var sh valueShape
		if err := json.Unmarshal(raw, &sh); err != nil {
			return nil, valueInvalid(what, "the object has the wrong form")
		}
		return decodeShape(what, sh)
	case '[':
		return nil, valueInvalid(what, "a list")
	}
	n := string(raw)
	if !strings.ContainsAny(n, ".eE") {
		i, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			return nil, valueInvalid(what, "the integer "+n+" is too big for a number; send it as {\"int\":\""+n+"\"} or as text")
		}
		return i, nil
	}
	f, err := strconv.ParseFloat(n, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return nil, valueInvalid(what, n+" is not a number")
	}
	return f, nil
}

func decodeShape(what string, sh valueShape) (any, *Error) {
	set := 0
	for _, p := range []bool{sh.Int != nil, sh.Real != nil, sh.Text != nil, sh.Blob != nil} {
		if p {
			set++
		}
	}
	if set != 1 {
		return nil, valueInvalid(what, "an object must have exactly one of int, real, text or blob")
	}
	if sh.Truncated {
		return nil, errf("value_truncated", what+" is only the start of a longer value, so it cannot be saved.",
			"Leave this value out: values shown cut off can not be changed here. Use a SQL statement for a long text or blob.")
	}
	switch {
	case sh.Int != nil:
		i, err := strconv.ParseInt(strings.TrimSpace(*sh.Int), 10, 64)
		if err != nil {
			return nil, valueInvalid(what, *sh.Int+" is not an integer in the range of 64 bits")
		}
		return i, nil
	case sh.Real != nil:
		f, err := strconv.ParseFloat(strings.TrimSpace(*sh.Real), 64)
		if err != nil || math.IsNaN(f) {
			return nil, valueInvalid(what, *sh.Real+" is not a number SQLite can store (NaN becomes NULL)")
		}
		return f, nil
	case sh.Text != nil:
		return *sh.Text, nil
	}
	b, err := base64.StdEncoding.DecodeString(*sh.Blob)
	if err != nil {
		if b, err = base64.RawStdEncoding.DecodeString(*sh.Blob); err != nil {
			return nil, valueInvalid(what, "the blob is not base64")
		}
	}
	return b, nil
}

// jsonSize is how many bytes a string takes in JSON, at most: a control
// character is 6 bytes (\u00XX), a quote or backslash 2, and a byte that is not
// valid UTF-8 becomes \ufffd, 6 bytes.
func jsonSize(s string) int {
	n := 0
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c < 0x20:
			n += 6
			i++
		case c == '"' || c == '\\':
			n += 2
			i++
		case c < utf8.RuneSelf:
			n++
			i++
		default:
			r, w := utf8.DecodeRuneInString(s[i:])
			if (r == utf8.RuneError && w == 1) || r == '\u2028' || r == '\u2029' {
				n += 6
			} else {
				n += w
			}
			i += w
		}
	}
	return n
}
