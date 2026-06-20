// Package pointers provides helper functions for obtaining pointers to
// common scalar values, simplifying proto optional-field construction.
package pointers

func StringPtr(s string) *string {
	return &s
}

func Uint32Ptr(s uint32) *uint32 {
	return &s
}

func BoolPtr(v bool) *bool {
	return &v
}

func Int32Ptr(v int32) *int32 {
	return &v
}
