package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
)

type member struct {
	key   string
	value json.RawMessage
}

// object is a JSON object whose members keep their order and their exact values.
type object []member

var errNotObject = errors.New("not a JSON object")

func parseObject(raw []byte) (object, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return nil, errNotObject
	}
	obj := object{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		obj = append(obj, member{key: t.(string), value: v})
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the JSON object")
	}
	return obj, nil
}

func (o object) get(key string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.key == key {
			return m.value, true
		}
	}
	return nil, false
}

func (o object) count(key string) int {
	n := 0
	for _, m := range o {
		if m.key == key {
			n++
		}
	}
	return n
}

func (o *object) set(key string, value json.RawMessage) {
	for i, m := range *o {
		if m.key == key {
			(*o)[i].value = value
			return
		}
	}
	*o = append(*o, member{key, value})
}

func (o *object) del(key string) {
	out := (*o)[:0]
	for _, m := range *o {
		if m.key != key {
			out = append(out, m)
		}
	}
	*o = out
}

func (o object) marshal() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(m.key)
		b.Write(k)
		b.WriteByte(':')
		b.Write(m.value)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func marshalArray(items []json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, it := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(it)
	}
	b.WriteByte(']')
	return b.Bytes()
}

// indent formats JSON with two-space indentation, as Claude Code writes its settings.
func indent(raw []byte) ([]byte, error) {
	var compact, out bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, err
	}
	if err := json.Indent(&out, compact.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// sameJSON reports whether a and b hold the same JSON value, ignoring formatting.
func sameJSON(a, b []byte) bool {
	var x, y any
	da, db := json.NewDecoder(bytes.NewReader(a)), json.NewDecoder(bytes.NewReader(b))
	da.UseNumber()
	db.UseNumber()
	if da.Decode(&x) != nil || db.Decode(&y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}
