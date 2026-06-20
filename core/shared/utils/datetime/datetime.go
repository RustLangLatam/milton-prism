// Package datetime provides helpers for converting between Go time.Time
// and Protocol Buffer Timestamp types.
package datetime

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func ToProtoTimestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}
