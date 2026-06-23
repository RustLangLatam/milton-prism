// Package repositories — unit tests for the server-side ListMigrations
// order_by parser. These verify the AIP-132 allowlist and the
// InvalidArgument-on-unknown-field contract without needing MongoDB.
package repositories

import (
	"errors"
	"testing"

	"milton_prism/core/services/migration/domain"

	"go.mongodb.org/mongo-driver/bson"
)

func TestParseOrderBy(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    bson.D
		wantErr bool
	}{
		{name: "empty defaults to create_time desc", in: "", want: bson.D{{Key: "create_time", Value: -1}}},
		{name: "whitespace only defaults", in: "   ", want: bson.D{{Key: "create_time", Value: -1}}},
		{name: "create_time desc", in: "create_time desc", want: bson.D{{Key: "create_time", Value: -1}}},
		{name: "create_time asc", in: "create_time asc", want: bson.D{{Key: "create_time", Value: 1}}},
		{name: "bare field defaults asc", in: "create_time", want: bson.D{{Key: "create_time", Value: 1}}},
		{name: "case-insensitive field+dir", in: "Create_Time DESC", want: bson.D{{Key: "create_time", Value: -1}}},
		{name: "state adds create_time tie-break", in: "state asc", want: bson.D{{Key: "state", Value: 1}, {Key: "create_time", Value: -1}}},
		{name: "topology desc with tie-break", in: "topology desc", want: bson.D{{Key: "topology", Value: -1}, {Key: "create_time", Value: -1}}},
		{name: "protocol allowed", in: "protocol asc", want: bson.D{{Key: "protocol", Value: 1}, {Key: "create_time", Value: -1}}},
		{name: "language allowed", in: "language desc", want: bson.D{{Key: "language", Value: -1}, {Key: "create_time", Value: -1}}},
		{name: "unknown field rejected", in: "owner_user_id desc", wantErr: true},
		{name: "injection attempt rejected", in: "$where desc", wantErr: true},
		{name: "bad direction rejected", in: "state sideways", wantErr: true},
		{name: "too many tokens rejected", in: "state asc extra", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseOrderBy(tt.in)
			if tt.wantErr {
				if !errors.Is(err, domain.ErrInvalidOrderBy) {
					t.Fatalf("expected ErrInvalidOrderBy, got err=%v sort=%v", err, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("sort length mismatch: got %v want %v", got, tt.want)
			}
			for i := range got {
				if got[i].Key != tt.want[i].Key || got[i].Value != tt.want[i].Value {
					t.Fatalf("sort[%d] mismatch: got %v want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
