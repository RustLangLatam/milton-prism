package handlers

import (
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	httpbodypb "google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/encoding/protojson"
)

// HttpBodyAwareJSONPb wraps runtime.JSONPb to intercept google.api.HttpBody
// responses. grpc-gateway's ForwardResponseMessage only handles HttpBody in
// the streaming path; for unary RPCs it falls through to JSON marshaling.
// This marshaler short-circuits both ContentType and Marshal for HttpBody so
// the binary payload reaches the HTTP client with the correct Content-Type.
type HttpBodyAwareJSONPb struct {
	runtime.JSONPb
}

// NewHttpBodyAwareJSONPb returns a marshaler with the same proto options used
// by the standard gateway JSON marshaler.
func NewHttpBodyAwareJSONPb() *HttpBodyAwareJSONPb {
	return &HttpBodyAwareJSONPb{
		JSONPb: runtime.JSONPb{
			MarshalOptions: protojson.MarshalOptions{
				UseProtoNames:     false,
				EmitUnpopulated:   true,
				EmitDefaultValues: true,
				Indent:            "  ",
				Multiline:         true,
			},
			UnmarshalOptions: protojson.UnmarshalOptions{
				DiscardUnknown: true,
			},
		},
	}
}

// ContentType returns the HttpBody content-type for binary responses and
// delegates to the embedded JSONPb marshaler for everything else.
func (m *HttpBodyAwareJSONPb) ContentType(v interface{}) string {
	if hb, ok := v.(*httpbodypb.HttpBody); ok {
		return hb.GetContentType()
	}
	return m.JSONPb.ContentType(v)
}

// Marshal returns the raw bytes for HttpBody responses and delegates to
// the embedded JSONPb marshaler for everything else.
func (m *HttpBodyAwareJSONPb) Marshal(v interface{}) ([]byte, error) {
	if hb, ok := v.(*httpbodypb.HttpBody); ok {
		return hb.GetData(), nil
	}
	return m.JSONPb.Marshal(v)
}
