package security

import (
	"os"
	"reflect"
)

// currentUserOwns uses the platform's stat owner when available. Reflection
// keeps this package buildable on platforms whose Stat_t type differs; a
// missing owner field fails closed rather than weakening a protected-file
// boundary.
func currentUserOwns(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return false
	}
	uid := value.FieldByName("Uid")
	if !uid.IsValid() || !uid.CanUint() {
		return false
	}
	return uid.Uint() == uint64(os.Getuid())
}
