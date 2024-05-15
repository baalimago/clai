package setup

import (
	"errors"
	"reflect"
)

// doOnExportedFields by using reflection to check each field within a of type T
// returns all non-nil errors
func doOnExportedFields[T any](a T, do func(fieldname string, field any) error) []error {
	ret := make([]error, 0)
	t := reflect.TypeOf(a)
	switch t.Kind() {
	case reflect.Struct:
		for i := range t.NumField() {
			f := t.Field(i)
			k := f.Type.Kind()
			if !f.IsExported() {
				continue
			}
			val := reflect.ValueOf(a).Field(i).Interface()
			switch k {
			case reflect.Ptr:
				deRef := reflect.ValueOf(a).Field(i).Elem()
				ret = append(ret, doOnExportedFields(deRef, do)...)
			case reflect.Struct:
				ret = append(ret, doOnExportedFields(val, do)...)
			default:
				ret = append(ret, do(f.Name, val))
			}
		}
	default:
		return []error{errors.New("incorrect type")}
	}

	return ret
}
