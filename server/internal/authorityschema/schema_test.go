package authorityschema

import "testing"

func TestStaged95CatalogBytes(t *testing.T) {
	if e := checkBytes(); e != nil {
		t.Fatal(e)
	}
}
