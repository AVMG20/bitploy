package dbview

import (
	"encoding/base64"
)

// cell reads one value of one row whole, so that a long text can be edited:
// the grid only gets the first 64 KiB. A text up to MaxCellBytes comes back as
// a plain string; a blob up to MaxCellBytes as {"blob": <all of it>, "size": n,
// "truncated": false}. Anything bigger has the normal, cut-off form. The row is
// found with a key, under the rules of update.
func (s *session) cell(req Request) (any, *Error) {
	m, e := s.editableTable(req.Table)
	if e != nil {
		return nil, e
	}
	c, ok := m.column(req.Column)
	if !ok || c.Hidden {
		return nil, columnNotFound(m, req.Column)
	}
	cond, args, e := m.keyCondition(req.Key)
	if e != nil {
		return nil, e
	}
	expr := quote(c.Name)
	if isDateColumn(c) {
		expr = "ifnull(" + expr + ", NULL)"
	}
	rs, err := s.conn.QueryContext(s.ctx, "SELECT "+expr+" FROM "+quote(m.Name)+" WHERE "+cond+" LIMIT 1", args...)
	if err != nil {
		return nil, s.fail(err)
	}
	defer rs.Close()
	if !rs.Next() {
		if err := rs.Err(); err != nil {
			return nil, s.fail(err)
		}
		return nil, errf("row_not_found", "This row does not exist.", "Reload the table: someone may have deleted or changed the row.")
	}
	vals, err := scanRow(rs, 1)
	if err != nil {
		return nil, s.fail(err)
	}
	return CellResult{Value: encodeCell(vals[0])}, nil
}

// encodeCell is encodeValue with the bigger limit of a single cell.
func encodeCell(v any) any {
	switch x := v.(type) {
	case string:
		// The answer must fit the response limit once escaped.
		if len(x) <= MaxCellBytes && fitsResponse(x) {
			return x
		}
	case []byte:
		if len(x) <= MaxCellBytes && base64.StdEncoding.EncodedLen(len(x)) <= MaxResponse-(512<<10) {
			return map[string]any{"blob": base64.StdEncoding.EncodeToString(x), "size": len(x), "truncated": false}
		}
	}
	enc, _ := encodeValue(v)
	return enc
}

// fitsResponse: the text, as JSON, leaves room in a response. jsonSize is an
// upper bound; only a text that might not fit is measured exactly.
func fitsResponse(s string) bool {
	room := MaxResponse - (512 << 10)
	if len(s)*6 <= room {
		return true
	}
	b, err := encode(s)
	return err == nil && len(b) <= room
}
