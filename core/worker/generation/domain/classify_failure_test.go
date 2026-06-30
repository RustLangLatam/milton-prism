package domain

import (
	"errors"
	"testing"
)

func TestClassifyFailure(t *testing.T) {
	cases := []struct {
		name      string
		invokeErr error
		reason    string
		want      FailureClass
	}{
		{"infra error is transient", errors.New("dial tcp: connection refused"), "", FailureClassTransient},
		{"rate limit keyword is transient", nil, "HTTP 429 Too Many Requests", FailureClassTransient},
		{"overloaded keyword is transient", nil, "the model is overloaded", FailureClassTransient},
		{"gate-red is design", nil, "go build failed: undefined: Foo", FailureClassDesign},
		{"empty reason is design", nil, "", FailureClassDesign},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyFailure(tc.invokeErr, tc.reason); got != tc.want {
				t.Fatalf("ClassifyFailure(%v, %q) = %q, want %q", tc.invokeErr, tc.reason, got, tc.want)
			}
		})
	}
}
