package setup

import (
	"errors"
	"slices"
	"testing"
)

type testStruct[A any] struct {
	ExportedField   A
	unexportedField A
	NestedStruct    *testStruct[A]
}

func TestDoOnExportedFields(t *testing.T) {
	tests := []struct {
		name    string
		input   testStruct[string]
		doFunc  func(fieldName string, field any) error
		wantErr []error
	}{
		{
			name: "NoError",
			input: testStruct[string]{
				ExportedField:   "good",
				unexportedField: "hidden",
			},
			doFunc: func(fieldName string, field any) error {
				return nil
			},
			wantErr: make([]error, 0),
		},
		{
			name: "WithError",
			input: testStruct[string]{
				ExportedField:   "please error",
				unexportedField: "hidden",
			},
			doFunc: func(fieldName string, field any) error {
				if field.(string) == "please error" {
					return errors.New("test error")
				}
				return nil
			},
			wantErr: []error{errors.New("test error")},
		},
		{
			name: "WithNestedError",
			input: testStruct[string]{
				ExportedField:   "good",
				unexportedField: "hidden",
				NestedStruct: &testStruct[string]{
					ExportedField: "please error",
				},
			},
			doFunc: func(fieldName string, field any) error {
				if field.(string) == "please error" {
					return errors.New("test error")
				}
				return nil
			},
			wantErr: []error{errors.New("test error")},
		},
		{
			name: "WithMultipleErrors",
			input: testStruct[string]{
				ExportedField: "please error",
				NestedStruct: &testStruct[string]{
					ExportedField: "please error",
				},
			},
			wantErr: []error{errors.New("please error"), errors.New("please error")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := doOnExportedFields(tt.input, tt.doFunc)
			if slices.Equal(errs, tt.wantErr) {
				t.Errorf("doOnExportedFields() error = %v, wantErr %v", errs, tt.wantErr)
			}
		})
	}
}
