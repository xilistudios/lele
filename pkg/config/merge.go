package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

type change struct {
	path   []string
	value  interface{}
	remove bool
}

func diffJSON(base, next map[string]interface{}) []change {
	return diffJSONAt(nil, base, next)
}

func diffJSONAt(prefix []string, base, next map[string]interface{}) []change {
	changes := make([]change, 0, len(next))
	for key, nextValue := range next {
		path := appendPath(prefix, key)
		baseValue, ok := base[key]
		if !ok {
			changes = append(changes, change{path: path, value: nextValue})
			continue
		}
		baseObject, baseIsObject := baseValue.(map[string]interface{})
		nextObject, nextIsObject := nextValue.(map[string]interface{})
		if baseIsObject && nextIsObject {
			changes = append(changes, diffJSONAt(path, baseObject, nextObject)...)
			continue
		}
		if !reflect.DeepEqual(baseValue, nextValue) {
			changes = append(changes, change{path: path, value: nextValue})
		}
	}
	for key := range base {
		if _, ok := next[key]; !ok {
			changes = append(changes, change{path: appendPath(prefix, key), remove: true})
		}
	}
	return changes
}

func mergeConfigDocument(raw map[string]json.RawMessage, base, next map[string]interface{}) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return rawDocumentFromTree(next)
	}
	return applyChanges(raw, pendingChanges(raw, diffJSON(base, next)))
}

func rawDocumentFromTree(tree map[string]interface{}) (map[string]json.RawMessage, error) {
	raw := make(map[string]json.RawMessage, len(tree))
	for key, value := range tree {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		raw[key] = encoded
	}
	return raw, nil
}

func applyChanges(raw map[string]json.RawMessage, changes []change) (map[string]json.RawMessage, error) {
	merged := make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		merged[key] = value
	}
	for _, ch := range changes {
		if len(ch.path) == 0 {
			continue
		}
		root := ch.path[0]
		if len(ch.path) == 1 {
			if ch.remove {
				delete(merged, root)
				continue
			}
			encoded, err := json.Marshal(ch.value)
			if err != nil {
				return nil, err
			}
			merged[root] = encoded
			continue
		}
		object, err := decodeJSONObject(merged[root])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", root, err)
		}
		_, present := merged[root]
		before := len(object)
		applyChangeAt(object, ch.path[1:], ch)
		if ch.remove && len(object) == 0 {
			if !present || before > 0 {
				delete(merged, root)
			}
			continue
		}
		encoded, err := json.Marshal(object)
		if err != nil {
			return nil, err
		}
		merged[root] = encoded
	}
	return merged, nil
}

func applyChangeAt(object map[string]interface{}, path []string, ch change) {
	key := path[0]
	if len(path) == 1 {
		if ch.remove {
			delete(object, key)
			return
		}
		object[key] = ch.value
		return
	}
	child, ok := object[key].(map[string]interface{})
	if !ok {
		if ch.remove {
			return
		}
		child = map[string]interface{}{}
		object[key] = child
	}
	applyChangeAt(child, path[1:], ch)
}

func pendingChanges(raw map[string]json.RawMessage, changes []change) []change {
	pending := make([]change, 0, len(changes))
	for _, ch := range changes {
		if placeholderAlreadySatisfied(raw, ch) {
			continue
		}
		pending = append(pending, ch)
	}
	return pending
}

func placeholderAlreadySatisfied(raw map[string]json.RawMessage, ch change) bool {
	if ch.remove || len(ch.path) == 0 {
		return false
	}
	next, ok := ch.value.(string)
	if !ok {
		return false
	}
	current, ok := rawStringAt(raw, ch.path)
	if !ok || !strings.Contains(current, "{{ENV_") {
		return false
	}
	return expandEnvString(current) == next
}

func rawStringAt(raw map[string]json.RawMessage, path []string) (string, bool) {
	root, ok := raw[path[0]]
	if !ok {
		return "", false
	}
	if len(path) == 1 {
		var value string
		if err := json.Unmarshal(root, &value); err != nil {
			return "", false
		}
		return value, true
	}
	node, err := decodeJSONObject(root)
	if err != nil {
		return "", false
	}
	var current interface{} = node
	for _, key := range path[1:] {
		object, ok := current.(map[string]interface{})
		if !ok {
			return "", false
		}
		current, ok = object[key]
		if !ok {
			return "", false
		}
	}
	value, ok := current.(string)
	return value, ok
}

func appendPath(prefix []string, key string) []string {
	path := make([]string, len(prefix)+1)
	copy(path, prefix)
	path[len(prefix)] = key
	return path
}

func decodeJSONObject(raw json.RawMessage) (map[string]interface{}, error) {
	if len(raw) == 0 {
		return map[string]interface{}{}, nil
	}
	var object map[string]interface{}
	if err := decodeJSONValue(raw, &object); err != nil {
		return nil, err
	}
	if object == nil {
		object = map[string]interface{}{}
	}
	return object, nil
}

func decodeJSONValue(raw []byte, target interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func jsonTreeOf(value interface{}) (map[string]interface{}, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return decodeJSONObject(encoded)
}

type documentState int

const (
	documentMissing documentState = iota
	documentOK
	documentCorrupt
)

func rawDocumentFromFile(path string) (map[string]json.RawMessage, documentState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, documentMissing, nil
		}
		// unreadable is treated as corrupt: refuse to save rather than clobber
		return nil, documentCorrupt, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, documentCorrupt, err
	}
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}
	return raw, documentOK, nil
}

func writeConfigFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".lele-config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	committed = true
	return nil
}
