package lsp

import "sync"

// documents is an in-memory store of open document contents keyed by URI.
var documents sync.Map // map[string]string

func storeDoc(uri, text string) { documents.Store(uri, text) }
func deleteDoc(uri string)      { documents.Delete(uri) }

func loadDoc(uri string) (string, bool) {
	v, ok := documents.Load(uri)
	if !ok {
		return "", false
	}
	return v.(string), true
}
